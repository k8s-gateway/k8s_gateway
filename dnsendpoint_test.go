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
