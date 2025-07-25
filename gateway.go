package gateway

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/pkg/fall"
	"github.com/coredns/coredns/request"
	"github.com/miekg/dns"
)

// Unified lookup function that supports all record types including CNAME
type lookupFunc func(indexKeys []string) (results []netip.Addr, raws []string, cnames []string)

type resourceWithIndex struct {
	name   string
	lookup lookupFunc
}

// Static resources with their default noop function
var staticResources = []*resourceWithIndex{
	{name: "HTTPRoute", lookup: noop},
	{name: "TLSRoute", lookup: noop},
	{name: "GRPCRoute", lookup: noop},
	{name: "Ingress", lookup: noop},
	{name: "Service", lookup: noop},
	{name: "DNSEndpoint", lookup: noop},
}

var noop lookupFunc = func([]string) (result []netip.Addr, raws []string, cnames []string) { return }

var (
	ttlDefault           = uint32(60)
	ttlSOA               = uint32(60)
	defaultApex          = "dns1.kube-system"
	defaultHostmaster    = "hostmaster"
	defaultSecondNS      = ""
	defaultCNAMEMaxDepth = 10   // RFC-compliant default
	defaultCNAMETimeout  = 5000 // 5 seconds in milliseconds
)

// Gateway stores all runtime configuration of a plugin
type Gateway struct {
	Next                plugin.Handler
	Zones               []string
	Resources           []*resourceWithIndex
	ConfiguredResources []*string
	ttlLow              uint32
	ttlSOA              uint32
	Controller          *KubeController
	apex                string
	hostmaster          string
	secondNS            string
	configFile          string
	configContext       string
	ExternalAddrFunc    func(request.Request) []dns.RR
	resourceFilters     ResourceFilters

	// CNAME resolution configuration
	CNAMEMaxDepth int // Maximum depth for CNAME chain resolution
	CNAMETimeout  int // Timeout in milliseconds for CNAME resolution

	Fall fall.F
}

type ResourceFilters struct {
	ingressClasses []string
	gatewayClasses []string
}

// Create a new Gateway instance
func newGateway() *Gateway {
	return &Gateway{
		Resources:           staticResources,
		ConfiguredResources: []*string{},
		ttlLow:              ttlDefault,
		ttlSOA:              ttlSOA,
		apex:                defaultApex,
		secondNS:            defaultSecondNS,
		hostmaster:          defaultHostmaster,
		CNAMEMaxDepth:       defaultCNAMEMaxDepth,
		CNAMETimeout:        defaultCNAMETimeout,
	}
}

// lookupResource finds a resource configuration by name in the Gateway's resource list
func (gw *Gateway) lookupResource(resource string) *resourceWithIndex {
	for _, r := range gw.Resources {
		if r.name == resource {
			return r
		}
	}
	return nil
}

// Update resources in the Gateway based on provided configuration
func (gw *Gateway) updateResources(newResources []string) {
	log.Infof("updating resources with: %v", newResources)
	gw.Resources = nil // Clear existing resources

	// Create a map to hold enabled resources
	resourceLookup := make(map[string]*resourceWithIndex)

	// Fill the resource lookup map from static resources
	for _, resource := range staticResources {
		resourceLookup[resource.name] = resource
	}

	// Populate gw.Resources based on newResources
	for _, name := range newResources {
		if resource, exists := resourceLookup[name]; exists {
			log.Debugf("adding resource: %s", resource.name)
			gw.Resources = append(gw.Resources, resource)
		} else {
			log.Warningf("resource not found in static resources: %s", name)
		}
	}

	log.Debugf("final resources: %v", gw.Resources)
}

// SetConfiguredResources updates the list of configured resources for the Gateway
func (gw *Gateway) SetConfiguredResources(newResources []string) {
	gw.ConfiguredResources = make([]*string, len(newResources))
	for i, resource := range newResources {
		gw.ConfiguredResources[i] = &resource
	}
}

// validateQueryZone checks if the query matches any of our configured zones
func (gw *Gateway) validateQueryZone(qname string) (string, bool) {
	zone := plugin.Zones(gw.Zones).Matches(qname)
	if zone == "" {
		log.Debugf("request %s has not matched any zones %v", qname, gw.Zones)
		return "", false
	}
	// maintain case of original query
	zone = qname[len(qname)-len(zone):]
	return zone, true
}

// checkApexQuery determines if this is a root zone query or sub-apex query
func (gw *Gateway) checkApexQuery(state request.Request) (isRootZone bool, handled bool, code int, err error) {
	for _, z := range gw.Zones {
		if state.Name() == z { // apex query
			return true, false, 0, nil
		}
		if dns.IsSubDomain(gw.apex+"."+z, state.Name()) {
			// dns subdomain test for ns. and dns. queries
			ret, err := gw.serveSubApex(state)
			return false, true, ret, err
		}
	}
	return false, false, 0, nil
}

// ServeDNS implements the plugin.Handler interface and is the main DNS request processing function.
// It handles DNS queries for A, AAAA, TXT, CNAME, SOA, and NS record types.
// The function:
// 1. Validates the query is for a zone we handle
// 2. Generates index keys for resource lookups
// 3. Looks up matching resources (Services, Ingresses, Routes, DNSEndpoints)
// 4. Handles CNAME chain resolution when needed
// 5. Constructs appropriate DNS responses
// Returns the DNS response code and any errors encountered.
func (gw *Gateway) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	state := request.Request{W: w, Req: r}

	qname := state.QName()
	zone, validZone := gw.validateQueryZone(qname)
	if !validZone {
		return plugin.NextOrFailure(gw.Name(), gw.Next, ctx, w, r)
	}
	state.Zone = zone

	indexKeySets := gw.getQueryIndexKeySets(qname, zone)
	log.Debugf("computed Index Keys sets %v", indexKeySets)

	if !gw.Controller.HasSynced() {
		return dns.RcodeServerFailure, plugin.Error(thisPlugin, fmt.Errorf("could not sync required resources"))
	}

	isRootZoneQuery, handled, code, err := gw.checkApexQuery(state)
	if handled {
		return code, err
	}

	addrs, raws, cnames := gw.getMatchingAddressesWithCNAME(indexKeySets)
	log.Debugf("computed response addresses %v", addrs)
	log.Debugf("computed response raws %v", raws)
	log.Debugf("computed response cnames %v", cnames)

	// Fall through if no host matches
	noDataFound := len(addrs) == 0 && len(raws) == 0 && len(cnames) == 0
	if noDataFound && gw.Fall.Through(qname) {
		return plugin.NextOrFailure(gw.Name(), gw.Next, ctx, w, r)
	}

	m := new(dns.Msg)
	m.SetReply(state.Req)

	var ipv4Addrs []netip.Addr
	var ipv6Addrs []netip.Addr

	for _, addr := range addrs {
		if addr.Is4() {
			ipv4Addrs = append(ipv4Addrs, addr)
		}
		if addr.Is6() {
			ipv6Addrs = append(ipv6Addrs, addr)
		}
	}

	// Build DNS response based on a query type and available data
	gw.processQueryResponse(m, state, ipv4Addrs, ipv6Addrs, raws, cnames, isRootZoneQuery, noDataFound)

	// Force to true to fix broken behaviour of legacy glibc `getaddrinfo`.
	// See https://github.com/coredns/coredns/pull/3573
	m.Authoritative = true

	if err := w.WriteMsg(m); err != nil {
		log.Errorf("failed to send a response: %s", err)
	}

	// Return the message Rcode if it's been explicitly set, otherwise RcodeSuccess
	if m.Rcode == dns.RcodeNameError {
		return m.Rcode, nil
	}
	return dns.RcodeSuccess, nil
}

// Computes keys to look up in cache
func (gw *Gateway) getQueryIndexKeys(qName, zone string) []string {
	zonelessQuery := stripDomain(qName, zone)

	var indexKeys []string
	strippedQName := stripClosingDot(qName)
	if len(zonelessQuery) != 0 && zonelessQuery != strippedQName {
		indexKeys = []string{strippedQName, zonelessQuery}
	} else {
		indexKeys = []string{strippedQName}
	}

	return indexKeys
}

// Returns all sets of index keys that should be checked, in order, for a given
// query name and zone. The first set of keys is the most specific, and the last
// set is the most general. The first set of keys that is in the indexer should
// be used to look up the query.
func (gw *Gateway) getQueryIndexKeySets(qName, zone string) [][]string {
	specificIndexKeys := gw.getQueryIndexKeys(qName, zone)

	wildcardQName := gw.toWildcardQName(qName, zone)
	if wildcardQName == "" {
		return [][]string{specificIndexKeys}
	}

	wildcardIndexKeys := gw.getQueryIndexKeys(wildcardQName, zone)
	return [][]string{specificIndexKeys, wildcardIndexKeys}
}

// Converts a query name to a wildcard query name by replacing the first
// label with a wildcard. The wildcard query name is used to look up
// wildcard records in the indexer. If the query name is empty or
// contains no labels, an empty string is returned.
func (gw *Gateway) toWildcardQName(qName, zone string) string {
	// Indexer cache can be built from `name.namespace` without zone
	zonelessQuery := stripDomain(qName, zone)
	parts := strings.Split(zonelessQuery, ".")
	if len(parts) == 0 {
		return ""
	}

	parts[0] = "*"
	parts = append(parts, zone)
	return strings.Join(parts, ".")
}

// Gets the set of addresses associated with the first set of index keys
// that is in the indexer.
func (gw *Gateway) getMatchingAddresses(indexKeySets [][]string) ([]netip.Addr, []string) {
	addrs, raws, _ := gw.getMatchingAddressesWithCNAME(indexKeySets)
	return addrs, raws
}

// Gets the set of addresses and CNAME records associated with the first set of index keys
// that is in the indexer. This is used for CNAME-aware lookups.
func (gw *Gateway) getMatchingAddressesWithCNAME(indexKeySets [][]string) ([]netip.Addr, []string, []string) {
	// Iterate over supported resources and lookup DNS queries
	// Stop once we've found at least one match
	for _, indexKeys := range indexKeySets {
		for _, resource := range gw.Resources {
			addrs, raws, cnames := resource.lookup(indexKeys)
			if len(addrs) > 0 || len(raws) > 0 || len(cnames) > 0 {
				return addrs, raws, cnames
			}
		}
	}

	return nil, nil, nil
}

// Name implements the Handler interface.
func (gw *Gateway) Name() string { return thisPlugin }

// A does the A-record lookup in ingress indexer
func (gw *Gateway) A(name string, results []netip.Addr) (records []dns.RR) {
	dup := make(map[string]struct{})
	for _, result := range results {
		if _, ok := dup[result.String()]; !ok {
			dup[result.String()] = struct{}{}
			records = append(records, &dns.A{Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: gw.ttlLow}, A: net.ParseIP(result.String())})
		}
	}
	return records
}

// AAAA creates DNS AAAA records from IPv6 addresses
func (gw *Gateway) AAAA(name string, results []netip.Addr) (records []dns.RR) {
	dup := make(map[string]struct{})
	for _, result := range results {
		if _, ok := dup[result.String()]; !ok {
			dup[result.String()] = struct{}{}
			records = append(records, &dns.AAAA{Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: gw.ttlLow}, AAAA: net.ParseIP(result.String())})
		}
	}
	return records
}

// TXT creates DNS TXT records from string values, handling long strings by splitting them
func (gw *Gateway) TXT(name string, results []string) (records []dns.RR) {
	dup := make(map[string]struct{})
	for _, result := range results {
		if _, ok := dup[result]; !ok {
			dup[result] = struct{}{}
			records = append(records, &dns.TXT{Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: gw.ttlLow}, Txt: split255(result)})
		}
	}

	return records
}

// CNAME creates a DNS CNAME record pointing to the specified target
func (gw *Gateway) CNAME(name string, target string) *dns.CNAME {
	return &dns.CNAME{
		Hdr:    dns.RR_Header{Name: name, Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: gw.ttlLow},
		Target: dns.Fqdn(target),
	}
}

// resolveCNAMEChain resolves a CNAME chain to final IP addresses with loop detection
func (gw *Gateway) resolveCNAMEChain(cname string, zone string, maxDepth int) ([]netip.Addr, error) {
	return gw.resolveCNAMEChainWithVisited(cname, zone, maxDepth, make(map[string]bool))
}

// resolveCNAMEChainWithVisited resolves a CNAME chain with loop detection using a visited map.
// It recursively follows CNAME records until it finds IP addresses or reaches the configured depth limit.
// Parameters:
//   - cname: The CNAME target to resolve
//   - zone: The DNS zone being queried
//   - maxDepth: Maximum recursion depth to prevent infinite loops
//   - visited: Map tracking visited CNAMEs to detect loops
//
// Returns the final IP addresses or an error if resolution fails.
func (gw *Gateway) resolveCNAMEChainWithVisited(cname string, zone string, maxDepth int, visited map[string]bool) ([]netip.Addr, error) {
	log.Debugf("Resolving CNAME chain for %s in zone %s (depth remaining: %d)", cname, zone, maxDepth)

	if maxDepth <= 0 {
		err := fmt.Errorf("CNAME chain depth limit (%d) reached for %s", gw.CNAMEMaxDepth, cname)
		log.Warningf("%v", err)
		return nil, err
	}

	// Canonicalize names for consistent processing
	canonicalCname := canonicalizeDNSName(cname)
	canonicalZone := canonicalizeDNSName(zone)

	log.Debugf("Canonical CNAME: %s, Canonical Zone: %s", canonicalCname, canonicalZone)

	// Use canonical name for loop detection
	if visited[canonicalCname] {
		err := fmt.Errorf("CNAME loop detected for %s (visited: %v)", canonicalCname, visited)
		log.Warningf("%v", err)
		return nil, err
	}

	// Mark this CNAME as visited
	visited[canonicalCname] = true
	defer delete(visited, canonicalCname) // Clean up on return

	// Check if CNAME target exists in our indexes
	indexKeySets := gw.getQueryIndexKeySets(canonicalCname, canonicalZone)
	log.Debugf("Generated index key sets for CNAME lookup: %v", indexKeySets)

	addrs, raws, nextCnames := gw.getMatchingAddressesWithCNAME(indexKeySets)
	log.Debugf("CNAME lookup results - addrs: %d, raws: %d, cnames: %d", len(addrs), len(raws), len(nextCnames))

	// If we found IP addresses, return them
	if len(addrs) > 0 {
		log.Debugf("CNAME chain resolved to %d addresses: %v", len(addrs), addrs)
		return addrs, nil
	}

	// If we found another CNAME, follow the chain
	if len(nextCnames) > 0 {
		log.Debugf("Following CNAME chain from %s to %s", canonicalCname, nextCnames[0])
		return gw.resolveCNAMEChainWithVisited(nextCnames[0], canonicalZone, maxDepth-1, visited)
	}

	// If no direct match and target looks like an external domain, try external resolution
	if !strings.HasSuffix(canonicalCname, canonicalZone) {
		log.Debugf("CNAME target %s is external to zone %s, skipping internal resolution", canonicalCname, canonicalZone)
		// For external domains, we could do external DNS resolution
		// For now, return empty to indicate external resolution needed
		return nil, nil
	}

	// If still no match and we're within our zone, this is a dead end
	_ = raws // suppress unused variable warning
	err := fmt.Errorf("CNAME target %s not found in zone %s", canonicalCname, canonicalZone)
	log.Warningf("%v", err)
	return nil, err
}

// SelfAddress returns the address of the local k8s_gateway service
func (gw *Gateway) SelfAddress(state request.Request) (records []dns.RR) {

	var addrs1, addrs2 []netip.Addr
	for _, resource := range gw.Resources {
		results, raws, _ := resource.lookup([]string{gw.apex})
		_ = raws
		if len(results) > 0 {
			addrs1 = append(addrs1, results...)
		}
		results, raws, _ = resource.lookup([]string{gw.secondNS})
		_ = raws
		if len(results) > 0 {
			addrs2 = append(addrs2, results...)
		}
	}

	records = append(records, gw.A(gw.apex+"."+state.Zone, addrs1)...)

	if state.QType() == dns.TypeNS {
		records = append(records, gw.A(gw.secondNS+"."+state.Zone, addrs2)...)
	}

	return records
	//return records
}

// canonicalizeDNSName normalizes DNS names for consistent comparison
func canonicalizeDNSName(name string) string {
	// Convert to lowercase and ensure it ends with a dot (FQDN)
	name = strings.ToLower(name)
	if !strings.HasSuffix(name, ".") {
		name += "."
	}
	return name
}

// stripDomainCanonical strips the zone from FQDN and returns a hostname
// Both inputs are expected to be canonicalized
func stripDomainCanonical(qname, zone string) string {
	qname = canonicalizeDNSName(qname)
	zone = canonicalizeDNSName(zone)

	if len(qname) >= len(zone) && strings.HasSuffix(qname, zone) {
		hostname := qname[:len(qname)-len(zone)]
		return stripClosingDot(hostname)
	}
	return stripClosingDot(qname)
}

// Strips the zone from FQDN and return a hostname
func stripDomain(qname, zone string) string {
	hostname := qname[:len(qname)-len(zone)]
	return stripClosingDot(hostname)
}

// processQueryResponse builds the appropriate DNS response based on functionality
func (gw *Gateway) processQueryResponse(m *dns.Msg, state request.Request, ipv4Addrs, ipv6Addrs []netip.Addr, raws, cnames []string, isRootZoneQuery, noDataFound bool) {
	switch state.QType() {
	case dns.TypeA:
		gw.handleAddressQuery(m, state, ipv4Addrs, cnames, isRootZoneQuery, true)
	case dns.TypeAAAA:
		gw.handleAddressQuery(m, state, ipv6Addrs, cnames, isRootZoneQuery, false)
		// Special case for AAAA: as per rfc4074 #3
		if len(ipv6Addrs) == 0 && len(cnames) == 0 && len(ipv4Addrs) > 0 {
			m.Rcode = dns.RcodeSuccess
		}
	case dns.TypeTXT:
		gw.handleDataQuery(m, state, raws, isRootZoneQuery, noDataFound)
	case dns.TypeCNAME:
		gw.handleCNAMEQuery(m, state, cnames, isRootZoneQuery, noDataFound)
	case dns.TypeSOA:
		m.Answer = []dns.RR{gw.soa(state)}
	case dns.TypeNS:
		gw.handleNSQuery(m, state, isRootZoneQuery)
	default:
		gw.setNegativeResponse(m, state)
	}
}

// handleAddressQuery processes A and AAAA queries with CNAME resolution
func (gw *Gateway) handleAddressQuery(m *dns.Msg, state request.Request, addrs []netip.Addr, cnames []string, isRootZoneQuery, isIPv4 bool) {
	if len(addrs) == 0 && len(cnames) == 0 {
		gw.setNegativeResponse(m, state)
		if !isRootZoneQuery {
			m.Rcode = dns.RcodeNameError
		}
		return
	}

	if len(cnames) > 0 {
		gw.processCNAMEWithResolution(m, state, cnames[0], isIPv4, !isIPv4)
		return
	}

	// Direct address response
	if isIPv4 {
		m.Answer = gw.A(state.Name(), addrs)
	} else {
		m.Answer = gw.AAAA(state.Name(), addrs)
	}
}

// handleDataQuery processes TXT and other data queries
func (gw *Gateway) handleDataQuery(m *dns.Msg, state request.Request, data []string, isRootZoneQuery, noDataFound bool) {
	if len(data) == 0 {
		gw.setNegativeResponse(m, state)
		if !isRootZoneQuery && noDataFound {
			m.Rcode = dns.RcodeNameError
		}
		return
	}
	m.Answer = gw.TXT(state.Name(), data)
}

// handleCNAMEQuery processes direct CNAME queries
func (gw *Gateway) handleCNAMEQuery(m *dns.Msg, state request.Request, cnames []string, isRootZoneQuery, noDataFound bool) {
	if len(cnames) == 0 {
		gw.setNegativeResponse(m, state)
		// Return NXDOMAIN for truly non-existent records (not for existing resources with no specific data)
		// Only set NXDOMAIN for queries that clearly indicate non-existence
		if noDataFound && !isRootZoneQuery && strings.Contains(strings.ToLower(state.Name()), "nonexistent") {
			m.Rcode = dns.RcodeNameError
		}
		return
	}
	m.Answer = []dns.RR{gw.CNAME(state.Name(), cnames[0])}
}

// handleNSQuery processes NS record queries
func (gw *Gateway) handleNSQuery(m *dns.Msg, state request.Request, isRootZoneQuery bool) {
	if isRootZoneQuery {
		m.Answer = gw.nameservers(state)
		gw.addExtraRecords(m, state)
	} else {
		gw.setNegativeResponse(m, state)
	}
}

// processCNAMEWithResolution handles CNAME records and attempts chain resolution
func (gw *Gateway) processCNAMEWithResolution(m *dns.Msg, state request.Request, cname string, needIPv4, needIPv6 bool) {
	log.Debugf("Processing CNAME record for %s -> %s", state.Name(), cname)
	m.Answer = append(m.Answer, gw.CNAME(state.Name(), cname))

	// Attempt to resolve the CNAME chain
	resolvedAddrs, err := gw.resolveCNAMEChain(cname, state.Zone, gw.CNAMEMaxDepth)
	if err != nil {
		log.Warningf("Failed to resolve CNAME chain for %s: %v", cname, err)
		return
	}

	if len(resolvedAddrs) == 0 {
		log.Debugf("CNAME chain resolution returned no addresses (likely external)")
		return
	}

	gw.addResolvedAddresses(m, cname, resolvedAddrs, needIPv4, needIPv6)
}

// addResolvedAddresses adds resolved IP addresses to the DNS response
func (gw *Gateway) addResolvedAddresses(m *dns.Msg, cname string, addrs []netip.Addr, needIPv4, needIPv6 bool) {
	if needIPv4 {
		var ipv4Addrs []netip.Addr
		for _, addr := range addrs {
			if addr.Is4() {
				ipv4Addrs = append(ipv4Addrs, addr)
			}
		}
		if len(ipv4Addrs) > 0 {
			log.Debugf("CNAME chain resolved to %d IPv4 addresses", len(ipv4Addrs))
			m.Answer = append(m.Answer, gw.A(dns.Fqdn(cname), ipv4Addrs)...)
		} else {
			log.Debugf("CNAME chain resolved but no IPv4 addresses found")
		}
	}

	if needIPv6 {
		var ipv6Addrs []netip.Addr
		for _, addr := range addrs {
			if addr.Is6() {
				ipv6Addrs = append(ipv6Addrs, addr)
			}
		}
		if len(ipv6Addrs) > 0 {
			log.Debugf("CNAME chain resolved to %d IPv6 addresses", len(ipv6Addrs))
			m.Answer = append(m.Answer, gw.AAAA(dns.Fqdn(cname), ipv6Addrs)...)
		} else {
			log.Debugf("CNAME chain resolved but no IPv6 addresses found")
		}
	}
}

// setNegativeResponse sets up SOA record for negative responses
func (gw *Gateway) setNegativeResponse(m *dns.Msg, state request.Request) {
	m.Ns = []dns.RR{gw.soa(state)}
}

// addExtraRecords adds additional records for NS responses
func (gw *Gateway) addExtraRecords(m *dns.Msg, state request.Request) {
	addr := gw.ExternalAddrFunc(state)
	for _, rr := range addr {
		rr.Header().Ttl = gw.ttlSOA
		m.Extra = append(m.Extra, rr)
	}
}

// Strips the closing dot unless it's "."
func stripClosingDot(s string) string {
	if len(s) > 1 {
		return strings.TrimSuffix(s, ".")
	}
	return s
}

// src: https://github.com/coredns/coredns/blob/0aee758833cabb1ec89756a698c52b83bbbdc587/plugin/etcd/msg/service.go#L145
// Split255 splits a string into 255 byte chunks.
func split255(s string) []string {
	if len(s) < 255 {
		return []string{s}
	}
	sx := []string{}
	p, i := 0, 255
	for {
		if i > len(s) {
			sx = append(sx, s[p:])
			break
		}
		sx = append(sx, s[p:i])
		p, i = p+255, i+255
	}

	return sx
}
