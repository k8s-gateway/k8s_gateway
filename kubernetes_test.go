package gateway

import (
	"context"
	"strings"
	"testing"

	"github.com/coredns/coredns/plugin/test"
	"github.com/miekg/dns"
	core "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1"
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

func TestServiceLabelSelector(t *testing.T) {
	ctx := context.TODO()

	service1 := &core.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "service1",
			Namespace: "default",
			Labels:    map[string]string{"app": "service1", "tier": "frontend"},
			Annotations: map[string]string{
				hostnameAnnotationKey: "service1.example.com",
			},
		},
		Spec: core.ServiceSpec{Type: core.ServiceTypeLoadBalancer},
		Status: core.ServiceStatus{
			LoadBalancer: core.LoadBalancerStatus{
				Ingress: []core.LoadBalancerIngress{{IP: "10.0.0.1"}},
			},
		},
	}

	service2 := &core.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "service2",
			Namespace: "default",
			Labels:    map[string]string{"app": "service2", "tier": "backend"},
			Annotations: map[string]string{
				hostnameAnnotationKey: "service2.example.com",
			},
		},
		Spec: core.ServiceSpec{Type: core.ServiceTypeLoadBalancer},
		Status: core.ServiceStatus{
			LoadBalancer: core.LoadBalancerStatus{
				Ingress: []core.LoadBalancerIngress{{IP: "10.0.0.2"}},
			},
		},
	}

	service3 := &core.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "service3",
			Namespace: "default",
			Annotations: map[string]string{
				hostnameAnnotationKey: "service3.example.com",
			},
		},
		Spec: core.ServiceSpec{Type: core.ServiceTypeLoadBalancer},
		Status: core.ServiceStatus{
			LoadBalancer: core.LoadBalancerStatus{
				Ingress: []core.LoadBalancerIngress{{IP: "10.0.0.3"}},
			},
		},
	}

	tests := []struct {
		name          string
		selector      string
		expectedCount int
		expectedNames []string
	}{
		{
			name:          "empty selector returns all services",
			selector:      "",
			expectedCount: 3,
			expectedNames: []string{"service1", "service2", "service3"},
		},
		{
			name:          "equality selector matches one service",
			selector:      "app=service1",
			expectedCount: 1,
			expectedNames: []string{"service1"},
		},
		{
			name:          "set-based selector matches multiple services",
			selector:      "app in (service1,service2)",
			expectedCount: 2,
			expectedNames: []string{"service1", "service2"},
		},
		{
			name:          "selector with no matches returns empty",
			selector:      "app=service4",
			expectedCount: 0,
			expectedNames: []string{},
		},
		{
			name:          "compound selector narrows results",
			selector:      "app=service1,tier=frontend",
			expectedCount: 1,
			expectedNames: []string{"service1"},
		},
		{
			name:          "inequality selector excludes matching value",
			selector:      "app!=service2",
			expectedCount: 2,
			expectedNames: []string{"service1", "service3"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := fake.NewClientset(service1, service2, service3)

			lister := serviceLister(ctx, client, core.NamespaceAll, tc.selector)
			result, err := lister(metav1.ListOptions{})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			serviceList, ok := result.(*core.ServiceList)
			if !ok {
				t.Fatalf("expected *core.ServiceList, got %T", result)
			}

			if len(serviceList.Items) != tc.expectedCount {
				names := make([]string, len(serviceList.Items))
				for i, svc := range serviceList.Items {
					names[i] = svc.Name
				}
				t.Errorf("expected %d services, got %d: %v", tc.expectedCount, len(serviceList.Items), names)
			}

			for _, expectedName := range tc.expectedNames {
				found := false
				for _, svc := range serviceList.Items {
					if svc.Name == expectedName {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected service %q not found in results", expectedName)
				}
			}
		})
	}
}

func TestMultiSelectorServiceLookup(t *testing.T) {
	service1 := &core.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "service1",
			Namespace: "default",
			Labels:    map[string]string{"app": "service1"},
			Annotations: map[string]string{
				hostnameAnnotationKey: "service1.example.com",
			},
		},
		Spec: core.ServiceSpec{Type: core.ServiceTypeLoadBalancer},
		Status: core.ServiceStatus{
			LoadBalancer: core.LoadBalancerStatus{
				Ingress: []core.LoadBalancerIngress{{IP: "10.0.0.1"}},
			},
		},
	}

	service2 := &core.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "service2",
			Namespace: "default",
			Labels:    map[string]string{"app": "service2"},
			Annotations: map[string]string{
				hostnameAnnotationKey: "service2.example.com",
			},
		},
		Spec: core.ServiceSpec{Type: core.ServiceTypeLoadBalancer},
		Status: core.ServiceStatus{
			LoadBalancer: core.LoadBalancerStatus{
				Ingress: []core.LoadBalancerIngress{{IP: "10.0.0.2"}},
			},
		},
	}

	service3 := &core.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "service3",
			Namespace: "default",
			Labels:    map[string]string{"app": "service3"},
			Annotations: map[string]string{
				hostnameAnnotationKey: "service3.example.com",
			},
		},
		Spec: core.ServiceSpec{Type: core.ServiceTypeLoadBalancer},
		Status: core.ServiceStatus{
			LoadBalancer: core.LoadBalancerStatus{
				Ingress: []core.LoadBalancerIngress{{IP: "10.0.0.3"}},
			},
		},
	}

	// Two indexers with disjoint selectors: app=service1 and app=service2.
	// service3 matches neither.

	indexer1 := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{
		serviceHostnameIndex: serviceHostnameIndexFunc,
	})
	if err := indexer1.Add(service1); err != nil {
		t.Fatalf("failed to add service to indexer1: %v", err)
	}

	indexer2 := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{
		serviceHostnameIndex: serviceHostnameIndexFunc,
	})
	if err := indexer2.Add(service2); err != nil {
		t.Fatalf("failed to add service to indexer2: %v", err)
	}

	_ = service3 // not added to any indexer

	controllers := []cache.SharedIndexInformer{
		&fakeSharedIndexInformer{indexer: indexer1},
		&fakeSharedIndexInformer{indexer: indexer2},
	}

	endpointSliceIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{
		endpointSliceServiceIndex: endpointSliceServiceIndexFunc,
	})
	endpointSliceInformer := &fakeSharedIndexInformer{indexer: endpointSliceIndexer}

	lookup := lookupServiceIndex(controllers, endpointSliceInformer)

	t.Run("union of disjoint selectors returns both services", func(t *testing.T) {
		results1, _ := lookup([]string{"service1.example.com"})
		if len(results1) != 1 || results1[0].String() != "10.0.0.1" {
			t.Errorf("expected [10.0.0.1], got %v", results1)
		}

		results2, _ := lookup([]string{"service2.example.com"})
		if len(results2) != 1 || results2[0].String() != "10.0.0.2" {
			t.Errorf("expected [10.0.0.2], got %v", results2)
		}

		results3, _ := lookup([]string{"service3.example.com"})
		if len(results3) != 0 {
			t.Errorf("expected no results for service3, got %v", results3)
		}
	})

	t.Run("deduplication across overlapping informers", func(t *testing.T) {
		if err := indexer2.Add(service1); err != nil {
			t.Fatalf("failed to add duplicate: %v", err)
		}
		results, _ := lookup([]string{"service1.example.com"})
		if len(results) != 1 {
			t.Errorf("expected 1 result after dedup, got %d: %v", len(results), results)
		}
	})
}

func TestResolveEndpointsRequested(t *testing.T) {
	cases := []struct {
		name        string
		annotations map[string]string
		expected    bool
	}{
		{"opt-in true", map[string]string{resolveEndpointsAnnotationKey: "true"}, true},
		{"explicit false", map[string]string{resolveEndpointsAnnotationKey: "false"}, false},
		{"non-true value", map[string]string{resolveEndpointsAnnotationKey: "yes"}, false},
		{"absent", map[string]string{}, false},
		{"nil annotations", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service := &core.Service{ObjectMeta: metav1.ObjectMeta{Annotations: tc.annotations}}
			if got := resolveEndpointsRequested(service); got != tc.expected {
				t.Errorf("resolveEndpointsRequested = %v, want %v", got, tc.expected)
			}
		})
	}
}

func TestIsLoadBalancerService(t *testing.T) {
	lb := &core.Service{Spec: core.ServiceSpec{Type: core.ServiceTypeLoadBalancer}}
	if !isLoadBalancerService(lb) {
		t.Errorf("expected LoadBalancer service to report true")
	}
	clusterIP := &core.Service{Spec: core.ServiceSpec{Type: core.ServiceTypeClusterIP}}
	if isLoadBalancerService(clusterIP) {
		t.Errorf("expected ClusterIP service to report false")
	}
}

func TestEndpointSliceServiceIndexFunc(t *testing.T) {
	t.Run("valid service-name label", func(t *testing.T) {
		es := &discovery.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "backend-abc12",
				Namespace: "default",
				Labels:    map[string]string{discovery.LabelServiceName: "backend"},
			},
		}
		keys, err := endpointSliceServiceIndexFunc(es)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(keys) != 1 || keys[0] != "default/backend" {
			t.Errorf("expected [default/backend], got %v", keys)
		}
	})

	t.Run("missing service-name label", func(t *testing.T) {
		es := &discovery.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{Namespace: "default", Labels: map[string]string{"other": "x"}},
		}
		keys, _ := endpointSliceServiceIndexFunc(es)
		if len(keys) != 0 {
			t.Errorf("expected no keys, got %v", keys)
		}
	})

	t.Run("nil labels", func(t *testing.T) {
		es := &discovery.EndpointSlice{ObjectMeta: metav1.ObjectMeta{Namespace: "default"}}
		keys, _ := endpointSliceServiceIndexFunc(es)
		if len(keys) != 0 {
			t.Errorf("expected no keys, got %v", keys)
		}
	})

	t.Run("wrong object type", func(t *testing.T) {
		keys, _ := endpointSliceServiceIndexFunc(&core.Service{})
		if len(keys) != 0 {
			t.Errorf("expected no keys for non-EndpointSlice object, got %v", keys)
		}
	})
}

func TestEndpointSliceAddresses(t *testing.T) {
	ready := true
	notReady := false

	es1 := &discovery.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backend-1",
			Namespace: "default",
			Labels:    map[string]string{discovery.LabelServiceName: "backend"},
		},
		Endpoints: []discovery.Endpoint{
			{Addresses: []string{"10.1.0.1"}, Conditions: discovery.EndpointConditions{Ready: &ready}},
			{Addresses: []string{"10.1.0.2"}, Conditions: discovery.EndpointConditions{Ready: &notReady}}, // excluded
			{Addresses: []string{"10.1.0.3"}},                                                             // nil Ready -> treated as ready
			{Addresses: []string{"fd00::1"}, Conditions: discovery.EndpointConditions{Ready: &ready}},     // IPv6
			{Addresses: []string{"not-an-ip"}, Conditions: discovery.EndpointConditions{Ready: &ready}},   // skipped
		},
	}
	es2 := &discovery.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backend-2",
			Namespace: "default",
			Labels:    map[string]string{discovery.LabelServiceName: "backend"},
		},
		Endpoints: []discovery.Endpoint{
			{Addresses: []string{"10.1.0.1"}, Conditions: discovery.EndpointConditions{Ready: &ready}}, // duplicate across slices
			{Addresses: []string{"10.1.0.4"}, Conditions: discovery.EndpointConditions{Ready: &ready}},
		},
	}

	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{
		endpointSliceServiceIndex: endpointSliceServiceIndexFunc,
	})
	if err := indexer.Add(es1); err != nil {
		t.Fatalf("failed to add es1: %v", err)
	}
	if err := indexer.Add(es2); err != nil {
		t.Fatalf("failed to add es2: %v", err)
	}
	informer := &fakeSharedIndexInformer{indexer: indexer}

	service := &core.Service{ObjectMeta: metav1.ObjectMeta{Name: "backend", Namespace: "default"}}
	addrs := endpointSliceAddresses(informer, service)

	got := make(map[string]bool, len(addrs))
	for _, a := range addrs {
		got[a.String()] = true
	}

	want := []string{"10.1.0.1", "10.1.0.3", "fd00::1", "10.1.0.4"}
	if len(addrs) != len(want) {
		t.Fatalf("expected %d addresses %v, got %d: %v", len(want), want, len(addrs), addrs)
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("expected address %s in result, got %v", w, addrs)
		}
	}
	if got["10.1.0.2"] {
		t.Errorf("not-ready address 10.1.0.2 should be excluded, got %v", addrs)
	}
}

func TestLookupServiceIndexResolvesEndpoints(t *testing.T) {
	ready := true

	// Headless service opting in to endpoint resolution (no LoadBalancer ingress).
	service := &core.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "backend",
			Namespace:   "default",
			Annotations: map[string]string{resolveEndpointsAnnotationKey: "true"},
		},
		Spec: core.ServiceSpec{Type: core.ServiceTypeClusterIP, ClusterIP: core.ClusterIPNone},
	}

	serviceIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{
		serviceHostnameIndex: serviceHostnameIndexFunc,
	})
	if err := serviceIndexer.Add(service); err != nil {
		t.Fatalf("failed to add service: %v", err)
	}

	es := &discovery.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backend-1",
			Namespace: "default",
			Labels:    map[string]string{discovery.LabelServiceName: "backend"},
		},
		Endpoints: []discovery.Endpoint{
			{Addresses: []string{"10.2.0.1"}, Conditions: discovery.EndpointConditions{Ready: &ready}},
			{Addresses: []string{"10.2.0.2"}, Conditions: discovery.EndpointConditions{Ready: &ready}},
		},
	}
	endpointSliceIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{
		endpointSliceServiceIndex: endpointSliceServiceIndexFunc,
	})
	if err := endpointSliceIndexer.Add(es); err != nil {
		t.Fatalf("failed to add endpoint slice: %v", err)
	}

	lookup := lookupServiceIndex(
		[]cache.SharedIndexInformer{&fakeSharedIndexInformer{indexer: serviceIndexer}},
		&fakeSharedIndexInformer{indexer: endpointSliceIndexer},
	)

	// Default hostname for an opted-in service is name.namespace.
	results, _ := lookup([]string{"backend.default"})

	got := make(map[string]bool, len(results))
	for _, a := range results {
		got[a.String()] = true
	}
	if len(results) != 2 || !got["10.2.0.1"] || !got["10.2.0.2"] {
		t.Errorf("expected endpoint IPs [10.2.0.1 10.2.0.2], got %v", results)
	}
}
