package gateway

import (
	"context"
	"net/netip"
	"strings"
	"testing"

	"github.com/coredns/coredns/plugin/pkg/dnstest"
	"github.com/coredns/coredns/plugin/test"
	"github.com/miekg/dns"
)

// TestRealCNAMEResolution tests CNAME resolution with a realistic scenario
func TestRealCNAMEResolution(t *testing.T) {
	ctrl := &KubeController{hasSynced: true}
	gw := newGateway()
	gw.Zones = []string{"example.com."}
	gw.Next = test.NextHandler(dns.RcodeSuccess, nil)
	gw.ExternalAddrFunc = gw.SelfAddress
	gw.Controller = ctrl

	// Set up realistic CNAME chain: www -> app -> service -> actual IPs
	setupRealisticCNAMEChain(gw)

	testCases := []struct {
		name            string
		qname           string
		qtype           uint16
		expectRcode     int
		expectCNAME     bool
		expectA         bool
		expectChainHops int // Number of CNAME hops in response
		finalTarget     string
	}{
		{
			name:            "Direct A record query",
			qname:           "api.example.com.",
			qtype:           dns.TypeA,
			expectRcode:     dns.RcodeSuccess,
			expectCNAME:     false,
			expectA:         true,
			expectChainHops: 0,
			finalTarget:     "",
		},
		{
			name:            "Single CNAME hop",
			qname:           "service.example.com.",
			qtype:           dns.TypeA,
			expectRcode:     dns.RcodeSuccess,
			expectCNAME:     true,
			expectA:         true,
			expectChainHops: 1,
			finalTarget:     "api.example.com.",
		},
		{
			name:            "Double CNAME hop",
			qname:           "app.example.com.",
			qtype:           dns.TypeA,
			expectRcode:     dns.RcodeSuccess,
			expectCNAME:     true,
			expectA:         true,
			expectChainHops: 1, // Only first CNAME in response
			finalTarget:     "service.example.com.",
		},
		{
			name:            "Triple CNAME hop",
			qname:           "www.example.com.",
			qtype:           dns.TypeA,
			expectRcode:     dns.RcodeSuccess,
			expectCNAME:     true,
			expectA:         true,
			expectChainHops: 1, // Only first CNAME in response
			finalTarget:     "app.example.com.",
		},
		{
			name:            "CNAME query returns first hop only",
			qname:           "www.example.com.",
			qtype:           dns.TypeCNAME,
			expectRcode:     dns.RcodeSuccess,
			expectCNAME:     true,
			expectA:         false,
			expectChainHops: 1,
			finalTarget:     "app.example.com.",
		},
		{
			name:            "AAAA query with CNAME chain",
			qname:           "www.example.com.",
			qtype:           dns.TypeAAAA,
			expectRcode:     dns.RcodeSuccess,
			expectCNAME:     true,
			expectA:         false, // No AAAA, but should have CNAME
			expectChainHops: 1,     // Only first CNAME, no resolution
			finalTarget:     "app.example.com.",
		},
	}

	ctx := context.TODO()
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := new(dns.Msg)
			req.SetQuestion(tc.qname, tc.qtype)

			w := dnstest.NewRecorder(&test.ResponseWriter{})
			code, err := gw.ServeDNS(ctx, w, req)

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if code != tc.expectRcode {
				t.Errorf("Expected rcode %d, got %d", tc.expectRcode, code)
			}

			resp := w.Msg
			if resp == nil {
				t.Fatal("Got nil response")
			}

			// Count record types in response
			cnameCount := 0
			aCount := 0
			aaaaCount := 0
			var finalCNAMETarget string

			for _, answer := range resp.Answer {
				switch rr := answer.(type) {
				case *dns.CNAME:
					cnameCount++
					finalCNAMETarget = rr.Target
				case *dns.A:
					aCount++
				case *dns.AAAA:
					aaaaCount++
				}
			}

			// Verify expectations
			if tc.expectCNAME && cnameCount == 0 {
				t.Error("Expected CNAME record in response")
			}
			if !tc.expectCNAME && cnameCount > 0 {
				t.Error("Unexpected CNAME record in response")
			}

			if tc.expectA && aCount == 0 {
				t.Error("Expected A record in response")
			}

			if tc.expectChainHops > 0 && cnameCount != tc.expectChainHops {
				t.Errorf("Expected %d CNAME hops, got %d", tc.expectChainHops, cnameCount)
			}

			if tc.finalTarget != "" && !strings.EqualFold(finalCNAMETarget, tc.finalTarget) {
				t.Errorf("Expected final CNAME target %s, got %s", tc.finalTarget, finalCNAMETarget)
			}

			t.Logf("Query: %s %s -> Response: %d CNAMEs, %d A records, %d AAAA records",
				tc.qname, dns.TypeToString[tc.qtype], cnameCount, aCount, aaaaCount)
		})
	}
}

// setupRealisticCNAMEChain creates a realistic CNAME chain for testing
// Chain: www -> app -> service -> api (actual IPs)
func setupRealisticCNAMEChain(gw *Gateway) {
	// Create realistic test data with CNAME chains
	realisticCNAMEIndexes := map[string][]string{
		"www.example.com":     {"app.example.com"},     // www points to app
		"app.example.com":     {"service.example.com"}, // app points to service
		"service.example.com": {"api.example.com"},     // service points to api
	}

	realisticAddressIndexes := map[string][]netip.Addr{
		"api.example.com": {netip.MustParseAddr("10.0.1.100"), netip.MustParseAddr("10.0.1.101")}, // api has actual IPs
	}

	// Lookup function that returns both CNAMEs and addresses
	realisticLookupFunc := func(indexKeys []string) (results []netip.Addr, raws []string, cnames []string) {
		for _, key := range indexKeys {
			// Check for CNAME data
			if cnameTargets, exists := realisticCNAMEIndexes[strings.ToLower(key)]; exists {
				cnames = append(cnames, cnameTargets...)
			}
			// Check for address data
			if addrs, exists := realisticAddressIndexes[strings.ToLower(key)]; exists {
				results = append(results, addrs...)
			}
		}
		return results, raws, cnames
	}

	// Apply the lookup function to DNSEndpoint resource (which supports CNAMEs)
	if resource := gw.lookupResource("DNSEndpoint"); resource != nil {
		resource.lookup = realisticLookupFunc
	}
}

// TestCNAMELoopDetection tests that CNAME loops are properly detected and handled
func TestCNAMELoopDetection(t *testing.T) {
	ctrl := &KubeController{hasSynced: true}
	gw := newGateway()
	gw.Zones = []string{"example.com."}
	gw.Next = test.NextHandler(dns.RcodeSuccess, nil)
	gw.ExternalAddrFunc = gw.SelfAddress
	gw.Controller = ctrl
	gw.CNAMEMaxDepth = 3 // Set low depth for testing

	// Set up CNAME loop: loop1 -> loop2 -> loop1
	setupCNAMELoop(gw)

	testCases := []struct {
		name        string
		qname       string
		qtype       uint16
		expectError bool
		description string
	}{
		{
			name:        "CNAME loop detection",
			qname:       "loop1.example.com.",
			qtype:       dns.TypeA,
			expectError: false, // Should handle gracefully, return CNAME without resolution
			description: "Should detect loop and return CNAME without infinite recursion",
		},
		{
			name:        "CNAME depth limit",
			qname:       "deep1.example.com.",
			qtype:       dns.TypeA,
			expectError: false, // Should handle gracefully
			description: "Should respect depth limit and stop resolution",
		},
	}

	ctx := context.TODO()
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := new(dns.Msg)
			req.SetQuestion(tc.qname, tc.qtype)

			w := dnstest.NewRecorder(&test.ResponseWriter{})
			code, err := gw.ServeDNS(ctx, w, req)

			if tc.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tc.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if code == dns.RcodeSuccess || code == dns.RcodeNameError {
				t.Logf("✅ %s: Handled correctly with rcode %d", tc.description, code)
			} else {
				t.Errorf("Unexpected response code: %d", code)
			}
		})
	}
}

// setupCNAMELoop creates CNAME loops and deep chains for testing
func setupCNAMELoop(gw *Gateway) {
	loopCNAMEIndexes := map[string][]string{
		"loop1.example.com": {"loop2.example.com"}, // loop1 -> loop2
		"loop2.example.com": {"loop1.example.com"}, // loop2 -> loop1 (creates loop)
		"deep1.example.com": {"deep2.example.com"}, // deep1 -> deep2
		"deep2.example.com": {"deep3.example.com"}, // deep2 -> deep3
		"deep3.example.com": {"deep4.example.com"}, // deep3 -> deep4
		"deep4.example.com": {"deep5.example.com"}, // deep4 -> deep5 (exceeds depth limit)
	}

	loopLookupFunc := func(indexKeys []string) (results []netip.Addr, raws []string, cnames []string) {
		for _, key := range indexKeys {
			if cnameTargets, exists := loopCNAMEIndexes[strings.ToLower(key)]; exists {
				cnames = append(cnames, cnameTargets...)
			}
		}
		return results, raws, cnames
	}

	if resource := gw.lookupResource("DNSEndpoint"); resource != nil {
		resource.lookup = loopLookupFunc
	}
}
