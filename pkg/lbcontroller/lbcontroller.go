package lbcontroller

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	listersv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
)

const (
	NodeAnnotation = "nfs.csi.k8s.io/assigned-ip"
)

type LBController struct {
	clientset  kubernetes.Interface
	nodeLister listersv1.NodeLister
	ipMap      map[string]int
	mutex      sync.Mutex
}

func NewLBController(ipList []string) *LBController {
	klog.Infof("Building kube configs for running in cluster...")
	config, err := rest.InClusterConfig()
	if err != nil {
		klog.Fatalf("Failed to create config: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Fatalf("Failed to create client: %v", err)
	}

	ctx := signals.SetupSignalHandler()
	sharedInformerFactory := informers.NewSharedInformerFactory(clientset, 10*time.Minute /*Resync interval of the informer*/)
	nodeLister := sharedInformerFactory.Core().V1().Nodes().Lister()
	stopCh := ctx.Done()
	sharedInformerFactory.Start(stopCh)
	sharedInformerFactory.WaitForCacheSync(stopCh)

	lbc := LBController{
		clientset:  clientset,
		nodeLister: nodeLister,
	}

	ipMap, err := lbc.resyncIPMap(ipList)

	if err != nil {
		klog.Fatalf("Failed to resync LB Controller cache: %v", err)
	}

	lbc.ipMap = ipMap

	return &lbc
}

func (c *LBController) resyncIPMap(ipList []string) (map[string]int, error) {
	clusterNodes, err := c.nodeLister.List(labels.Everything())
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster nodes: %w", err)
	}

	c.mutex.Lock()
	defer c.mutex.Unlock()

	ipMap := make(map[string]int)
	for _, ip := range ipList {
		ipMap[ip] = 0
	}

	for _, node := range clusterNodes {
		if ip, exists := node.Annotations[NodeAnnotation]; exists {
			klog.Infof("Node %q already have IP %q assigned", node.Name, ip)
			if _, exists := ipMap[ip]; exists {
				ipMap[ip]++
			}
		}
	}

	klog.Infof("LB controller ipMap resynced: %v", ipMap)

	return ipMap, nil
}

func (c *LBController) AssignIPToNode(ctx context.Context, nodeName string) (string, error) {
	node, err := c.nodeLister.Get(nodeName)
	if err != nil {
		return "", err
	}

	c.mutex.Lock()
	defer c.mutex.Unlock()

	if ip, exists := node.Annotations[NodeAnnotation]; exists {
		klog.Infof("Node %q already have IP %q assigned", node.Name, ip)
		if _, exists := c.ipMap[ip]; exists {
			return ip, nil
		}
		klog.Infof("IP %q not found among the NFS server IP list. Reassigning a new IP to node %q", ip, node.Name)
	}

	// Sort IPs by their current count.
	ips := make([]string, 0, len(c.ipMap))
	for ip := range c.ipMap {
		ips = append(ips, ip)
	}
	sort.Slice(ips, func(i, j int) bool {
		return c.ipMap[ips[i]] < c.ipMap[ips[j]]
	})
	selectedIP := ips[0]

	nodeCopy := node.DeepCopy()
	if node.Annotations == nil {
		nodeCopy.Annotations = make(map[string]string)
	}
	nodeCopy.Annotations[NodeAnnotation] = selectedIP

	klog.Infof("Assigning %q to node %q", selectedIP, node.Name)

	_, err = c.clientset.CoreV1().Nodes().Update(ctx, nodeCopy, metav1.UpdateOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to assign IP %q to node %q: %v", selectedIP, node.Name, err)
	}

	c.ipMap[selectedIP]++

	klog.Infof("Updated LB controller map is %v", c.ipMap)

	return selectedIP, nil
}

func (c *LBController) RemoveIPFromNode(ctx context.Context, nodeName string) error {
	node, err := c.nodeLister.Get(nodeName)
	if err != nil {
		return err
	}

	ip, ok := node.Annotations[NodeAnnotation]
	if !ok {
		klog.Infof("Node %q does not have annotation %q, skip RemoveIPFromNode", nodeName, NodeAnnotation)

		return nil
	}

	c.mutex.Lock()
	defer c.mutex.Unlock()

	if _, exists := c.ipMap[ip]; !exists {
		klog.Infof("%q does not exist in LB controller IP map, skip RemoveIPFromNode", ip)

		return nil
	}

	klog.Infof("Removing IP annotation %q from node %q", ip, nodeName)

	nodeCopy := node.DeepCopy()
	delete(nodeCopy.Annotations, NodeAnnotation)
	_, err = c.clientset.CoreV1().Nodes().Update(ctx, nodeCopy, metav1.UpdateOptions{})
	if err != nil {
		return err
	}

	c.ipMap[ip]--

	klog.Infof("ControllerUnpublishVolume successfully removed IP %q from node %q. The updated LB controller map is %v", ip, nodeName, c.ipMap)

	return nil
}
