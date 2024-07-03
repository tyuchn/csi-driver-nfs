package lbcontroller

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
)

func NewFakeLBController(ipMap map[string]int, nodes []runtime.Object) *LBController {
	client := fake.NewSimpleClientset(nodes...)
	factory := informers.NewSharedInformerFactory(client, time.Hour /* disable resync*/)
	nodeInformer := factory.Core().V1().Nodes()

	for _, obj := range nodes {
		switch obj.(type) {
		case *v1.Node:
			nodeInformer.Informer().GetStore().Add(obj)
		default:
			break
		}
	}

	return &LBController{
		ipMap:      ipMap,
		clientset:  client,
		nodeLister: nodeInformer.Lister(),
	}
}

type testNode struct {
	name       string
	assignedIP string
}

func newNode(name, assignedIP string) *v1.Node {
	node := v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}

	if assignedIP != "" {
		node.ObjectMeta.Annotations = map[string]string{NodeAnnotation: assignedIP}
	}

	return &node
}

func newNodePool(fakeNodes []testNode) []runtime.Object {
	var nodePool []runtime.Object
	for _, fn := range fakeNodes {
		node := newNode(fn.name, fn.assignedIP)
		nodePool = append(nodePool, node)
	}

	return nodePool
}

func TestResyncIPMap(t *testing.T) {
	cases := []struct {
		name         string
		ipList       []string
		clusterNodes []testNode
		expectedMap  map[string]int
		expectedErr  bool
	}{
		{
			name:   "Nodes do not have annotation",
			ipList: []string{},
			clusterNodes: []testNode{
				{
					name: "node-1",
				},
			},
			expectedMap: map[string]int{},
		},
		{
			name:   "Nodes have IP annotation, IP not in the ipList",
			ipList: []string{"127.0.0.1"},
			clusterNodes: []testNode{
				{
					name:       "node-1",
					assignedIP: "127.0.0.0",
				},
			},
			expectedMap: map[string]int{"127.0.0.1": 0},
		},
		{
			name:   "Nodes have IP annotation, IP in the ipList",
			ipList: []string{"127.0.0.1"},
			clusterNodes: []testNode{
				{
					name:       "node-1",
					assignedIP: "127.0.0.1",
				},
			},
			expectedMap: map[string]int{"127.0.0.1": 1},
		},
		{
			name:   "Nodes have IP annotation, part of the IPs are in the ipList",
			ipList: []string{"127.0.0.1", "192.168.1.1"},
			clusterNodes: []testNode{
				{
					name:       "node-1",
					assignedIP: "127.0.0.1",
				},
				{
					name:       "node-2",
					assignedIP: "127.0.0.0",
				},
				{
					name: "node-3",
				},
			},
			expectedMap: map[string]int{"127.0.0.1": 1, "192.168.1.1": 0},
		},
	}
	for _, test := range cases {
		nodePool := newNodePool(test.clusterNodes)
		lbController := NewFakeLBController(map[string]int{}, nodePool)
		ipMap, err := lbController.resyncIPMap(test.ipList)
		if gotExpected := gotExpectedError(test.name, test.expectedErr, err); gotExpected != nil {
			t.Fatal(gotExpected)
		}
		if diff := cmp.Diff(test.expectedMap, ipMap); diff != "" {
			t.Errorf("test %q failed: unexpected diff (-want +got):\n%s", test.name, diff)
		}
	}
}

func TestAssignIPToNode(t *testing.T) {
	cases := []struct {
		name         string
		ipMap        map[string]int
		clusterNodes []testNode
		nodeName     string
		expectedIP   string
		expectedMap  map[string]int
		expectedErr  bool
		// TODO: validate the updated node object.
	}{
		{
			name: "Node already have IP assigned, IP found in ipMap",
			ipMap: map[string]int{
				"127.0.0.1": 1,
				"127.0.0.2": 1,
				"127.0.0.3": 1,
			},
			clusterNodes: []testNode{
				{
					name:       "node-1",
					assignedIP: "127.0.0.2",
				},
				{
					name:       "node-2",
					assignedIP: "127.0.0.1",
				},
				{
					name:       "node-3",
					assignedIP: "127.0.0.3",
				},
			},
			nodeName:   "node-1",
			expectedIP: "127.0.0.2",
			expectedMap: map[string]int{
				"127.0.0.1": 1,
				"127.0.0.2": 1,
				"127.0.0.3": 1,
			},
		},
		{
			name: "Node already have IP assigned, IP not found in ipMap",
			ipMap: map[string]int{
				"127.0.0.1": 1,
				"127.0.0.2": 0,
				"127.0.0.3": 1,
			},
			clusterNodes: []testNode{
				{
					name:       "node-1",
					assignedIP: "192.168.1.1",
				},
				{
					name:       "node-2",
					assignedIP: "127.0.0.1",
				},
				{
					name:       "node-3",
					assignedIP: "127.0.0.3",
				},
			},
			nodeName:   "node-1",
			expectedIP: "127.0.0.2",
			expectedMap: map[string]int{
				"127.0.0.1": 1,
				"127.0.0.2": 1,
				"127.0.0.3": 1,
			},
		},
		{
			name: "Node doesn't have IP assigned",
			ipMap: map[string]int{
				"127.0.0.1": 1,
				"127.0.0.2": 0,
				"127.0.0.3": 1,
			},
			clusterNodes: []testNode{
				{
					name: "node-1",
				},
				{
					name:       "node-2",
					assignedIP: "127.0.0.1",
				},
				{
					name:       "node-3",
					assignedIP: "127.0.0.3",
				},
			},
			nodeName:   "node-1",
			expectedIP: "127.0.0.2",
			expectedMap: map[string]int{
				"127.0.0.1": 1,
				"127.0.0.2": 1,
				"127.0.0.3": 1,
			},
		},
		// TODO: add a negative test case when node update fail.
	}
	for _, test := range cases {
		nodePool := newNodePool(test.clusterNodes)
		lbController := NewFakeLBController(test.ipMap, nodePool)
		ctx := context.Background()
		ip, err := lbController.AssignIPToNode(ctx, test.nodeName)
		if gotExpected := gotExpectedError(test.name, test.expectedErr, err); gotExpected != nil {
			t.Fatal(gotExpected)
		}
		if diff := cmp.Diff(test.expectedIP, ip); diff != "" {
			t.Errorf("test %q failed: unexpected diff (-want +got):\n%s", test.name, diff)
		}
		if diff := cmp.Diff(test.expectedMap, lbController.ipMap); diff != "" {
			t.Errorf("test %q failed: unexpected diff (-want +got):\n%s", test.name, diff)
		}
	}
}

func TestRemoveIPFromNode(t *testing.T) {
	cases := []struct {
		name         string
		ipMap        map[string]int
		clusterNodes []testNode
		nodeName     string
		expectedMap  map[string]int
		expectedErr  bool
	}{
		{
			name: "Node does not have annotation",
			ipMap: map[string]int{
				"127.0.0.1": 1,
				"127.0.0.2": 0,
				"127.0.0.3": 1,
			},
			clusterNodes: []testNode{
				{
					name: "node-1",
				},
				{
					name:       "node-2",
					assignedIP: "127.0.0.1",
				},
				{
					name:       "node-3",
					assignedIP: "127.0.0.3",
				},
			},
			nodeName: "node-1",
			expectedMap: map[string]int{
				"127.0.0.1": 1,
				"127.0.0.2": 0,
				"127.0.0.3": 1,
			},
		},
		{
			name: "Node has IP annotation, but IP not found in ipMap",
			ipMap: map[string]int{
				"127.0.0.1": 1,
				"127.0.0.2": 0,
				"127.0.0.3": 1,
			},
			clusterNodes: []testNode{
				{
					name:       "node-1",
					assignedIP: "192.168.1.1",
				},
				{
					name:       "node-2",
					assignedIP: "127.0.0.1",
				},
				{
					name:       "node-3",
					assignedIP: "127.0.0.3",
				},
			},
			nodeName: "node-1",
			expectedMap: map[string]int{
				"127.0.0.1": 1,
				"127.0.0.2": 0,
				"127.0.0.3": 1,
			},
		},
		{
			name: "Node has IP annotation, IP exist in ipMap",
			ipMap: map[string]int{
				"127.0.0.1": 1,
				"127.0.0.2": 1,
				"127.0.0.3": 1,
			},
			clusterNodes: []testNode{
				{
					name:       "node-1",
					assignedIP: "127.0.0.2",
				},
				{
					name:       "node-2",
					assignedIP: "127.0.0.1",
				},
				{
					name:       "node-3",
					assignedIP: "127.0.0.3",
				},
			},
			nodeName: "node-1",
			expectedMap: map[string]int{
				"127.0.0.1": 1,
				"127.0.0.2": 0,
				"127.0.0.3": 1,
			},
		},
	}
	for _, test := range cases {
		nodePool := newNodePool(test.clusterNodes)
		lbController := NewFakeLBController(test.ipMap, nodePool)
		ctx := context.Background()
		err := lbController.RemoveIPFromNode(ctx, test.nodeName)
		if gotExpected := gotExpectedError(test.name, test.expectedErr, err); gotExpected != nil {
			t.Fatal(gotExpected)
		}
		if diff := cmp.Diff(test.expectedMap, lbController.ipMap); diff != "" {
			t.Errorf("test %q failed: unexpected diff (-want +got):\n%s", test.name, diff)
		}
	}
}

func gotExpectedError(testFunc string, wantErr bool, err error) error {
	if err != nil && !wantErr {
		return fmt.Errorf("%s got error %v, want nil", testFunc, err)
	}
	if err == nil && wantErr {
		return fmt.Errorf("%s got nil, want error", testFunc)
	}
	return nil
}
