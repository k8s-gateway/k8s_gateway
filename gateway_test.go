package gateway

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/coredns/coredns/plugin/pkg/fall"

	"github.com/coredns/coredns/plugin/pkg/dnstest"
	"github.com/coredns/coredns/plugin/test"

	"github.com/miekg/dns"
)

type FallthroughCase struct {
	test.Case
	FallthroughZones    []string
	FallthroughExpected bool
}

type Fallen struct {
	error
}

func TestLookup(t *testing.T) {
	ctrl := &KubeController{hasSynced: true}

	gw := newGateway()
	gw.Zones = []string{"example.com."}
	gw.Next = test.NextHandler(dns.RcodeSuccess, nil)
	gw.ExternalAddrFunc = gw.SelfAddress
	gw.Controller = ctrl
	real := []string{"Ingress", "Service", "HTTPRoute", "TLSRoute", "GRPCRoute", "DNSEndpoint"}
	fake := []string{"Pod", "Gateway"}

	for _, resource := range real {
		if found := gw.lookupResource(resource); found == nil {
			t.Errorf("Could not lookup supported resource %s", resource)
		}
	}

	for _, resource := range fake {
		if found := gw.lookupResource(resource); found != nil {
			t.Errorf("Located unsupported resource %s", resource)
		}
	}
}

func TestPlugin(t *testing.T) {
	ctrl := &KubeController{hasSynced: true}

	gw := newGateway()
	gw.Zones = []string{"example.com."}
	gw.Next = test.NextHandler(dns.RcodeSuccess, nil)
	gw.ExternalAddrFunc = gw.SelfAddress
	gw.Controller = ctrl
	setupLookupFuncs(gw)

	ctx := context.TODO()
	for i, tc := range tests {
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

func TestPluginFallthrough(t *testing.T) {
	ctrl := &KubeController{hasSynced: true}
	gw := newGateway()
	gw.Zones = []string{"example.com."}
	gw.Next = test.NextHandler(dns.RcodeSuccess, Fallen{})
	gw.ExternalAddrFunc = gw.SelfAddress
	gw.Controller = ctrl
	setupLookupFuncs(gw)

	ctx := context.TODO()
	for i, tc := range testsFallthrough {
		r := tc.Msg()
		w := dnstest.NewRecorder(&test.ResponseWriter{})

		gw.Fall = fall.F{Zones: tc.FallthroughZones}
		_, err := gw.ServeDNS(ctx, w, r)

		if errors.As(err, &Fallen{}) && !tc.FallthroughExpected {
			t.Fatalf("Test %d query resulted unexpectedly in a fall through instead of a response", i)
		}
		if err == nil && tc.FallthroughExpected {
			t.Fatalf("Test %d query resulted unexpectedly in a response instead of a fall through", i)
		}
	}
}

var tests = []test.Case{
	// Existing Service IPv4 | Test 0
	{
		Qname: "svc1.ns1.example.com.", Qtype: dns.TypeA, Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.A("svc1.ns1.example.com.   60  IN  A   192.0.1.1"),
		},
	},
	// Existing Ingress | Test 1
	{
		Qname: "domain.example.com.", Qtype: dns.TypeA, Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.A("domain.example.com. 60  IN  A   192.0.0.1"),
		},
	},
	// Ingress takes precedence over services | Test 2
	{
		Qname: "svc2.ns1.example.com.", Qtype: dns.TypeA, Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.A("svc2.ns1.example.com.   60  IN  A   192.0.0.2"),
		},
	},
	// Non-existing Service | Test 3
	{
		Qname: "svcX.ns1.example.com.", Qtype: dns.TypeA, Rcode: dns.RcodeNameError,
		Ns: []dns.RR{
			test.SOA("example.com.  60  IN  SOA dns1.kube-system.example.com. hostmaster.example.com. 1499347823 7200 1800 86400 5"),
		},
	},
	// Non-existing Ingress | Test 4
	{
		Qname: "d0main.example.com.", Qtype: dns.TypeA, Rcode: dns.RcodeNameError,
		Ns: []dns.RR{
			test.SOA("example.com.  60  IN  SOA dns1.kube-system.example.com. hostmaster.example.com. 1499347823 7200 1800 86400 5"),
		},
	},
	// SOA for the existing domain | Test 5
	{
		Qname: "domain.example.com.", Qtype: dns.TypeSOA, Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.SOA("example.com.  60  IN  SOA dns1.kube-system.example.com. hostmaster.example.com. 1499347823 7200 1800 86400 5"),
		},
	},
	// Service with no public addresses | Test 6
	{
		Qname: "svc3.ns1.example.com.", Qtype: dns.TypeA, Rcode: dns.RcodeNameError,
		Ns: []dns.RR{
			test.SOA("example.com.  60  IN  SOA dns1.kube-system.example.com. hostmaster.example.com. 1499347823 7200 1800 86400 5"),
		},
	},
	// Real service, wrong query type | Test 7
	{
		Qname: "svc3.ns1.example.com.", Qtype: dns.TypeCNAME, Rcode: dns.RcodeSuccess,
		Ns: []dns.RR{
			test.SOA("example.com.  60  IN  SOA dns1.kube-system.example.com. hostmaster.example.com. 1499347823 7200 1800 86400 5"),
		},
	},
	// Ingress FQDN == zone | Test 8
	{
		Qname: "example.com.", Qtype: dns.TypeA, Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.A("example.com.    60  IN  A   192.0.0.3"),
		},
	},
	// Existing Ingress with a mix of lower and upper case letters | Test 9
	{
		Qname: "dOmAiN.eXamPLe.cOm.", Qtype: dns.TypeA, Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.A("domain.example.com. 60  IN  A   192.0.0.1"),
		},
	},
	// Existing Service with a mix of lower and upper case letters | Test 10
	{
		Qname: "svC1.Ns1.exAmplE.Com.", Qtype: dns.TypeA, Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.A("svc1.ns1.example.com.   60  IN  A   192.0.1.1"),
		},
	},
	// basic gateway API lookup | Test 13
	{
		Qname: "domain.gw.example.com.", Qtype: dns.TypeA, Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.A("domain.gw.example.com.  60  IN  A   192.0.2.1"),
		},
	},
	// gateway API lookup priority over Ingress | Test 14
	{
		Qname: "shadow.example.com.", Qtype: dns.TypeA, Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.A("shadow.example.com. 60  IN  A   192.0.2.4"),
		},
	},
	// Existing Service A record, but no AAAA record | Test 15
	{
		Qname: "svc2.ns1.example.com.", Qtype: dns.TypeAAAA, Rcode: dns.RcodeSuccess,
		Ns: []dns.RR{
			test.SOA("example.com.  60  IN  SOA dns1.kube-system.example.com. hostmaster.example.com. 1499347823 7200 1800 86400 5"),
		},
	},
	// Existing Service IPv6 | Test 16
	{
		Qname: "svc1.ns1.example.com.", Qtype: dns.TypeAAAA, Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.AAAA("svc1.ns1.example.com.    60  IN  AAAA    fd12:3456:789a:1::"),
		},
	},
	// lookup apex NS record | Test 17
	{
		Qname: "example.com.", Qtype: dns.TypeNS, Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.NS("example.com.   60  IN  NS  dns1.kube-system.example.com"),
		},
		Extra: []dns.RR{
			test.A("dns1.kube-system.example.com.   60  IN  A   192.0.1.53"),
		},
	},
	// Lookup that relies on a wildcard | Test 18
	{
		Qname: "not-explicitly-defined-label.wildcard.example.com.", Qtype: dns.TypeA, Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.A("not-explicitly-defined-label.wildcard.example.com. 60  IN  A   192.0.0.6"),
		},
	},
	// Lookup with a matching wildcard but a more specific entry | Test 19
	{
		Qname: "specific-subdomain.wildcard.example.com.", Qtype: dns.TypeA, Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.A("specific-subdomain.wildcard.example.com. 60  IN  A   192.0.0.7"),
		},
	},
	// Existing Endpoint | TXT record
	{
		Qname: "endpoint.example.com.", Qtype: dns.TypeTXT, Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.TXT("endpoint.example.com. 60  IN  TXT   \"Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor incididunt ut labore et dolore magna aliqua. Ut enim ad minim veniam, quis nostrud exercitation ullamco laboris nisi ut aliquip ex ea commodo consequat. Duis aute irure dolor i\" \"n reprehenderit in voluptate velit esse cillum dolore eu fugiat nulla pariatur. Excepteur sint occaecat cupidatat non proident, sunt in culpa qui officia deserunt mollit anim id est laborum.\""),
		},
	},
	// Non-existing Endpoint | TXT record
	{
		Qname: "endpointX.ns1.example.com.", Qtype: dns.TypeTXT, Rcode: dns.RcodeNameError,
		Ns: []dns.RR{
			test.SOA("example.com.  60  IN  SOA dns1.kube-system.example.com. hostmaster.example.com. 1499347823 7200 1800 86400 5"),
		},
	},
}

var testsFallthrough = []FallthroughCase{
	// Match found, fallthrough enabled | Test 0
	{
		Case:             test.Case{Qname: "example.com.", Qtype: dns.TypeA},
		FallthroughZones: []string{"."}, FallthroughExpected: false,
	},
	// No match found, fallthrough enabled | Test 1
	{
		Case:             test.Case{Qname: "non-existent.example.com.", Qtype: dns.TypeA},
		FallthroughZones: []string{"."}, FallthroughExpected: true,
	},
	// Match found, fallthrough for different zone | Test 2
	{
		Case:             test.Case{Qname: "example.com.", Qtype: dns.TypeA},
		FallthroughZones: []string{"not-example.com."}, FallthroughExpected: false,
	},
	// No match found, fallthrough for different zone | Test 3
	{
		Case:             test.Case{Qname: "non-existent.example.com.", Qtype: dns.TypeA},
		FallthroughZones: []string{"not-example.com."}, FallthroughExpected: false,
	},
	// No fallthrough on gw apex | Test 4
	{
		Case:             test.Case{Qname: "dns1.kube-system.example.com.", Qtype: dns.TypeA},
		FallthroughZones: []string{"."}, FallthroughExpected: false,
	},
}

var testServiceIndexes = map[string][]netip.Addr{
	"svc1.ns1":         {netip.MustParseAddr("192.0.1.1"), netip.MustParseAddr("fd12:3456:789a:1::")},
	"svc2.ns1":         {netip.MustParseAddr("192.0.1.2")},
	"svc3.ns1":         {},
	"dns1.kube-system": {netip.MustParseAddr("192.0.1.53")},
}

func testServiceLookup(keys []string) (results []netip.Addr, raws []string) {
	for _, key := range keys {
		results = append(results, testServiceIndexes[strings.ToLower(key)]...)
	}
	return results, raws
}

var testIngressIndexes = map[string][]netip.Addr{
	"domain.example.com":                      {netip.MustParseAddr("192.0.0.1")},
	"svc2.ns1.example.com":                    {netip.MustParseAddr("192.0.0.2")},
	"example.com":                             {netip.MustParseAddr("192.0.0.3")},
	"shadow.example.com":                      {netip.MustParseAddr("192.0.0.4")},
	"shadow-vs.example.com":                   {netip.MustParseAddr("192.0.0.5")},
	"*.wildcard.example.com":                  {netip.MustParseAddr("192.0.0.6")},
	"specific-subdomain.wildcard.example.com": {netip.MustParseAddr("192.0.0.7")},
}

func testIngressLookup(keys []string) (results []netip.Addr, raws []string) {
	for _, key := range keys {
		results = append(results, testIngressIndexes[strings.ToLower(key)]...)
	}
	return results, raws
}

var testRouteIndexes = map[string][]netip.Addr{
	"domain.gw.example.com": {netip.MustParseAddr("192.0.2.1")},
	"shadow.example.com":    {netip.MustParseAddr("192.0.2.4")},
}

func testRouteLookup(keys []string) (results []netip.Addr, raws []string) {
	for _, key := range keys {
		results = append(results, testRouteIndexes[strings.ToLower(key)]...)
	}
	return results, raws
}

var testDNSEndpointIndexes = map[string][]netip.Addr{
	"domain.endpoint.example.com": {netip.MustParseAddr("192.0.4.1")},
	"endpoint.example.com":        {netip.MustParseAddr("192.0.4.4")},
}

// test implementation for TXT multiple records does not work correctly
// because it is confused with the concatenation of strings longer than 255 bytes
// The loop in https://github.com/coredns/coredns/blob/master/plugin/test/helpers.go#L209
// may be the origin of the problem
var testDNSEndpointTxtIndexes = map[string][]string{
	"endpoint.example.com":        {"Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor incididunt ut labore et dolore magna aliqua. Ut enim ad minim veniam, quis nostrud exercitation ullamco laboris nisi ut aliquip ex ea commodo consequat. Duis aute irure dolor in reprehenderit in voluptate velit esse cillum dolore eu fugiat nulla pariatur. Excepteur sint occaecat cupidatat non proident, sunt in culpa qui officia deserunt mollit anim id est laborum."},
}

func testDNSEndpointLookup(keys []string) (results []netip.Addr, raws []string) {
	for _, key := range keys {
		results = append(results, testDNSEndpointIndexes[strings.ToLower(key)]...)
	}
	for _, key := range keys {
		raws = append(raws, testDNSEndpointTxtIndexes[strings.ToLower(key)]...)
	}
	return results, raws
}

func setupLookupFuncs(gw *Gateway) {
	if resource := gw.lookupResource("Ingress"); resource != nil {
		resource.lookup = testIngressLookup
	}
	if resource := gw.lookupResource("Service"); resource != nil {
		resource.lookup = testServiceLookup
	}
	if resource := gw.lookupResource("HTTPRoute"); resource != nil {
		resource.lookup = testRouteLookup
	}
	if resource := gw.lookupResource("TLSRoute"); resource != nil {
		resource.lookup = testRouteLookup
	}
	if resource := gw.lookupResource("GRPCRoute"); resource != nil {
		resource.lookup = testRouteLookup
	}
	if resource := gw.lookupResource("DNSEndpoint"); resource != nil {
		resource.lookup = testDNSEndpointLookup
	}
}
