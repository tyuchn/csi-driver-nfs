package nfs

import (
	"fmt"
	"os"

	"golang.org/x/net/context"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

type NodeLB struct {
	nodeName string
	client   *kubernetes.Clientset
}

func InitNodeLB() *NodeLB {
	nodeName := os.Getenv("NODE_ID")
	if nodeName == "" {
		klog.Fatal("The NODE_ID environment variable must be set when using --enable-node-lb")
	}

	klog.Infof("Building kube configs for running in cluster...")
	config, err := rest.InClusterConfig()
	if err != nil {
		klog.Fatalf("Failed to create config: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Fatalf("Failed to create client: %v", err)
	}

	return &NodeLB{
		nodeName: nodeName,
		client:   clientset,
	}
}

func (lb *NodeLB) getIP() (string, error) {
	ctx := context.Background()
	node, err := lb.client.CoreV1().Nodes().Get(ctx, lb.nodeName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("no assigned IP found for node %s", lb.nodeName)
	}

	ip, ok := node.Annotations["nfs.csi.k8s.io/assigned-ip"]
	if !ok {
		return "", fmt.Errorf("no assigned IP found for node %s", lb.nodeName)
	}

	return ip, nil
}
