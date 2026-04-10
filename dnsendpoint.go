package gateway

import (
	"context"
	"net/netip"
	"slices"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	externaldnsv1 "sigs.k8s.io/external-dns/apis/v1alpha1"
)

const (
	externalDNSHostnameIndex = "externalDNSHostname"
)

var externaldnsCRDClient rest.Interface

func newExternalDNSRESTClient(config *rest.Config) (rest.Interface, error) {
	scheme := runtime.NewScheme()
	if err := externaldnsv1.AddToScheme(scheme); err != nil {
		return nil, err
	}
	gv := schema.GroupVersion{Group: "externaldns.k8s.io", Version: "v1alpha1"}
	cfgCopy := *config
	cfgCopy.GroupVersion = &gv
	cfgCopy.APIPath = "/apis"
	cfgCopy.NegotiatedSerializer = serializer.WithoutConversionCodecFactory{
		CodecFactory: serializer.NewCodecFactory(scheme),
	}
	return rest.RESTClientFor(&cfgCopy)
}

func initializeDNSEndpointController(ctx context.Context, ctrl *KubeController, gw *Gateway) {
	if !crdExists(apiextensionsClient, "dnsendpoints.externaldns.k8s.io") {
		return
	}
	if !slices.Contains(dereferenceStrings(gw.ConfiguredResources), "DNSEndpoint") {
		return
	}
	resource := gw.lookupResource("DNSEndpoint")
	if resource == nil {
		return
	}

	dnsEndpointController := cache.NewSharedIndexInformer(
		&cache.ListWatch{
			WatchFunc: dnsEndpointWatcher(ctx, ""),
			ListFunc:  dnsEndpointLister(ctx, ""),
		},
		&externaldnsv1.DNSEndpoint{},
		defaultResyncPeriod,
		cache.Indexers{externalDNSHostnameIndex: dnsEndpointTargetIndexFunc},
	)
	resource.lookup = lookupDNSEndpoint(dnsEndpointController)
	ctrl.controllers = append(ctrl.controllers, dnsEndpointController)
	log.Infof("DNSEndpoint controller initialized")
}

func dnsEndpointWatcher(ctx context.Context, ns string) func(metav1.ListOptions) (watch.Interface, error) {
	return func(opts metav1.ListOptions) (watch.Interface, error) {
		opts.Watch = true
		return externaldnsCRDClient.Get().
			Resource("dnsendpoints").
			Namespace(ns).
			VersionedParams(&opts, metav1.ParameterCodec).
			Watch(ctx)
	}
}

func dnsEndpointLister(ctx context.Context, ns string) func(metav1.ListOptions) (runtime.Object, error) {
	return func(opts metav1.ListOptions) (runtime.Object, error) {
		return externaldnsCRDClient.Get().
			Resource("dnsendpoints").
			Namespace(ns).
			VersionedParams(&opts, metav1.ParameterCodec).
			Do(ctx).
			Get()
	}
}

func dnsEndpointTargetIndexFunc(obj interface{}) ([]string, error) {
	dnsEndpoint, ok := obj.(*externaldnsv1.DNSEndpoint)
	if !ok {
		return []string{}, nil
	}

	if checkIgnoreLabel(dnsEndpoint.Labels) {
		log.Debugf("Ignoring dnsEndpoint %s due to %s label", dnsEndpoint.Name, ignoreLabelKey)
		return []string{}, nil
	}

	var hostnames []string
	for _, endpoint := range dnsEndpoint.Spec.Endpoints {
		log.Debugf("Adding index %s for DNSEndpoint %s", endpoint.DNSName, dnsEndpoint.Name)
		hostnames = append(hostnames, endpoint.DNSName)
	}
	return hostnames, nil
}

func lookupDNSEndpoint(ctrl cache.SharedIndexInformer) func([]string) (results []netip.Addr, raws []string) {
	return func(indexKeys []string) (result []netip.Addr, raw []string) {
		var objs []interface{}
		for _, key := range indexKeys {
			obj, _ := ctrl.GetIndexer().ByIndex(externalDNSHostnameIndex, strings.ToLower(key))
			objs = append(objs, obj...)
		}
		log.Debugf("Found %d matching DNSEndpoint objects", len(objs))
		for _, obj := range objs {
			dnsEndpoint, _ := obj.(*externaldnsv1.DNSEndpoint)

			for _, endpoint := range dnsEndpoint.Spec.Endpoints {
				for _, target := range endpoint.Targets {
					if endpoint.RecordType == "A" || endpoint.RecordType == "AAAA" {
						addr, err := netip.ParseAddr(target)
						if err != nil {
							continue
						}
						result = append(result, addr)
					}
					if endpoint.RecordType == "TXT" {
						raw = append(raw, target)
					}
				}
			}
		}
		return result, raw
	}
}
