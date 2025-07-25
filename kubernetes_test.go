package gateway

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/coredns/coredns/plugin/test"
	"github.com/miekg/dns"
	core "k8s.io/api/core/v1"
	networking "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	fakeRest "k8s.io/client-go/rest/fake"
	"k8s.io/client-go/tools/cache"
	externaldnsv1 "sigs.k8s.io/external-dns/apis/v1alpha1"
	"sigs.k8s.io/external-dns/endpoint"
	gatewayapi_v1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayapi_v1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
	gatewayClient "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned"
	gwFake "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned/fake"
)

// taken from external-dns/source/crd_test.go
func addKnownTypes(scheme *runtime.Scheme, groupVersion schema.GroupVersion) {
	scheme.AddKnownTypes(groupVersion,
		&externaldnsv1.DNSEndpoint{},
		&externaldnsv1.DNSEndpointList{},
	)
	metav1.AddToGroupVersion(scheme, groupVersion)
}

func defaultHeader() http.Header {
	header := http.Header{}
	header.Set("Content-Type", runtime.ContentTypeJSON)
	return header
}

func objBody(codec runtime.Encoder, obj runtime.Object) io.ReadCloser {
	return io.NopCloser(bytes.NewReader([]byte(runtime.EncodeOrDie(codec, obj))))
}

func fakeRESTClient(endpoints []*endpoint.Endpoint, apiVersion string, kind string, namespace string, name string, annotations map[string]string, labels map[string]string, _ *testing.T) rest.Interface {
	groupVersion, _ := schema.ParseGroupVersion(apiVersion)
	scheme := runtime.NewScheme()
	addKnownTypes(scheme, groupVersion)

	// Create your DNSEndpoint object.
	dnsEndpoint := &externaldnsv1.DNSEndpoint{
		TypeMeta: metav1.TypeMeta{
			APIVersion: apiVersion,
			Kind:       kind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Annotations: annotations,
			Labels:      labels,
			Generation:  1,
		},
		Spec: externaldnsv1.DNSEndpointSpec{
			Endpoints: endpoints,
		},
	}
	var dnsEndpointList externaldnsv1.DNSEndpointList

	codecFactory := serializer.WithoutConversionCodecFactory{
		CodecFactory: serializer.NewCodecFactory(scheme),
	}

	client := &fakeRest.RESTClient{
		GroupVersion:         groupVersion,
		VersionedAPIPath:     "/apis/" + apiVersion,
		NegotiatedSerializer: codecFactory,
		Client: fakeRest.CreateHTTPClient(func(req *http.Request) (*http.Response, error) {
			codec := codecFactory.LegacyCodec(groupVersion)
			switch p, m := req.URL.Path, req.Method; {

			case p == "/apis/"+apiVersion+"/"+strings.ToLower(kind)+"s" && m == http.MethodGet,
				p == "/apis/"+apiVersion+"/namespaces/"+namespace+"/"+strings.ToLower(kind)+"s" && m == http.MethodGet,
				(strings.HasPrefix(p, "/apis/"+apiVersion+"/namespaces/") && strings.HasSuffix(p, strings.ToLower(kind)+"s") && m == http.MethodGet):
				dnsEndpointList.Items = []externaldnsv1.DNSEndpoint{*dnsEndpoint}
				return &http.Response{StatusCode: http.StatusOK, Header: defaultHeader(), Body: objBody(codec, &dnsEndpointList)}, nil

			case p == "/apis/"+apiVersion+"/namespaces/"+namespace+"/"+strings.ToLower(kind)+"s" && m == http.MethodPost:
				return &http.Response{
					StatusCode: http.StatusCreated,
					Header:     defaultHeader(),
					Body:       objBody(codec, dnsEndpoint),
				}, nil

			case p == "/apis/"+apiVersion+"/namespaces/"+namespace+"/"+strings.ToLower(kind)+"s/"+name+"/status" && m == http.MethodPut:
				return &http.Response{StatusCode: http.StatusOK, Header: defaultHeader(), Body: objBody(codec, dnsEndpoint)}, nil

			default:
				return nil, fmt.Errorf("unexpected request: %#v\n%#v", req.URL, req)
			}
		}),
	}

	return client
}

func TestController(t *testing.T) {
	client := fake.NewClientset()
	gwClient := gwFake.NewClientset()
	ctrl := &KubeController{
		client:    client,
		gwClient:  gwClient,
		hasSynced: true,
	}
	addServices(client)
	addIngresses(client)
	addGateways(gwClient)
	addHTTPRoutes(gwClient)
	addTLSRoutes(gwClient)
	addGRPCRoutes(gwClient)
	addDNSEndpoints(fakeRESTClient(testDNSEndpoints["dual.example.com"].Spec.Endpoints, "externaldns.k8s.io/v1alpha1", "DNSEndpoint", "ns1", "ep1", nil, nil, t))

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

	for index, testObj := range testHTTPRoutes {
		found, _ := httpRouteHostnameIndexFunc(testObj)
		if checkIgnoreLabel(testObj.Labels) {
			if len(found) != 0 {
				t.Errorf("Ignored HTTPRoute key %s should not be found in index, but found: %v", index, found)
			}
			continue
		}
		if !isFound(index, found) {
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
		if !isFound(index, found) {
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
		if !isFound(index, found) {
			t.Errorf("GRPCRoute key %s not found in index: %v", index, found)
		}
	}

	for index, testObj := range testGateways {
		found, _ := gatewayIndexFunc(testObj)
		if !isFound(index, found) {
			t.Errorf("Gateway key %s not found in index: %v", index, found)
		}
	}

	for index, testObj := range testDNSEndpoints {
		found, _ := dnsEndpointTargetIndexFunc(testObj)
		if checkIgnoreLabel(testObj.Labels) {
			if len(found) != 0 {
				t.Errorf("Ignored DNSEndpoint key %s should not be found in index, but found: %v", index, found)
			}
			continue
		}
		if !isFound(index, found) {
			t.Errorf("DNSEndpoint key %s not found in index: %v", index, found)
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

func addDNSEndpoints(client rest.Interface) {
	ctx := context.TODO()
	for _, ep := range testDNSEndpoints {
		_, err := client.Post().Resource("dnsendpoints").Namespace("ns1").Body(ep).Do(ctx).Get()
		if err != nil {
			log.Warningf("Failed to Create a DNSEndpoint Object :%s", err)
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
	"annotation-external-dns": {
		ObjectMeta: metav1.ObjectMeta{
			Name:      "svc3",
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
			Name:      "svc3",
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

var testDNSEndpoints = map[string]*externaldnsv1.DNSEndpoint{
	"dual.example.com": &externaldnsv1.DNSEndpoint{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ep1",
			Namespace: "ns1",
		},
		Spec: externaldnsv1.DNSEndpointSpec{
			Endpoints: []*endpoint.Endpoint{
				{
					DNSName:    "dual.example.com",
					RecordType: "A",
					Targets:    []string{"192.0.2.200"},
				},
				{
					DNSName:    "dual.example.com",
					RecordType: "AAAA",
					Targets:    []string{"2001:db8::1"},
				},
			},
		},
	},
	"ignored.example.com": &externaldnsv1.DNSEndpoint{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ignored-ep",
			Namespace: "ns1",
			Labels: map[string]string{
				ignoreLabelKey: "true",
			},
		},
		Spec: externaldnsv1.DNSEndpointSpec{
			Endpoints: []*endpoint.Endpoint{
				{
					DNSName:    "ignored.example.com",
					RecordType: "A",
					Targets:    []string{"192.0.2.99"},
				},
			},
		},
	},
	"cname.example.com": &externaldnsv1.DNSEndpoint{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cname-ep",
			Namespace: "ns1",
		},
		Spec: externaldnsv1.DNSEndpointSpec{
			Endpoints: []*endpoint.Endpoint{
				{
					DNSName:    "cname.example.com",
					RecordType: "CNAME",
					Targets:    []string{"target.example.com"},
				},
			},
		},
	},
	"chain.example.com": &externaldnsv1.DNSEndpoint{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "chain-ep",
			Namespace: "ns1",
		},
		Spec: externaldnsv1.DNSEndpointSpec{
			Endpoints: []*endpoint.Endpoint{
				{
					DNSName:    "chain.example.com",
					RecordType: "CNAME",
					Targets:    []string{"step2.example.com"},
				},
				{
					DNSName:    "step2.example.com",
					RecordType: "CNAME",
					Targets:    []string{"final.example.com"},
				},
				{
					DNSName:    "final.example.com",
					RecordType: "A",
					Targets:    []string{"10.0.1.100"},
				},
			},
		},
	},
	"service.example.com": &externaldnsv1.DNSEndpoint{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mixed-ep",
			Namespace: "ns1",
		},
		Spec: externaldnsv1.DNSEndpointSpec{
			Endpoints: []*endpoint.Endpoint{
				{
					DNSName:    "service.example.com",
					RecordType: "A",
					Targets:    []string{"10.0.0.1"},
				},
				{
					DNSName:    "alias.example.com",
					RecordType: "CNAME",
					Targets:    []string{"service.example.com"},
				},
				{
					DNSName:    "text.example.com",
					RecordType: "TXT",
					Targets:    []string{"v=spf1 include:_spf.google.com ~all"},
				},
			},
		},
	},
}

// TestDNSEndpointCNAMELookup tests CNAME record lookups from DNSEndpoint resources
func TestDNSEndpointCNAMELookup(t *testing.T) {
	tests := []struct {
		name           string
		endpoint       *externaldnsv1.DNSEndpoint
		indexKeys      []string
		expectedAddrs  int
		expectedCNAMEs int
		expectedRaws   int
	}{
		{
			name:           "CNAME record lookup",
			endpoint:       testDNSEndpoints["cname.example.com"],
			indexKeys:      []string{"cname.example.com"},
			expectedAddrs:  0,
			expectedCNAMEs: 1,
			expectedRaws:   0,
		},
		{
			name:           "Mixed record types",
			endpoint:       testDNSEndpoints["service.example.com"],
			indexKeys:      []string{"alias.example.com"},
			expectedAddrs:  0,
			expectedCNAMEs: 1,
			expectedRaws:   0,
		},
		{
			name:           "A record from mixed endpoint",
			endpoint:       testDNSEndpoints["service.example.com"],
			indexKeys:      []string{"service.example.com"},
			expectedAddrs:  1,
			expectedCNAMEs: 0,
			expectedRaws:   0,
		},
		{
			name:           "TXT record from mixed endpoint",
			endpoint:       testDNSEndpoints["service.example.com"],
			indexKeys:      []string{"text.example.com"},
			expectedAddrs:  0,
			expectedCNAMEs: 0,
			expectedRaws:   1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Mock controller with test endpoint
			ctrl := createMockDNSEndpointController([]*externaldnsv1.DNSEndpoint{test.endpoint})
			lookupFunc := lookupDNSEndpointWithCNAME(ctrl)

			addrs, raws, cnames := lookupFunc(test.indexKeys)

			if len(addrs) != test.expectedAddrs {
				t.Errorf("Expected %d addresses, got %d", test.expectedAddrs, len(addrs))
			}

			if len(cnames) != test.expectedCNAMEs {
				t.Errorf("Expected %d CNAME records, got %d", test.expectedCNAMEs, len(cnames))
			}

			if len(raws) != test.expectedRaws {
				t.Errorf("Expected %d raw records, got %d", test.expectedRaws, len(raws))
			}

			// Verify CNAME target
			if test.expectedCNAMEs > 0 && len(cnames) > 0 {
				expectedTarget := ""
				for _, ep := range test.endpoint.Spec.Endpoints {
					if ep.RecordType == "CNAME" && strings.EqualFold(ep.DNSName, test.indexKeys[0]) {
						expectedTarget = ep.Targets[0]
						break
					}
				}
				if cnames[0] != expectedTarget {
					t.Errorf("Expected CNAME target '%s', got '%s'", expectedTarget, cnames[0])
				}
			}
		})
	}
}

// Helper function to create mock DNSEndpoint controller for testing
func createMockDNSEndpointController(endpoints []*externaldnsv1.DNSEndpoint) cache.SharedIndexInformer {
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{
		externalDNSHostnameIndex: dnsEndpointTargetIndexFunc,
	})

	// Add endpoints to indexer
	for _, ep := range endpoints {
		indexer.Add(ep)
	}

	return &mockDNSEndpointInformer{indexer: indexer}
}

// Mock informer implementation for testing
type mockDNSEndpointInformer struct {
	indexer cache.Indexer
}

func (m *mockDNSEndpointInformer) GetIndexer() cache.Indexer {
	return m.indexer
}

// Stub implementations for other required methods
func (m *mockDNSEndpointInformer) AddEventHandler(handler cache.ResourceEventHandler) (cache.ResourceEventHandlerRegistration, error) {
	return nil, nil
}
func (m *mockDNSEndpointInformer) AddEventHandlerWithResyncPeriod(handler cache.ResourceEventHandler, resyncPeriod time.Duration) (cache.ResourceEventHandlerRegistration, error) {
	return nil, nil
}
func (m *mockDNSEndpointInformer) AddEventHandlerWithOptions(handler cache.ResourceEventHandler, options cache.HandlerOptions) (cache.ResourceEventHandlerRegistration, error) {
	return nil, nil
}
func (m *mockDNSEndpointInformer) RemoveEventHandler(handle cache.ResourceEventHandlerRegistration) error {
	return nil
}
func (m *mockDNSEndpointInformer) GetStore() cache.Store           { return m.indexer }
func (m *mockDNSEndpointInformer) GetController() cache.Controller { return nil }
func (m *mockDNSEndpointInformer) Run(stopCh <-chan struct{})      {}
func (m *mockDNSEndpointInformer) HasSynced() bool                 { return true }
func (m *mockDNSEndpointInformer) LastSyncResourceVersion() string { return "" }
func (m *mockDNSEndpointInformer) SetWatchErrorHandler(handler cache.WatchErrorHandler) error {
	return nil
}
func (m *mockDNSEndpointInformer) SetTransform(f cache.TransformFunc) error { return nil }
func (m *mockDNSEndpointInformer) IsStopped() bool                          { return false }
func (m *mockDNSEndpointInformer) RunWithContext(ctx context.Context)       {}
func (m *mockDNSEndpointInformer) SetWatchErrorHandlerWithContext(handler cache.WatchErrorHandlerWithContext) error {
	return nil
}
func (m *mockDNSEndpointInformer) AddIndexers(indexers cache.Indexers) error { return nil }

// Enhanced lookupDNSEndpoint that supports CNAME records for testing
func lookupDNSEndpointWithCNAME(ctrl cache.SharedIndexInformer) func([]string) (results []netip.Addr, raws []string, cnames []string) {
	return func(indexKeys []string) (result []netip.Addr, raw []string, cname []string) {
		var objs []interface{}
		for _, key := range indexKeys {
			obj, _ := ctrl.GetIndexer().ByIndex(externalDNSHostnameIndex, strings.ToLower(key))
			objs = append(objs, obj...)
		}

		for _, obj := range objs {
			dnsEndpoint, ok := obj.(*externaldnsv1.DNSEndpoint)
			if !ok {
				continue
			}

			for _, endpoint := range dnsEndpoint.Spec.Endpoints {
				// Only process endpoints that match one of our index keys
				matchesKey := false
				for _, key := range indexKeys {
					if strings.EqualFold(endpoint.DNSName, key) {
						matchesKey = true
						break
					}
				}
				if !matchesKey {
					continue
				}

				for _, target := range endpoint.Targets {
					switch endpoint.RecordType {
					case "A", "AAAA":
						if addr, err := netip.ParseAddr(target); err == nil {
							result = append(result, addr)
						}
					case "TXT":
						raw = append(raw, target)
					case "CNAME":
						cname = append(cname, target)
					}
				}
			}
		}
		return result, raw, cname
	}
}
