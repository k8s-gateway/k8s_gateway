package gateway

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/rest"
	fakeRest "k8s.io/client-go/rest/fake"
	"k8s.io/client-go/tools/cache"
	externaldnsv1 "sigs.k8s.io/external-dns/apis/v1alpha1"
	"sigs.k8s.io/external-dns/endpoint"
)

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

func addDNSEndpoints(client rest.Interface) {
	ctx := context.TODO()
	for _, ep := range testDNSEndpoints {
		_, err := client.Post().Resource("dnsendpoints").Namespace("ns1").Body(ep).Do(ctx).Get()
		if err != nil {
			log.Warningf("Failed to Create a DNSEndpoint Object :%s", err)
		}
	}
}

func TestDNSEndpointController(t *testing.T) {
	addDNSEndpoints(fakeRESTClient(testDNSEndpoints["dual.example.com"].Spec.Endpoints, "externaldns.k8s.io/v1alpha1", "DNSEndpoint", "ns1", "ep1", nil, nil, t))

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

func TestDNSEndpointTargetIndexFunc_BadInput(t *testing.T) {
	found, err := dnsEndpointTargetIndexFunc("not-a-dnsendpoint")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("expected empty result for non-DNSEndpoint input, got: %v", found)
	}
}

func newDNSEndpointIndexer() cache.Indexer {
	return cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{
		externalDNSHostnameIndex: dnsEndpointTargetIndexFunc,
	})
}

func TestLookupDNSEndpoint(t *testing.T) {
	fakeIndexer := newDNSEndpointIndexer()
	ep := &externaldnsv1.DNSEndpoint{
		ObjectMeta: metav1.ObjectMeta{Name: "ep1", Namespace: "ns1"},
		Spec: externaldnsv1.DNSEndpointSpec{
			Endpoints: []*endpoint.Endpoint{
				// valid A record
				{DNSName: "svc.example.com", RecordType: "A", Targets: []string{"192.0.2.1"}},
				// valid AAAA record
				{DNSName: "svc.example.com", RecordType: "AAAA", Targets: []string{"2001:db8::1"}},
				// TXT record — goes into raw, not result
				{DNSName: "svc.example.com", RecordType: "TXT", Targets: []string{"heritage=external-dns"}},
			},
		},
	}
	if err := fakeIndexer.Add(ep); err != nil {
		t.Fatalf("failed to add DNSEndpoint to indexer: %v", err)
	}

	lookup := lookupDNSEndpoint(&fakeSharedIndexInformer{indexer: fakeIndexer})
	result, raw := lookup([]string{"svc.example.com"})

	if len(result) != 2 {
		t.Errorf("expected 2 IP results (1 A + 1 AAAA), got %d: %v", len(result), result)
	}
	if len(raw) != 1 || raw[0] != "heritage=external-dns" {
		t.Errorf("expected 1 TXT result %q, got %v", "heritage=external-dns", raw)
	}
}

func TestLookupDNSEndpoint_InvalidIP(t *testing.T) {
	fakeIndexer := newDNSEndpointIndexer()
	ep := &externaldnsv1.DNSEndpoint{
		ObjectMeta: metav1.ObjectMeta{Name: "ep2", Namespace: "ns1"},
		Spec: externaldnsv1.DNSEndpointSpec{
			Endpoints: []*endpoint.Endpoint{
				{DNSName: "bad.example.com", RecordType: "A", Targets: []string{"not-an-ip", "192.0.2.5"}},
			},
		},
	}
	if err := fakeIndexer.Add(ep); err != nil {
		t.Fatalf("failed to add DNSEndpoint to indexer: %v", err)
	}

	lookup := lookupDNSEndpoint(&fakeSharedIndexInformer{indexer: fakeIndexer})
	result, _ := lookup([]string{"bad.example.com"})

	if len(result) != 1 {
		t.Errorf("expected 1 valid IP (invalid one skipped), got %d: %v", len(result), result)
	}
	if result[0].String() != "192.0.2.5" {
		t.Errorf("expected 192.0.2.5, got %s", result[0])
	}
}

func TestLookupDNSEndpoint_NoMatch(t *testing.T) {
	fakeIndexer := newDNSEndpointIndexer()
	lookup := lookupDNSEndpoint(&fakeSharedIndexInformer{indexer: fakeIndexer})
	result, raw := lookup([]string{"unknown.example.com"})

	if len(result) != 0 {
		t.Errorf("expected no IP results, got: %v", result)
	}
	if len(raw) != 0 {
		t.Errorf("expected no raw results, got: %v", raw)
	}
}

func TestDNSEndpointLister(t *testing.T) {
	ep := testDNSEndpoints["dual.example.com"]
	client := fakeRESTClient(ep.Spec.Endpoints, "externaldns.k8s.io/v1alpha1", "DNSEndpoint", "ns1", "ep1", nil, nil, t)

	old := externaldnsCRDClient
	externaldnsCRDClient = client
	defer func() { externaldnsCRDClient = old }()

	lister := dnsEndpointLister(context.TODO(), "ns1")
	obj, err := lister(metav1.ListOptions{})
	if err != nil {
		t.Fatalf("dnsEndpointLister returned error: %v", err)
	}
	list, ok := obj.(*externaldnsv1.DNSEndpointList)
	if !ok {
		t.Fatalf("expected *externaldnsv1.DNSEndpointList, got %T", obj)
	}
	if len(list.Items) != 1 {
		t.Errorf("expected 1 item, got %d", len(list.Items))
	}
	if list.Items[0].Name != "ep1" {
		t.Errorf("expected item name %q, got %q", "ep1", list.Items[0].Name)
	}
}
