package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/coredns/coredns/plugin/pkg/dnstest"
	"github.com/coredns/coredns/plugin/test"
	"github.com/coredns/coredns/request"

	"github.com/miekg/dns"
)

func TestApex(t *testing.T) {

	ctrl := &KubeController{hasSynced: true}
	gw := newGateway()
	gw.Zones = []string{"example.com."}
	gw.Next = test.NextHandler(dns.RcodeSuccess, nil)
	gw.Controller = ctrl
	gw.ExternalAddrFunc = selfAddressTest
	setupEmptyLookupFuncs(gw)

	ctx := context.TODO()
	for i, tc := range testsApex {
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
		
		// For SOA records, verify the serial is dynamic and reasonable
		if tc.Qtype == dns.TypeSOA || hasSOAInSection(resp) {
			if !verifyDynamicSOA(t, resp, i) {
				continue
			}
		}
		
		// Use custom verification for tests with SOA records
		if err := verifySortedResponse(resp, tc); err != nil {
			t.Errorf("Test number #%d: %+v", i, err)
		}
	}
}

// hasSOAInSection checks if response has SOA in any section
func hasSOAInSection(msg *dns.Msg) bool {
	for _, rr := range msg.Answer {
		if rr.Header().Rrtype == dns.TypeSOA {
			return true
		}
	}
	for _, rr := range msg.Ns {
		if rr.Header().Rrtype == dns.TypeSOA {
			return true
		}
	}
	return false
}

// verifyDynamicSOA checks that SOA record has reasonable dynamic serial
func verifyDynamicSOA(t *testing.T, msg *dns.Msg, testNum int) bool {
	var soa *dns.SOA
	
	// Find SOA in answer or authority section
	for _, rr := range msg.Answer {
		if s, ok := rr.(*dns.SOA); ok {
			soa = s
			break
		}
	}
	if soa == nil {
		for _, rr := range msg.Ns {
			if s, ok := rr.(*dns.SOA); ok {
				soa = s
				break
			}
		}
	}
	
	if soa == nil {
		return true // No SOA to verify
	}
	
	// Verify serial is reasonable (Unix timestamp)
	now := uint32(time.Now().Unix())
	// Allow for some time skew (10 seconds before/after)
	if soa.Serial < now-10 || soa.Serial > now+10 {
		t.Errorf("Test %d: SOA serial %d is not close to current time %d", testNum, soa.Serial, now)
		return false
	}
	
	// Verify other SOA fields
	if soa.Refresh != 7200 {
		t.Errorf("Test %d: SOA Refresh should be 7200, got %d", testNum, soa.Refresh)
		return false
	}
	if soa.Retry != 1800 {
		t.Errorf("Test %d: SOA Retry should be 1800, got %d", testNum, soa.Retry)
		return false
	}
	if soa.Expire != 86400 {
		t.Errorf("Test %d: SOA Expire should be 86400, got %d", testNum, soa.Expire)
		return false
	}
	
	return true
}

// verifySortedResponse is like test.SortAndCheck but ignores SOA serial numbers
func verifySortedResponse(resp *dns.Msg, tc test.Case) error {
	// Create a copy of the test case with normalized SOA records
	normalizedTC := tc
	normalizedTC.Answer = normalizeSOASerials(tc.Answer)
	normalizedTC.Ns = normalizeSOASerials(tc.Ns)
	normalizedTC.Extra = normalizeSOASerials(tc.Extra)
	
	// Normalize the response SOA records too
	respCopy := resp.Copy()
	respCopy.Answer = normalizeSOASerials(resp.Answer)
	respCopy.Ns = normalizeSOASerials(resp.Ns)
	respCopy.Extra = normalizeSOASerials(resp.Extra)
	
	return test.SortAndCheck(respCopy, normalizedTC)
}

// normalizeSOASerials sets all SOA serials to a fixed value for comparison
func normalizeSOASerials(rrs []dns.RR) []dns.RR {
	normalized := make([]dns.RR, len(rrs))
	for i, rr := range rrs {
		if soa, ok := rr.(*dns.SOA); ok {
			// Create a copy of the SOA record with serial set to 0
			soaCopy := &dns.SOA{
				Hdr:     soa.Hdr,
				Ns:      soa.Ns,
				Mbox:    soa.Mbox,
				Serial:  0, // Set to 0 for comparison
				Refresh: soa.Refresh,
				Retry:   soa.Retry,
				Expire:  soa.Expire,
				Minttl:  soa.Minttl,
			}
			normalized[i] = soaCopy
		} else {
			normalized[i] = rr
		}
	}
	return normalized
}

var testsApex = []test.Case{
	{
		Qname: "example.com.", Qtype: dns.TypeSOA,
		Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.SOA("example.com.  60  IN  SOA dns1.kube-system.example.com. hostmaster.example.com. 0 7200 1800 86400 5"),
		},
	},
	{
		Qname: "example.com.", Qtype: dns.TypeNS,
		Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.NS("example.com.   60  IN  NS  dns1.kube-system.example.com."),
		},
		Extra: []dns.RR{
			test.A("dns1.kube-system.example.com.   60  IN  A   127.0.0.1"),
		},
	},
	{
		Qname: "example.com.", Qtype: dns.TypeSRV,
		Rcode: dns.RcodeSuccess,
		Ns: []dns.RR{
			test.SOA("example.com.  60  IN  SOA dns1.kube-system.example.com. hostmaster.example.com. 0 7200 1800 86400 5"),
		},
	},
	{
		Qname: "example.com.", Qtype: dns.TypeA,
		Rcode: dns.RcodeSuccess,
		Ns: []dns.RR{
			test.SOA("example.com.  60  IN  SOA dns1.kube-system.example.com. hostmaster.example.com. 0 7200 1800 86400 5"),
		},
	},
	{
		Qname: "dns1.kube-system.example.com.", Qtype: dns.TypeSRV,
		Rcode: dns.RcodeSuccess,
		Ns: []dns.RR{
			test.SOA("example.com.  60  IN  SOA dns1.kube-system.example.com. hostmaster.example.com. 0 7200 1800 86400 5"),
		},
	},
	{
		Qname: "dns1.kube-system.example.com.", Qtype: dns.TypeNS,
		Rcode: dns.RcodeSuccess,
		Ns: []dns.RR{
			test.SOA("example.com.  60  IN  SOA dns1.kube-system.example.com. hostmaster.example.com. 0 7200 1800 86400 5"),
		},
	},
	{
		Qname: "dns1.kube-system.example.com.", Qtype: dns.TypeSOA,
		Rcode: dns.RcodeSuccess,
		Ns: []dns.RR{
			test.SOA("example.com.  60  IN  SOA dns1.kube-system.example.com. hostmaster.example.com. 0 7200 1800 86400 5"),
		},
	},
	{
		Qname: "dns1.kube-system.example.com.", Qtype: dns.TypeA,
		Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.A("dns1.kube-system.example.com.   60  IN  A   127.0.0.1"),
		},
	},
	{
		Qname: "dns1.kube-system.example.com.", Qtype: dns.TypeAAAA,
		Rcode: dns.RcodeSuccess,
		Ns: []dns.RR{
			test.SOA("example.com.  60  IN  SOA dns1.kube-system.example.com. hostmaster.example.com. 0 7200 1800 86400 5"),
		},
	},
	{
		Qname: "foo.dns1.kube-system.example.com.", Qtype: dns.TypeA,
		Rcode: dns.RcodeNameError,
		Ns: []dns.RR{
			test.SOA("example.com.  60  IN  SOA dns1.kube-system.example.com. hostmaster.example.com. 0 7200 1800 86400 5"),
		},
	},
}

func selfAddressTest(state request.Request) []dns.RR {
	a := test.A("dns1.kube-system.example.com. IN A 127.0.0.1")
	return []dns.RR{a}
}

func TestSOASerialDynamic(t *testing.T) {
	gw := newGateway()
	gw.Zones = []string{"example.com."}

	state := request.Request{Zone: "example.com."}

	// Get first SOA - should use initial serial since dirty=true
	soa1 := gw.soa(state)
	t.Logf("First SOA serial: %d", soa1.Serial)

	// Verify serial is reasonable (should be initialized)
	if soa1.Serial == 0 {
		t.Errorf("SOA serial should not be 0")
	}

	// Mark as dirty to force serial update
	gw.markDirty()

	// Get second SOA - should get a new serial because we marked dirty
	soa2 := gw.soa(state)
	t.Logf("Second SOA serial: %d", soa2.Serial)

	// Verify serial increased or stayed same (depending on timing)
	if soa2.Serial < soa1.Serial {
		t.Errorf("SOA serial should not decrease: first=%d, second=%d", soa1.Serial, soa2.Serial)
	}

	// Get third SOA without marking dirty - should return same serial
	soa3 := gw.soa(state)
	if soa3.Serial != soa2.Serial {
		t.Errorf("SOA serial should be cached when not dirty: second=%d, third=%d", soa2.Serial, soa3.Serial)
	}

	t.Logf("Serial caching works correctly")
}
