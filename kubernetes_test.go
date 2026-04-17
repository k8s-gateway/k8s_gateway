package gateway

import (
	"context"
	"strings"
	"testing"

	"github.com/coredns/coredns/plugin/test"
	"github.com/miekg/dns"
	core "k8s.io/api/core/v1"
	networking "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
)

func TestController(t *testing.T) {
	client := fake.NewClientset()
	ctrl := &KubeController{
		client:    client,
		hasSynced: true,
	}
	addServices(client)
	addIngresses(client)

	gw := newGateway()
	gw.Zones = []string{"example.com."}
	gw.Next = test.NextHandler(dns.RcodeSuccess, nil)
	gw.Controller = ctrl

	for index, testObj := range testIngresses {
		found, _ := ingressHostnameIndexFunc(testObj)
		if checkIgnoreLabel(testObj.Labels) {
			if len(found) != 0 {
				t.Errorf("Ignored Ingress key %s should not be found in index, but found: %v", index, found)
			}
			continue
		}
		if !isFound(index, found) {
			t.Errorf("Ingress key %s not found in index: %v", index, found)
		}
		ips := fetchIngressLoadBalancerIPs(testObj.Status.LoadBalancer.Ingress)
		if len(ips) != 1 {
			t.Errorf("Unexpected number of IPs found %d", len(ips))
		}
	}

	for index, testObj := range testServices {
		found, _ := serviceHostnameIndexFunc(testObj)
		if checkIgnoreLabel(testObj.Labels) {
			if len(found) != 0 {
				t.Errorf("Ignored Service key %s should not be found in index, but found: %v", index, found)
			}
			continue
		}
		indices := strings.Split(index, ",")
		for _, idx := range indices {
			if !isFound(strings.TrimSpace(idx), found) {
				t.Errorf("Service key %s not found in index: %v", idx, found)
			}
		}
		ips := fetchServiceLoadBalancerIPs(testObj.Status.LoadBalancer.Ingress)
		if len(ips) != 1 {
			t.Errorf("Unexpected number of IPs found %d", len(ips))
		}
	}

	for index, testObj := range testBadServices {
		found, _ := serviceHostnameIndexFunc(testObj)
		if isFound(index, found) {
			t.Errorf("Unexpected service key %s found in index: %v", index, found)
		}
	}

	for _, testObj := range testInvalidAnnotationServices {
		found, _ := serviceHostnameIndexFunc(testObj)
		if len(found) != 0 {
			t.Errorf("Unexpected non-empty service hostnames %v for invalid annotation: %v", found, testObj.Annotations)
		}
	}
}

func isFound(s string, ss []string) bool {
	for _, str := range ss {
		if str == s {
			return true
		}
	}
	return false
}

func addServices(client kubernetes.Interface) {
	ctx := context.TODO()
	for _, svc := range testServices {
		_, err := client.CoreV1().Services("ns1").Create(ctx, svc, metav1.CreateOptions{})
		if err != nil {
			log.Warningf("Failed to Create Service Objects :%s", err)
		}
	}
}

func addIngresses(client kubernetes.Interface) {
	ctx := context.TODO()
	for _, ingress := range testIngresses {
		_, err := client.NetworkingV1().Ingresses("ns1").Create(ctx, ingress, metav1.CreateOptions{})
		if err != nil {
			log.Warningf("Failed to Create Ingress Objects :%s", err)
		}
	}
}

var testIngresses = map[string]*networking.Ingress{
	"a.example.org": {
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ing1",
			Namespace: "ns1",
		},
		Spec: networking.IngressSpec{
			Rules: []networking.IngressRule{
				{
					Host: "a.example.org",
				},
			},
		},
		Status: networking.IngressStatus{
			LoadBalancer: networking.IngressLoadBalancerStatus{
				Ingress: []networking.IngressLoadBalancerIngress{
					{IP: "192.0.0.1"},
				},
			},
		},
	},
	"example.org": {
		Spec: networking.IngressSpec{
			Rules: []networking.IngressRule{
				{
					Host: "example.org",
				},
			},
		},
		Status: networking.IngressStatus{
			LoadBalancer: networking.IngressLoadBalancerStatus{
				Ingress: []networking.IngressLoadBalancerIngress{
					{IP: "192.0.0.2"},
				},
			},
		},
	},
	"ignored.example.org": {
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ignored-ingress",
			Namespace: "ns1",
			Labels: map[string]string{
				ignoreLabelKey: "true",
			},
		},
		Spec: networking.IngressSpec{
			Rules: []networking.IngressRule{
				{
					Host: "ignored.example.org",
				},
			},
		},
		Status: networking.IngressStatus{
			LoadBalancer: networking.IngressLoadBalancerStatus{
				Ingress: []networking.IngressLoadBalancerIngress{
					{IP: "192.0.0.99"},
				},
			},
		},
	},
}

var testServices = map[string]*core.Service{
	"svc1.ns1": {
		ObjectMeta: metav1.ObjectMeta{
			Name:      "svc1",
			Namespace: "ns1",
		},
		Spec: core.ServiceSpec{
			Type: core.ServiceTypeLoadBalancer,
		},
		Status: core.ServiceStatus{
			LoadBalancer: core.LoadBalancerStatus{
				Ingress: []core.LoadBalancerIngress{
					{IP: "192.0.0.1"},
				},
			},
		},
	},
	"svc2.ns1": {
		ObjectMeta: metav1.ObjectMeta{
			Name:      "svc2",
			Namespace: "ns1",
		},
		Spec: core.ServiceSpec{
			Type: core.ServiceTypeLoadBalancer,
		},
		Status: core.ServiceStatus{
			LoadBalancer: core.LoadBalancerStatus{
				Ingress: []core.LoadBalancerIngress{
					{IP: "192.0.0.2"},
				},
			},
		},
	},
	"annotation": {
		ObjectMeta: metav1.ObjectMeta{
			Name:      "svc3",
			Namespace: "ns1",
			Annotations: map[string]string{
				"coredns.io/hostname": "annotation",
			},
		},
		Spec: core.ServiceSpec{
			Type: core.ServiceTypeLoadBalancer,
		},
		Status: core.ServiceStatus{
			LoadBalancer: core.LoadBalancerStatus{
				Ingress: []core.LoadBalancerIngress{
					{IP: "192.0.0.3"},
				},
			},
		},
	},
	"*.annotation-wildcard": {
		ObjectMeta: metav1.ObjectMeta{
			Name:      "svc4",
			Namespace: "ns1",
			Annotations: map[string]string{
				"coredns.io/hostname": "*.annotation-wildcard",
			},
		},
		Spec: core.ServiceSpec{
			Type: core.ServiceTypeLoadBalancer,
		},
		Status: core.ServiceStatus{
			LoadBalancer: core.LoadBalancerStatus{
				Ingress: []core.LoadBalancerIngress{
					{IP: "192.0.0.3"},
				},
			},
		},
	},
	"annotation-list1, annotation-list2": {
		ObjectMeta: metav1.ObjectMeta{
			Name:      "svc5",
			Namespace: "ns1",
			Annotations: map[string]string{
				"coredns.io/hostname": "annotation-list1, annotation-list2",
			},
		},
		Spec: core.ServiceSpec{
			Type: core.ServiceTypeLoadBalancer,
		},
		Status: core.ServiceStatus{
			LoadBalancer: core.LoadBalancerStatus{
				Ingress: []core.LoadBalancerIngress{
					{IP: "192.0.0.3"},
				},
			},
		},
	},
	"annotation-external-dns": {
		ObjectMeta: metav1.ObjectMeta{
			Name:      "svc6",
			Namespace: "ns1",
			Annotations: map[string]string{
				"external-dns.alpha.kubernetes.io/hostname": "annotation-external-dns",
			},
		},
		Spec: core.ServiceSpec{
			Type: core.ServiceTypeLoadBalancer,
		},
		Status: core.ServiceStatus{
			LoadBalancer: core.LoadBalancerStatus{
				Ingress: []core.LoadBalancerIngress{
					{IP: "192.0.0.3"},
				},
			},
		},
	},
	"annotation-external-dns-list1,annotation-external-dns-list2": {
		ObjectMeta: metav1.ObjectMeta{
			Name:      "svc7",
			Namespace: "ns1",
			Annotations: map[string]string{
				"external-dns.alpha.kubernetes.io/hostname": "annotation-external-dns-list1,annotation-external-dns-list2",
			},
		},
		Spec: core.ServiceSpec{
			Type: core.ServiceTypeLoadBalancer,
		},
		Status: core.ServiceStatus{
			LoadBalancer: core.LoadBalancerStatus{
				Ingress: []core.LoadBalancerIngress{
					{IP: "192.0.0.3"},
				},
			},
		},
	},
	"ignored-svc.ns1": {
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ignored-svc",
			Namespace: "ns1",
			Labels: map[string]string{
				ignoreLabelKey: "true",
			},
		},
		Spec: core.ServiceSpec{
			Type: core.ServiceTypeLoadBalancer,
		},
		Status: core.ServiceStatus{
			LoadBalancer: core.LoadBalancerStatus{
				Ingress: []core.LoadBalancerIngress{
					{IP: "192.0.0.99"},
				},
			},
		},
	},
}

var testBadServices = map[string]*core.Service{
	"svc1.ns2": {
		ObjectMeta: metav1.ObjectMeta{
			Name:      "svc1",
			Namespace: "ns2",
		},
		Spec: core.ServiceSpec{
			Type: core.ServiceTypeClusterIP,
		},
		Status: core.ServiceStatus{
			LoadBalancer: core.LoadBalancerStatus{
				Ingress: []core.LoadBalancerIngress{
					{IP: "192.0.0.1"},
				},
			},
		},
	},
}

var testInvalidAnnotationServices = []*core.Service{
	{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "svc1",
			Namespace: "ns3",
			Annotations: map[string]string{
				"coredns.io/hostname": "*my.host",
			},
		},
		Spec: core.ServiceSpec{
			Type: core.ServiceTypeLoadBalancer,
		},
		Status: core.ServiceStatus{
			LoadBalancer: core.LoadBalancerStatus{
				Ingress: []core.LoadBalancerIngress{
					{IP: "192.0.0.1"},
				},
			},
		},
	},
	{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "svc2",
			Namespace: "ns3",
			Annotations: map[string]string{
				"coredns.io/hostname": "**my.host",
			},
		},
		Spec: core.ServiceSpec{
			Type: core.ServiceTypeLoadBalancer,
		},
		Status: core.ServiceStatus{
			LoadBalancer: core.LoadBalancerStatus{
				Ingress: []core.LoadBalancerIngress{
					{IP: "192.0.0.1"},
				},
			},
		},
	},
	{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "svc3",
			Namespace: "ns3",
			Annotations: map[string]string{
				"external-dns.alpha.kubernetes.io/hostname": "my.*.host",
			},
		},
		Spec: core.ServiceSpec{
			Type: core.ServiceTypeLoadBalancer,
		},
		Status: core.ServiceStatus{
			LoadBalancer: core.LoadBalancerStatus{
				Ingress: []core.LoadBalancerIngress{
					{IP: "192.0.0.1"},
				},
			},
		},
	},
}

// testNodes maps a description to a Node fixture.
var testNodes = map[string]*core.Node{
	// node with both Hostname and InternalIP + ExternalIP
	"node1": {
		ObjectMeta: metav1.ObjectMeta{Name: "node1"},
		Status: core.NodeStatus{
			Addresses: []core.NodeAddress{
				{Type: core.NodeHostName, Address: "node1"},
				{Type: core.NodeInternalIP, Address: "10.0.0.1"},
				{Type: core.NodeExternalIP, Address: "203.0.113.1"},
			},
		},
	},
	// dual-stack node: one Hostname, two InternalIPs (IPv4 + IPv6)
	"node2": {
		ObjectMeta: metav1.ObjectMeta{Name: "node2"},
		Status: core.NodeStatus{
			Addresses: []core.NodeAddress{
				{Type: core.NodeHostName, Address: "node2"},
				{Type: core.NodeInternalIP, Address: "10.0.0.2"},
				{Type: core.NodeInternalIP, Address: "fd00::2"},
			},
		},
	},
	// node without a Hostname address — must not be indexed
	"node-no-hostname": {
		ObjectMeta: metav1.ObjectMeta{Name: "node-no-hostname"},
		Status: core.NodeStatus{
			Addresses: []core.NodeAddress{
				{Type: core.NodeExternalIP, Address: "203.0.113.99"},
			},
		},
	},
	// ignored node — must not be indexed regardless of addresses
	"ignored-node": {
		ObjectMeta: metav1.ObjectMeta{
			Name:   "ignored-node",
			Labels: map[string]string{ignoreLabelKey: "true"},
		},
		Status: core.NodeStatus{
			Addresses: []core.NodeAddress{
				{Type: core.NodeHostName, Address: "ignored-node"},
				{Type: core.NodeInternalIP, Address: "10.0.0.99"},
			},
		},
	},
}

func TestFetchNodeIPsByType(t *testing.T) {
	addrs := []core.NodeAddress{
		{Type: core.NodeHostName, Address: "node1"},
		{Type: core.NodeInternalIP, Address: "10.0.0.1"},
		{Type: core.NodeInternalIP, Address: "fd00::1"},
		{Type: core.NodeExternalIP, Address: "203.0.113.1"},
	}

	internalIPs := fetchNodeIPsByType(addrs, core.NodeInternalIP)
	if len(internalIPs) != 2 {
		t.Errorf("expected 2 InternalIP addresses, got %d: %v", len(internalIPs), internalIPs)
	}

	externalIPs := fetchNodeIPsByType(addrs, core.NodeExternalIP)
	if len(externalIPs) != 1 {
		t.Errorf("expected 1 ExternalIP address, got %d: %v", len(externalIPs), externalIPs)
	}
	if externalIPs[0].String() != "203.0.113.1" {
		t.Errorf("expected ExternalIP 203.0.113.1, got %s", externalIPs[0])
	}
}

func TestNodeHostnameIndex(t *testing.T) {
	for name, node := range testNodes {
		found, err := nodeHostnameIndexFunc(node)
		if err != nil {
			t.Errorf("node %s: unexpected error: %v", name, err)
		}
		if checkIgnoreLabel(node.Labels) {
			if len(found) != 0 {
				t.Errorf("ignored node %s should not be in index, got: %v", name, found)
			}
			continue
		}
		hasHostname := false
		for _, addr := range node.Status.Addresses {
			if addr.Type == core.NodeHostName {
				hasHostname = true
				if !isFound(addr.Address, found) {
					t.Errorf("node %s: hostname %q not found in index %v", name, addr.Address, found)
				}
			}
		}
		if !hasHostname && len(found) != 0 {
			t.Errorf("node %s without Hostname address should not be indexed, got: %v", name, found)
		}
	}
}

// TestLookupNodeIndexNoHostname is a wiring regression test: a node that has
// only an ExternalIP address (no NodeHostName) must not be returned by
// lookupNodeIndex regardless of the addrType requested.
func TestLookupNodeIndexNoHostname(t *testing.T) {
	// Build a fake informer cache that holds only the no-hostname node.
	fakeIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{
		nodeHostnameIndex: nodeHostnameIndexFunc,
	})
	noHostnameNode := testNodes["node-no-hostname"]
	if err := fakeIndexer.Add(noHostnameNode); err != nil {
		t.Fatalf("failed to add node to indexer: %v", err)
	}

	// Wrap in a minimal SharedIndexInformer-alike by using a fake informer.
	// lookupNodeIndex only uses ctrl.GetIndexer(), so we can satisfy it with a
	// thin wrapper around the Indexer we already have.
	fakeInformer := &fakeSharedIndexInformer{indexer: fakeIndexer}

	lookup := lookupNodeIndex(fakeInformer, core.NodeExternalIP)
	results, _ := lookup([]string{"node-no-hostname"})
	if len(results) != 0 {
		t.Errorf("expected no results for node without NodeHostName, got: %v", results)
	}
}

// fakeSharedIndexInformer satisfies the cache.SharedIndexInformer interface
// used by lookupNodeIndex. Only GetIndexer is exercised by that function, so
// the rest of the interface is satisfied by embedding the real type without
// implementing any other methods.
type fakeSharedIndexInformer struct {
	cache.SharedIndexInformer
	indexer cache.Indexer
}

func (f *fakeSharedIndexInformer) GetIndexer() cache.Indexer { return f.indexer }
