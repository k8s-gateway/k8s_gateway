package gateway

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

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

	for _, testObj := range testInvalidAnnotationServices {
		found, _ := serviceHostnameIndexFunc(testObj)
		if len(found) != 0 {
			t.Errorf("Unexpected non-empty service hostnames %v for invalid annotation: %v", found, testObj.Annotations)
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
}

func TestGatewayIndexFunc(t *testing.T) {
gw := &gatewayapi_v1.Gateway{
ObjectMeta: metav1.ObjectMeta{
Name:      "test-gateway",
Namespace: "test-ns",
},
}

keys, err := gatewayIndexFunc(gw)
if err != nil {
t.Fatalf("gatewayIndexFunc failed: %v", err)
}

expected := "test-ns/test-gateway"
if len(keys) != 1 || keys[0] != expected {
t.Errorf("Expected key %s, got %v", expected, keys)
}

// Test with non-gateway object (should still work if it has metadata)
svc := &core.Service{
ObjectMeta: metav1.ObjectMeta{
Name:      "test-svc",
Namespace: "default",
},
}
keys2, err := gatewayIndexFunc(svc)
if err != nil {
t.Fatalf("gatewayIndexFunc with service failed: %v", err)
}
if len(keys2) != 1 || keys2[0] != "default/test-svc" {
t.Errorf("Expected key default/test-svc, got %v", keys2)
}
}

func TestHTTPRouteHostnameIndexFunc(t *testing.T) {
// Test with valid HTTPRoute
route := &gatewayapi_v1.HTTPRoute{
ObjectMeta: metav1.ObjectMeta{
Name: "test-route",
},
Spec: gatewayapi_v1.HTTPRouteSpec{
Hostnames: []gatewayapi_v1.Hostname{
"test.example.com",
"test2.example.com",
},
},
}

hostnames, err := httpRouteHostnameIndexFunc(route)
if err != nil {
t.Fatalf("httpRouteHostnameIndexFunc failed: %v", err)
}

if len(hostnames) != 2 {
t.Errorf("Expected 2 hostnames, got %d", len(hostnames))
}

// Test with ignored label
ignoredRoute := &gatewayapi_v1.HTTPRoute{
ObjectMeta: metav1.ObjectMeta{
Name: "ignored-route",
Labels: map[string]string{
ignoreLabelKey: "true",
},
},
Spec: gatewayapi_v1.HTTPRouteSpec{
Hostnames: []gatewayapi_v1.Hostname{"should-be-ignored.example.com"},
},
}

hostnames2, err := httpRouteHostnameIndexFunc(ignoredRoute)
if err != nil {
t.Fatalf("httpRouteHostnameIndexFunc with ignored route failed: %v", err)
}

if len(hostnames2) != 0 {
t.Errorf("Expected 0 hostnames for ignored route, got %d", len(hostnames2))
}

// Test with wrong type
wrongType := &core.Service{}
hostnames3, err := httpRouteHostnameIndexFunc(wrongType)
if err != nil {
t.Fatalf("httpRouteHostnameIndexFunc with wrong type failed: %v", err)
}
if len(hostnames3) != 0 {
t.Errorf("Expected 0 hostnames for wrong type, got %d", len(hostnames3))
}
}

func TestTLSRouteHostnameIndexFunc(t *testing.T) {
route := &gatewayapi_v1alpha2.TLSRoute{
ObjectMeta: metav1.ObjectMeta{
Name: "tls-route",
},
Spec: gatewayapi_v1alpha2.TLSRouteSpec{
Hostnames: []gatewayapi_v1alpha2.Hostname{
"tls.example.com",
},
},
}

hostnames, err := tlsRouteHostnameIndexFunc(route)
if err != nil {
t.Fatalf("tlsRouteHostnameIndexFunc failed: %v", err)
}

if len(hostnames) != 1 {
t.Errorf("Expected 1 hostname, got %d", len(hostnames))
}

if hostnames[0] != "tls.example.com" {
t.Errorf("Expected tls.example.com, got %s", hostnames[0])
}
}

func TestGRPCRouteHostnameIndexFunc(t *testing.T) {
route := &gatewayapi_v1.GRPCRoute{
ObjectMeta: metav1.ObjectMeta{
Name: "grpc-route",
},
Spec: gatewayapi_v1.GRPCRouteSpec{
Hostnames: []gatewayapi_v1.Hostname{
"grpc.example.com",
},
},
}

hostnames, err := grpcRouteHostnameIndexFunc(route)
if err != nil {
t.Fatalf("grpcRouteHostnameIndexFunc failed: %v", err)
}

if len(hostnames) != 1 {
t.Errorf("Expected 1 hostname, got %d", len(hostnames))
}
}

func TestIngressHostnameIndexFunc(t *testing.T) {
ingress := &networking.Ingress{
ObjectMeta: metav1.ObjectMeta{
Name: "test-ingress",
},
Spec: networking.IngressSpec{
Rules: []networking.IngressRule{
{Host: "host1.example.com"},
{Host: "host2.example.com"},
},
},
}

hostnames, err := ingressHostnameIndexFunc(ingress)
if err != nil {
t.Fatalf("ingressHostnameIndexFunc failed: %v", err)
}

if len(hostnames) != 2 {
t.Errorf("Expected 2 hostnames, got %d", len(hostnames))
}
}

func TestServiceHostnameIndexFunc(t *testing.T) {
// Test with hostname annotation
svc := &core.Service{
ObjectMeta: metav1.ObjectMeta{
Name:      "test-svc",
Namespace: "default",
Annotations: map[string]string{
hostnameAnnotationKey: "custom.example.com",
},
},
Spec: core.ServiceSpec{
Type: core.ServiceTypeLoadBalancer,
},
}

hostnames, err := serviceHostnameIndexFunc(svc)
if err != nil {
t.Fatalf("serviceHostnameIndexFunc failed: %v", err)
}

if len(hostnames) != 1 || hostnames[0] != "custom.example.com" {
t.Errorf("Expected custom.example.com, got %v", hostnames)
}

// Test with default hostname
svc2 := &core.Service{
ObjectMeta: metav1.ObjectMeta{
Name:      "default-svc",
Namespace: "ns1",
},
Spec: core.ServiceSpec{
Type: core.ServiceTypeLoadBalancer,
},
}

hostnames2, err := serviceHostnameIndexFunc(svc2)
if err != nil {
t.Fatalf("serviceHostnameIndexFunc with default failed: %v", err)
}

if len(hostnames2) != 1 || hostnames2[0] != "default-svc.ns1" {
t.Errorf("Expected default-svc.ns1, got %v", hostnames2)
}

// Test with non-LoadBalancer service
svc3 := &core.Service{
ObjectMeta: metav1.ObjectMeta{
Name:      "cluster-svc",
Namespace: "default",
},
Spec: core.ServiceSpec{
Type: core.ServiceTypeClusterIP,
},
}

hostnames3, err := serviceHostnameIndexFunc(svc3)
if err != nil {
t.Fatalf("serviceHostnameIndexFunc with ClusterIP failed: %v", err)
}

if len(hostnames3) != 0 {
t.Errorf("Expected 0 hostnames for ClusterIP service, got %d", len(hostnames3))
}
}

func TestDNSEndpointTargetIndexFunc(t *testing.T) {
ep := &externaldnsv1.DNSEndpoint{
ObjectMeta: metav1.ObjectMeta{
Name: "test-ep",
},
Spec: externaldnsv1.DNSEndpointSpec{
Endpoints: []*endpoint.Endpoint{
{DNSName: "ep1.example.com"},
{DNSName: "ep2.example.com"},
},
},
}

hostnames, err := dnsEndpointTargetIndexFunc(ep)
if err != nil {
t.Fatalf("dnsEndpointTargetIndexFunc failed: %v", err)
}

if len(hostnames) != 2 {
t.Errorf("Expected 2 hostnames, got %d", len(hostnames))
}
}

func TestSplitHostnameAnnotation(t *testing.T) {
tests := []struct {
input    string
expected []string
}{
{"host1.example.com", []string{"host1.example.com"}},
{"host1.example.com,host2.example.com", []string{"host1.example.com", "host2.example.com"}},
{"host1.example.com, host2.example.com", []string{"host1.example.com", "host2.example.com"}},
{"host1.example.com , host2.example.com , host3.example.com", []string{"host1.example.com", "host2.example.com", "host3.example.com"}},
}

for _, test := range tests {
result := splitHostnameAnnotation(test.input)
if len(result) != len(test.expected) {
t.Errorf("For input %s, expected %d results, got %d", test.input, len(test.expected), len(result))
continue
}
for i, expected := range test.expected {
if result[i] != expected {
t.Errorf("For input %s, expected result[%d]=%s, got %s", test.input, i, expected, result[i])
}
}
}
}

func TestCheckServiceAnnotations(t *testing.T) {
svc := &core.Service{
ObjectMeta: metav1.ObjectMeta{
Annotations: map[string]string{
hostnameAnnotationKey:            "primary.example.com",
externalDnsHostnameAnnotationKey: "fallback.example.com",
},
},
}

// Should return first annotation
value, exists := checkServiceAnnotations(svc, hostnameAnnotationKey, externalDnsHostnameAnnotationKey)
if !exists {
t.Error("Expected annotation to exist")
}
if value != "primary.example.com" {
t.Errorf("Expected primary.example.com, got %s", value)
}

// Test with only second annotation
svc2 := &core.Service{
ObjectMeta: metav1.ObjectMeta{
Annotations: map[string]string{
externalDnsHostnameAnnotationKey: "fallback.example.com",
},
},
}

value2, exists2 := checkServiceAnnotations(svc2, hostnameAnnotationKey, externalDnsHostnameAnnotationKey)
if !exists2 {
t.Error("Expected annotation to exist")
}
if value2 != "fallback.example.com" {
t.Errorf("Expected fallback.example.com, got %s", value2)
}

// Test with no annotations
svc3 := &core.Service{
ObjectMeta: metav1.ObjectMeta{},
}

_, exists3 := checkServiceAnnotations(svc3, hostnameAnnotationKey, externalDnsHostnameAnnotationKey)
if exists3 {
t.Error("Expected annotation to not exist")
}
}

func TestCheckDomainValid(t *testing.T) {
tests := []struct {
domain   string
expected bool
}{
{"example.com", true},
{"test.example.com", true},
{"*.example.com", true},
{"valid-hostname.example.com", true},
{"", false},
{"invalid_underscore.example.com", false},
{strings.Repeat("a", 256) + ".com", false}, // Too long
}

for _, test := range tests {
result := checkDomainValid(test.domain)
if result != test.expected {
t.Errorf("For domain %s, expected %v, got %v", test.domain, test.expected, result)
}
}
}

func TestCheckIgnoreLabel(t *testing.T) {
// Test with ignore label set to true
labels1 := map[string]string{
ignoreLabelKey: "true",
}
if !checkIgnoreLabel(labels1) {
t.Error("Expected true for ignore label = true")
}

// Test with ignore label set to false
labels2 := map[string]string{
ignoreLabelKey: "false",
}
if checkIgnoreLabel(labels2) {
t.Error("Expected false for ignore label = false")
}

// Test with no ignore label
labels3 := map[string]string{}
if checkIgnoreLabel(labels3) {
t.Error("Expected false for no ignore label")
}

// Test with nil labels
if checkIgnoreLabel(nil) {
t.Error("Expected false for nil labels")
}
}
