package gateway

import (
	"net/netip"
	"runtime/debug"
	"sort"
	"strings"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/transfer"
	"github.com/coredns/coredns/request"
	"github.com/miekg/dns"
	core "k8s.io/api/core/v1"
	networking "k8s.io/api/networking/v1"
	"k8s.io/client-go/tools/cache"
	externaldnsv1 "sigs.k8s.io/external-dns/apis/v1alpha1"
	gatewayapi_v1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayapi_v1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

// Transfer implements the transfer.Transfer interface for zone transfers (AXFR).
func (gw *Gateway) Transfer(zone string, serial uint32) (<-chan []dns.RR, error) {
	// Check if zone matches
	match := plugin.Zones(gw.Zones).Matches(zone)
	if match == "" {
		return nil, transfer.ErrNotAuthoritative
	}

	// Create a state for SOA generation
	state := request.Request{Zone: zone}
	soa := gw.soa(state)

	// Check SOA serial for IXFR fallback
	if serial != 0 && soa.Serial == serial {
		ch := make(chan []dns.RR, 1)
		ch <- []dns.RR{soa}
		close(ch)
		return ch, nil
	}

	ch := make(chan []dns.RR)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Errorf("Panic in zone transfer for zone %s: %v\nStack trace:\n%s", zone, r, debug.Stack())
			}
			close(ch)
		}()

		// Send initial SOA
		ch <- []dns.RR{soa}

		// Send NS records
		nsRecords := gw.nameservers(state)
		for _, ns := range nsRecords {
			ch <- []dns.RR{ns}
		}

		// Send A/AAAA records for nameservers
		nsAddrs := gw.ExternalAddrFunc(state)
		if len(nsAddrs) > 0 {
			ch <- nsAddrs
		}

		// Transfer all resources
		gw.transferResources(ch, zone)

		// Send final SOA
		ch <- []dns.RR{soa}
	}()

	return ch, nil
}

// transferResources iterates through all resources and sends their DNS records
func (gw *Gateway) transferResources(ch chan []dns.RR, zone string) {
	if !gw.Controller.HasSynced() {
		log.Warningf("Controller not synced, skipping zone transfer")
		return
	}

	// Collect all records from all resources
	records := make(map[string][]dns.RR)

	for _, resource := range gw.Resources {
		switch resource.name {
		case "Ingress":
			gw.transferIngresses(records, zone)
		case "Service":
			gw.transferServices(records, zone)
		case "HTTPRoute":
			gw.transferHTTPRoutes(records, zone)
		case "TLSRoute":
			gw.transferTLSRoutes(records, zone)
		case "GRPCRoute":
			gw.transferGRPCRoutes(records, zone)
		case "DNSEndpoint":
			gw.transferDNSEndpoints(records, zone)
		}
	}

	// Sort keys for consistent ordering
	var keys []string
	for k := range records {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Send records in sorted order
	for _, key := range keys {
		ch <- records[key]
	}
}

// transferIngresses collects DNS records from Ingress resources
func (gw *Gateway) transferIngresses(records map[string][]dns.RR, zone string) {
	for _, ctrl := range gw.Controller.controllers {
		items := ctrl.GetStore().List()
		for _, item := range items {
			ingress, ok := item.(*networking.Ingress)
			if !ok {
				continue
			}

			// Check if object should be ignored
			if checkIgnoreLabel(ingress.Labels) {
				continue
			}

			// Filter by ingress class if configured
			if len(gw.resourceFilters.ingressClasses) > 0 {
				ingressClass := ingress.Spec.IngressClassName
				if ingressClass == nil || !contains(gw.resourceFilters.ingressClasses, *ingressClass) {
					continue
				}
			}

			// Get IPs from ingress
			addrs := fetchIngressLoadBalancerIPs(ingress.Status.LoadBalancer.Ingress)
			if len(addrs) == 0 {
				continue
			}

			// Generate records for each rule
			for _, rule := range ingress.Spec.Rules {
				if rule.Host == "" {
					continue
				}

				hostname := strings.ToLower(rule.Host)
				if !strings.HasSuffix(hostname, zone) {
					continue
				}

				fqdn := dns.Fqdn(hostname)
				addRecords(records, fqdn, gw.A(fqdn, ipv4Only(addrs)))
				addRecords(records, fqdn, gw.AAAA(fqdn, ipv6Only(addrs)))
			}
		}
	}
}

// transferServices collects DNS records from Service resources
func (gw *Gateway) transferServices(records map[string][]dns.RR, zone string) {
	for _, ctrl := range gw.Controller.controllers {
		items := ctrl.GetStore().List()
		for _, item := range items {
			service, ok := item.(*core.Service)
			if !ok {
				continue
			}

			// Check if object should be ignored
			if checkIgnoreLabel(service.Labels) {
				continue
			}

			// Only LoadBalancer services
			if service.Spec.Type != core.ServiceTypeLoadBalancer {
				continue
			}

			// Get IPs
			var addrs []netip.Addr
			if len(service.Spec.ExternalIPs) > 0 {
				for _, ip := range service.Spec.ExternalIPs {
					addr, err := netip.ParseAddr(ip)
					if err == nil {
						addrs = append(addrs, addr)
					}
				}
			} else {
				addrs = fetchServiceLoadBalancerIPs(service.Status.LoadBalancer.Ingress)
			}

			if len(addrs) == 0 {
				continue
			}

			// Generate hostname from annotations or default
			hostnames := getServiceHostnames(service, zone)
			for _, hostname := range hostnames {
				if !strings.HasSuffix(hostname, zone) {
					continue
				}

				fqdn := dns.Fqdn(hostname)
				addRecords(records, fqdn, gw.A(fqdn, ipv4Only(addrs)))
				addRecords(records, fqdn, gw.AAAA(fqdn, ipv6Only(addrs)))
			}
		}
	}
}

// findGatewayController searches for and returns the gateway controller informer
func (gw *Gateway) findGatewayController() cache.SharedIndexInformer {
	for _, c := range gw.Controller.controllers {
		items := c.GetStore().List()
		if len(items) > 0 {
			if _, ok := items[0].(*gatewayapi_v1.Gateway); ok {
				return c
			}
		}
	}
	return nil
}

// routeInfo encapsulates the common route information needed for DNS record generation
type routeInfo struct {
	labels      map[string]string
	namespace   string
	parentRefs  []gatewayapi_v1.ParentReference
	hostnames   []gatewayapi_v1.Hostname
}

// transferRouteResources is a generic helper that processes route resources and generates DNS records
func (gw *Gateway) transferRouteResources(records map[string][]dns.RR, zone string, gwCtrl cache.SharedIndexInformer, route routeInfo) {
	// Check if object should be ignored
	if checkIgnoreLabel(route.labels) {
		return
	}

	// Lookup gateway addresses
	addrs := lookupGateways(gwCtrl, route.parentRefs, route.namespace, gw.resourceFilters.gatewayClasses)
	if len(addrs) == 0 {
		return
	}

	// Generate records for each hostname
	for _, hostname := range route.hostnames {
		hostnameStr := strings.ToLower(string(hostname))
		if !strings.HasSuffix(hostnameStr, zone) {
			continue
		}

		fqdn := dns.Fqdn(hostnameStr)
		addRecords(records, fqdn, gw.A(fqdn, ipv4Only(addrs)))
		addRecords(records, fqdn, gw.AAAA(fqdn, ipv6Only(addrs)))
	}
}

// transferHTTPRoutes collects DNS records from HTTPRoute resources
func (gw *Gateway) transferHTTPRoutes(records map[string][]dns.RR, zone string) {
	gwCtrl := gw.findGatewayController()
	if gwCtrl == nil {
		return
	}

	for _, ctrl := range gw.Controller.controllers {
		items := ctrl.GetStore().List()
		for _, item := range items {
			httpRoute, ok := item.(*gatewayapi_v1.HTTPRoute)
			if !ok {
				continue
			}

			gw.transferRouteResources(records, zone, gwCtrl, routeInfo{
				labels:     httpRoute.Labels,
				namespace:  httpRoute.Namespace,
				parentRefs: httpRoute.Spec.ParentRefs,
				hostnames:  httpRoute.Spec.Hostnames,
			})
		}
	}
}

// transferTLSRoutes collects DNS records from TLSRoute resources
func (gw *Gateway) transferTLSRoutes(records map[string][]dns.RR, zone string) {
	gwCtrl := gw.findGatewayController()
	if gwCtrl == nil {
		return
	}

	for _, ctrl := range gw.Controller.controllers {
		items := ctrl.GetStore().List()
		for _, item := range items {
			tlsRoute, ok := item.(*gatewayapi_v1alpha2.TLSRoute)
			if !ok {
				continue
			}

			// Convert TLSRoute hostnames to the common type
			var hostnames []gatewayapi_v1.Hostname
			for _, h := range tlsRoute.Spec.Hostnames {
				hostnames = append(hostnames, gatewayapi_v1.Hostname(h))
			}

			gw.transferRouteResources(records, zone, gwCtrl, routeInfo{
				labels:     tlsRoute.Labels,
				namespace:  tlsRoute.Namespace,
				parentRefs: tlsRoute.Spec.ParentRefs,
				hostnames:  hostnames,
			})
		}
	}
}

// transferGRPCRoutes collects DNS records from GRPCRoute resources
func (gw *Gateway) transferGRPCRoutes(records map[string][]dns.RR, zone string) {
	gwCtrl := gw.findGatewayController()
	if gwCtrl == nil {
		return
	}

	for _, ctrl := range gw.Controller.controllers {
		items := ctrl.GetStore().List()
		for _, item := range items {
			grpcRoute, ok := item.(*gatewayapi_v1.GRPCRoute)
			if !ok {
				continue
			}

			gw.transferRouteResources(records, zone, gwCtrl, routeInfo{
				labels:     grpcRoute.Labels,
				namespace:  grpcRoute.Namespace,
				parentRefs: grpcRoute.Spec.ParentRefs,
				hostnames:  grpcRoute.Spec.Hostnames,
			})
		}
	}
}

// transferDNSEndpoints collects DNS records from DNSEndpoint resources
func (gw *Gateway) transferDNSEndpoints(records map[string][]dns.RR, zone string) {
	for _, ctrl := range gw.Controller.controllers {
		items := ctrl.GetStore().List()
		for _, item := range items {
			dnsEndpoint, ok := item.(*externaldnsv1.DNSEndpoint)
			if !ok {
				continue
			}

			// Check if object should be ignored
			if checkIgnoreLabel(dnsEndpoint.Labels) {
				continue
			}

			for _, endpoint := range dnsEndpoint.Spec.Endpoints {
				hostname := strings.ToLower(endpoint.DNSName)
				if !strings.HasSuffix(hostname, zone) {
					continue
				}

				fqdn := dns.Fqdn(hostname)

				switch endpoint.RecordType {
				case "A", "AAAA":
					var addrs []netip.Addr
					for _, target := range endpoint.Targets {
						addr, err := netip.ParseAddr(target)
						if err == nil {
							addrs = append(addrs, addr)
						}
					}
					if endpoint.RecordType == "A" {
						addRecords(records, fqdn, gw.A(fqdn, ipv4Only(addrs)))
					} else {
						addRecords(records, fqdn, gw.AAAA(fqdn, ipv6Only(addrs)))
					}
				case "TXT":
					addRecords(records, fqdn, gw.TXT(fqdn, endpoint.Targets))
				}
			}
		}
	}
}

// Helper functions

func addRecords(records map[string][]dns.RR, key string, rrs []dns.RR) {
	if len(rrs) > 0 {
		records[key] = append(records[key], rrs...)
	}
}

func ipv4Only(addrs []netip.Addr) []netip.Addr {
	var result []netip.Addr
	for _, addr := range addrs {
		if addr.Is4() {
			result = append(result, addr)
		}
	}
	return result
}

func ipv6Only(addrs []netip.Addr) []netip.Addr {
	var result []netip.Addr
	for _, addr := range addrs {
		if addr.Is6() {
			result = append(result, addr)
		}
	}
	return result
}



func getServiceHostnames(service *core.Service, zone string) []string {
	var hostnames []string

	// Check annotations
	if hostname, ok := service.Annotations[hostnameAnnotationKey]; ok {
		hostnames = append(hostnames, strings.Split(hostname, ",")...)
	}
	if hostname, ok := service.Annotations[externalDnsHostnameAnnotationKey]; ok {
		hostnames = append(hostnames, strings.Split(hostname, ",")...)
	}

	// Default to service.namespace.zone
	if len(hostnames) == 0 {
		hostnames = append(hostnames, service.Name+"."+service.Namespace+"."+zone)
	}

	// Clean up hostnames
	var cleaned []string
	for _, h := range hostnames {
		h = strings.TrimSpace(h)
		if h != "" {
			cleaned = append(cleaned, h)
		}
	}

	return cleaned
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
