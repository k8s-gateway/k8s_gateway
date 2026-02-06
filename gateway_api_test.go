package gateway

import (
	"context"
	"net/netip"
	"strings"
	"testing"

	"github.com/coredns/coredns/plugin/pkg/dnstest"
	"github.com/coredns/coredns/plugin/test"

	"github.com/miekg/dns"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	gatewayapi_v1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayapi_v1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
	gatewayClient "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned"
	gwFake "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned/fake"
)

func TestGatewayAPILookup(t *testing.T) {
	ctrl := &KubeController{hasSynced: true}

	gw := newGateway()
	gw.Zones = []string{"example.com."}
	gw.Next = test.NextHandler(dns.RcodeSuccess, nil)
	gw.ExternalAddrFunc = gw.SelfAddress
	gw.Controller = ctrl
	real := []string{"HTTPRoute", "TLSRoute", "GRPCRoute"}
	fake := []string{"Pod", "Gateway"}

	for _, resource := range real {
		if found := gw.lookupResource(resource); found == nil {
			t.Errorf("Could not lookup supported Gateway API resource %s", resource)
		}
	}

	for _, resource := range fake {
		if found := gw.lookupResource(resource); found != nil {
			t.Errorf("Located unsupported resource %s", resource)
		}
	}
}

func TestGatewayAPIPlugin(t *testing.T) {
	ctrl := &KubeController{hasSynced: true}

	gw := newGateway()
	gw.Zones = []string{"example.com."}
	gw.Next = test.NextHandler(dns.RcodeSuccess, nil)
	gw.ExternalAddrFunc = gw.SelfAddress
	gw.Controller = ctrl
	setupGatewayAPILookupFuncs(gw)

	ctx := context.TODO()
	for i, tc := range gatewayAPITests {
		r := tc.Msg()
		w := dnstest.NewRecorder(&test.ResponseWriter{})

		_, err := gw.ServeDNS(ctx, w, r)
		if err != tc.Error {
			t.Errorf("Test %d expected no error, got %v", i, err)
			return
		}
		if tc.Error != nil {
			continue
		}

		resp := w.Msg

		if resp == nil {
			t.Fatalf("Test %d, got nil message and no error for %q", i, r.Question[0].Name)
		}
		if err = test.SortAndCheck(resp, tc); err != nil {
			t.Errorf("Test %d failed with error: %v", i, err)
		}
	}
}

func TestGatewayAPIController(t *testing.T) {
	client := fake.NewClientset()
	gwClient := gwFake.NewClientset()
	ctrl := &KubeController{
		client:    client,
		gwClient:  gwClient,
		hasSynced: true,
	}
	addGateways(gwClient)
	addHTTPRoutes(gwClient)
	addTLSRoutes(gwClient)
	addGRPCRoutes(gwClient)

	gw := newGateway()
	gw.Zones = []string{"example.com."}
	gw.Next = test.NextHandler(dns.RcodeSuccess, nil)
	gw.Controller = ctrl

	for index, testObj := range testHTTPRoutes {
		found, _ := httpRouteHostnameIndexFunc(testObj)
		if checkIgnoreLabel(testObj.Labels) {
			if len(found) != 0 {
				t.Errorf("Ignored HTTPRoute key %s should not be found in index, but found: %v", index, found)
			}
			continue
		}
		if !isFoundInIndex(index, found) {
			t.Errorf("HTTPRoute key %s not found in index: %v", index, found)
		}
	}

	for index, testObj := range testTLSRoutes {
		found, _ := tlsRouteHostnameIndexFunc(testObj)
		if checkIgnoreLabel(testObj.Labels) {
			if len(found) != 0 {
				t.Errorf("Ignored TLSRoute key %s should not be found in index, but found: %v", index, found)
			}
			continue
		}
		if !isFoundInIndex(index, found) {
			t.Errorf("TLSRoute key %s not found in index: %v", index, found)
		}
	}

	for index, testObj := range testGRPCRoutes {
		found, _ := grpcRouteHostnameIndexFunc(testObj)
		if checkIgnoreLabel(testObj.Labels) {
			if len(found) != 0 {
				t.Errorf("Ignored GRPCRoute key %s should not be found in index, but found: %v", index, found)
			}
			continue
		}
		if !isFoundInIndex(index, found) {
			t.Errorf("GRPCRoute key %s not found in index: %v", index, found)
		}
	}

	for index, testObj := range testGateways {
		found, _ := gatewayIndexFunc(testObj)
		if !isFoundInIndex(index, found) {
			t.Errorf("Gateway key %s not found in index: %v", index, found)
		}
	}
}

func isFoundInIndex(s string, ss []string) bool {
	for _, str := range ss {
		if str == s {
			return true
		}
	}
	return false
}

func addGateways(client gatewayClient.Interface) {
	ctx := context.TODO()
	for _, gw := range testGateways {
		_, err := client.GatewayV1().Gateways("ns1").Create(ctx, gw, metav1.CreateOptions{})
		if err != nil {
			log.Warningf("Failed to Create a Gateway Object :%s", err)
		}
	}
}

func addHTTPRoutes(client gatewayClient.Interface) {
	ctx := context.TODO()
	for _, r := range testHTTPRoutes {
		_, err := client.GatewayV1().HTTPRoutes("ns1").Create(ctx, r, metav1.CreateOptions{})
		if err != nil {
			log.Warningf("Failed to Create a HTTPRoute Object :%s", err)
		}
	}
}

func addTLSRoutes(client gatewayClient.Interface) {
	ctx := context.TODO()
	for _, r := range testTLSRoutes {
		_, err := client.GatewayV1alpha2().TLSRoutes("ns1").Create(ctx, r, metav1.CreateOptions{})
		if err != nil {
			log.Warningf("Failed to Create a TLSRoutes Object :%s", err)
		}
	}
}

func addGRPCRoutes(client gatewayClient.Interface) {
	ctx := context.TODO()
	for _, r := range testGRPCRoutes {
		_, err := client.GatewayV1().GRPCRoutes("ns1").Create(ctx, r, metav1.CreateOptions{})
		if err != nil {
			log.Warningf("Failed to Create a GRPC Object :%s", err)
		}
	}

	for _, r2 := range testGRPCRoutesLegacy {
		_, err := client.GatewayV1alpha2().GRPCRoutes("ns1").Create(ctx, r2, metav1.CreateOptions{})
		if err != nil {
			log.Warningf("Failed to Create a GRPC Object :%s", err)
		}
	}
}

var gatewayAPITests = []test.Case{
	// basic gateway API lookup | Test 0
	{
		Qname: "domain.gw.example.com.", Qtype: dns.TypeA, Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.A("domain.gw.example.com.  60  IN  A   192.0.2.1"),
		},
	},
	// gateway API lookup priority over Ingress | Test 1
	{
		Qname: "shadow.example.com.", Qtype: dns.TypeA, Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.A("shadow.example.com. 60  IN  A   192.0.2.4"),
		},
	},
}

var testGatewayAPIRouteIndexes = map[string][]netip.Addr{
	"domain.gw.example.com": {netip.MustParseAddr("192.0.2.1")},
	"shadow.example.com":    {netip.MustParseAddr("192.0.2.4")},
}

func testGatewayAPIRouteLookup(keys []string) (results []netip.Addr, raws []string) {
	for _, key := range keys {
		results = append(results, testGatewayAPIRouteIndexes[strings.ToLower(key)]...)
	}
	return results, raws
}

func setupGatewayAPILookupFuncs(gw *Gateway) {
	if resource := gw.lookupResource("HTTPRoute"); resource != nil {
		resource.lookup = testGatewayAPIRouteLookup
	}
	if resource := gw.lookupResource("TLSRoute"); resource != nil {
		resource.lookup = testGatewayAPIRouteLookup
	}
	if resource := gw.lookupResource("GRPCRoute"); resource != nil {
		resource.lookup = testGatewayAPIRouteLookup
	}
}

var testGateways = map[string]*gatewayapi_v1.Gateway{
	"ns1/gw-1": {
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gw-1",
			Namespace: "ns1",
		},
		Spec: gatewayapi_v1.GatewaySpec{},
		Status: gatewayapi_v1.GatewayStatus{
			Addresses: []gatewayapi_v1.GatewayStatusAddress{
				{
					Value: "192.0.2.100",
				},
			},
		},
	},
	"ns1/gw-2": {
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gw-2",
			Namespace: "ns1",
		},
	},
}

var testHTTPRoutes = map[string]*gatewayapi_v1.HTTPRoute{
	"route-1.gw-1.example.com": {
		ObjectMeta: metav1.ObjectMeta{
			Name:      "route-1",
			Namespace: "ns1",
		},
		Spec: gatewayapi_v1.HTTPRouteSpec{
			//ParentRefs: []gatewayapi_v1.ParentRef{},
			Hostnames: []gatewayapi_v1.Hostname{"route-1.gw-1.example.com"},
		},
	},
	"ignored-route.gw-1.example.com": {
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ignored-route",
			Namespace: "ns1",
			Labels: map[string]string{
				ignoreLabelKey: "true",
			},
		},
		Spec: gatewayapi_v1.HTTPRouteSpec{
			Hostnames: []gatewayapi_v1.Hostname{"ignored-route.gw-1.example.com"},
		},
	},
}

var testTLSRoutes = map[string]*gatewayapi_v1alpha2.TLSRoute{
	"route-1.gw-1.example.com": {
		ObjectMeta: metav1.ObjectMeta{
			Name:      "route-1",
			Namespace: "ns1",
		},
		Spec: gatewayapi_v1alpha2.TLSRouteSpec{
			//ParentRefs: []gatewayapi_v1.ParentRef{},
			Hostnames: []gatewayapi_v1alpha2.Hostname{
				"route-1.gw-1.example.com",
			},
		},
	},
	"ignored-tls-route.gw-1.example.com": {
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ignored-tls-route",
			Namespace: "ns1",
			Labels: map[string]string{
				ignoreLabelKey: "true",
			},
		},
		Spec: gatewayapi_v1alpha2.TLSRouteSpec{
			Hostnames: []gatewayapi_v1alpha2.Hostname{
				"ignored-tls-route.gw-1.example.com",
			},
		},
	},
}

var testGRPCRoutes = map[string]*gatewayapi_v1.GRPCRoute{
	"route-1.gw-1.example.com": {
		ObjectMeta: metav1.ObjectMeta{
			Name:      "route-1",
			Namespace: "ns1",
		},
		Spec: gatewayapi_v1.GRPCRouteSpec{
			//ParentRefs: []gatewayapi_v1.ParentRef{},
			Hostnames: []gatewayapi_v1.Hostname{"route-1.gw-1.example.com"},
		},
	},
	"ignored-grpc-route.gw-1.example.com": {
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ignored-grpc-route",
			Namespace: "ns1",
			Labels: map[string]string{
				ignoreLabelKey: "true",
			},
		},
		Spec: gatewayapi_v1.GRPCRouteSpec{
			Hostnames: []gatewayapi_v1.Hostname{"ignored-grpc-route.gw-1.example.com"},
		},
	},
}

var testGRPCRoutesLegacy = map[string]*gatewayapi_v1alpha2.GRPCRoute{
	"route-1.gw-1.example.com": {
		ObjectMeta: metav1.ObjectMeta{
			Name:      "route-1",
			Namespace: "ns1",
		},
		Spec: gatewayapi_v1.GRPCRouteSpec{
			//ParentRefs: []gatewayapi_v1.ParentRef{},
			Hostnames: []gatewayapi_v1alpha2.Hostname{"route-1.gw-1.example.com"},
		},
	},
}
