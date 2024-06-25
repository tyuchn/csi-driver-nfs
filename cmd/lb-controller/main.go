package main

import (
	"context"
	"flag"
	"fmt"
	"sort"
	"strings"
	"sync"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
)

const (
	nodeAnnotation = "nfs.csi.k8s.io/assigned-ip"
)

var (
	kubeconfigPath = flag.String("kubeconfig-path", "", "kubeconfig path")
	ipAddresses    = flag.String("ip-addresses", "", "Comma-separated list of IP addresses")
	workers        = flag.Int("workers", 4, "number of workers")
)

func main() {
	klog.InitFlags(nil)
	flag.Parse()

	if *ipAddresses == "" {
		klog.Fatal("IP addresses are required")
	}

	ipList := strings.Split(*ipAddresses, ",")

	var err error
	var rc *rest.Config
	if *kubeconfigPath != "" {
		klog.V(4).Infof("using kubeconfig path %q", *kubeconfigPath)
		rc, err = clientcmd.BuildConfigFromFlags("", *kubeconfigPath)
	} else {
		klog.V(4).Info("Using in-cluster kubeconfig")
		rc, err = rest.InClusterConfig()
	}
	if err != nil {
		klog.Fatalf("Failed to read kubeconfig: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(rc)
	if err != nil {
		klog.Fatalf("Failed to build kubernetes clientset: %v", err)
	}

	controller := NewController(clientset, ipList)

	stopCh := make(chan struct{})
	defer close(stopCh)

	go controller.Run(stopCh, *workers)

	// Wait forever
	select {}
}

type Controller struct {
	clientset    *kubernetes.Clientset
	nodeInformer cache.SharedIndexInformer
	workqueue    workqueue.RateLimitingInterface
	ipMap        map[string]int
	mutex        sync.Mutex
}

func NewController(clientset *kubernetes.Clientset, ipList []string) *Controller {
	informerFactory := informers.NewSharedInformerFactory(clientset, 0)
	nodeInformer := informerFactory.Core().V1().Nodes().Informer()

	ipMap := make(map[string]int)
	for _, ip := range ipList {
		ipMap[ip] = 0
	}

	// Initialize work queue
	workqueue := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())

	return &Controller{
		clientset:    clientset,
		nodeInformer: nodeInformer,
		workqueue:    workqueue,
		ipMap:        ipMap,
	}
}

func (c *Controller) Run(stopCh <-chan struct{}, workers int) {
	c.nodeInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    c.onNodeAdd,
		DeleteFunc: c.onNodeDelete,
	})

	go c.nodeInformer.Run(stopCh)

	// Wait for the caches to be synced before initializing
	if !cache.WaitForCacheSync(stopCh, c.nodeInformer.HasSynced) {
		klog.Fatalf("Timed out waiting for informer cache to sync")
	}

	// Start workers
	for i := 0; i < workers; i++ { // Number of workers
		go c.runWorker()
	}

	<-stopCh
}

func (c *Controller) onNodeAdd(obj interface{}) {
	node := obj.(*corev1.Node)
	key, err := cache.MetaNamespaceKeyFunc(node)
	if err != nil {
		klog.Errorf("Failed to get key for node %v: %v", node, err)
		return
	}
	c.workqueue.AddRateLimited(key)
}

func (c *Controller) runWorker() {
	for c.processNextWorkItem() {
	}
}

func (c *Controller) processNextWorkItem() bool {
	key, shutdown := c.workqueue.Get()
	if shutdown {
		return false
	}
	defer c.workqueue.Done(key)

	err := c.syncNode(key.(string))
	if err != nil {
		klog.Errorf("Error syncing node %v: %v", key, err)
		// Re-add the item to the work queue to retry after a delay
		c.workqueue.AddRateLimited(key)
		return true
	}

	c.workqueue.Forget(key)
	return true
}

func (c *Controller) syncNode(key string) error {
	obj, exists, err := c.nodeInformer.GetIndexer().GetByKey(key)
	if err != nil {
		klog.Errorf("Failed to get node from indexer: %v", err)
		return err
	}
	if !exists {
		// Node was deleted
		klog.Infof("Node %q no longer exists", key)
		return nil
	}

	node := obj.(*corev1.Node)
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if err := c.assignIPToNode(node.DeepCopy()); err != nil {
		return err
	}

	return nil
}

func (c *Controller) onNodeDelete(obj interface{}) {
	node := obj.(*corev1.Node)
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if ip, exists := node.Annotations[nodeAnnotation]; exists {
		klog.Infof("Updating LB controller map because node %q with assigned IP %q has been deleted", node.Name, ip)
		if _, exists := c.ipMap[ip]; exists {
			c.ipMap[ip]--
			klog.Infof("Updated LB controller map after node deletion is %v", c.ipMap)
		}

	}
}

func (c *Controller) assignIPToNode(node *corev1.Node) error {
	if ip, exists := node.Annotations[nodeAnnotation]; exists {
		klog.Infof("Node %q already have IP %q assigned", node.Name, ip)
		if _, exists := c.ipMap[ip]; exists {
			c.ipMap[ip]++

			return nil
		}
		klog.Infof("IP %q not found among the NFS server IP list. Reassigning a new IP to node %q", ip, node.Name)
	}
	// Sort IPs by their current count
	ips := make([]string, 0, len(c.ipMap))
	for ip := range c.ipMap {
		ips = append(ips, ip)
	}
	sort.Slice(ips, func(i, j int) bool {
		return c.ipMap[ips[i]] < c.ipMap[ips[j]]
	})

	selectedIP := ips[0]

	if node.Annotations == nil {
		node.Annotations = make(map[string]string)
	}
	node.Annotations[nodeAnnotation] = selectedIP

	klog.Infof("Assigning %q to node %q", selectedIP, node.Name)

	_, err := c.clientset.CoreV1().Nodes().Update(context.TODO(), node, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to assign IP %q to node %q: %v", selectedIP, node.Name, err)
	}

	c.ipMap[selectedIP]++

	klog.Infof("Updated LB controller map is %v", c.ipMap)

	return nil
}
