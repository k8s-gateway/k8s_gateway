package gateway

import (
	"net/netip"
	"testing"

	"github.com/coredns/coredns/plugin/transfer"
	"github.com/miekg/dns"
)

// parseAddrs is a helper function for tests
func parseAddrs(strs []string) []netip.Addr {
	var addrs []netip.Addr
	for _, s := range strs {
		addr, err := netip.ParseAddr(s)
		if err == nil {
			addrs = append(addrs, addr)
		}
	}
	return addrs
}

func TestTransfer(t *testing.T) {
	ctrl := &KubeController{hasSynced: true}

	gw := newGateway()
	gw.Zones = []string{"example.com."}
	gw.ExternalAddrFunc = gw.SelfAddress
	gw.Controller = ctrl

	// Test with matching zone
	t.Run("matching zone", func(t *testing.T) {
		ch, err := gw.Transfer("example.com.", 0)
		if err != nil {
			t.Errorf("Expected no error for matching zone, got: %v", err)
		}
		if ch == nil {
			t.Fatal("Expected channel, got nil")
		}

		// Collect all records
		var records []dns.RR
		for rrs := range ch {
			records = append(records, rrs...)
		}

		// Should have at least SOA records (start and end)
		if len(records) < 2 {
			t.Errorf("Expected at least 2 records (SOA start and end), got: %d", len(records))
		}

		// First and last should be SOA
		if records[0].Header().Rrtype != dns.TypeSOA {
			t.Errorf("Expected first record to be SOA, got: %s", dns.TypeToString[records[0].Header().Rrtype])
		}
		if records[len(records)-1].Header().Rrtype != dns.TypeSOA {
			t.Errorf("Expected last record to be SOA, got: %s", dns.TypeToString[records[len(records)-1].Header().Rrtype])
		}

		// Should have NS records
		hasNS := false
		for _, rr := range records {
			if rr.Header().Rrtype == dns.TypeNS {
				hasNS = true
				break
			}
		}
		if !hasNS {
			t.Error("Expected at least one NS record")
		}
	})

	// Test with non-matching zone
	t.Run("non-matching zone", func(t *testing.T) {
		ch, err := gw.Transfer("other.com.", 0)
		if err != transfer.ErrNotAuthoritative {
			t.Errorf("Expected ErrNotAuthoritative for non-matching zone, got: %v", err)
		}
		if ch != nil {
			t.Error("Expected nil channel for non-matching zone")
		}
	})

	// Test IXFR fallback (same serial)
	t.Run("ixfr fallback", func(t *testing.T) {
		ch, err := gw.Transfer("example.com.", 12345)
		if err != nil {
			t.Errorf("Expected no error for IXFR fallback, got: %v", err)
		}
		if ch == nil {
			t.Fatal("Expected channel, got nil")
		}

		// Collect all records
		var records []dns.RR
		for rrs := range ch {
			records = append(records, rrs...)
		}

		// Should only have one SOA record for IXFR fallback
		if len(records) != 1 {
			t.Errorf("Expected exactly 1 record (SOA) for IXFR fallback, got: %d", len(records))
		}

		if records[0].Header().Rrtype != dns.TypeSOA {
			t.Errorf("Expected SOA record for IXFR fallback, got: %s", dns.TypeToString[records[0].Header().Rrtype])
		}
	})
}

func TestTransferHelpers(t *testing.T) {
	t.Run("ipv4Only", func(t *testing.T) {
		addrs := parseAddrs([]string{"192.0.2.1", "2001:db8::1", "198.51.100.1"})
		ipv4 := ipv4Only(addrs)
		if len(ipv4) != 2 {
			t.Errorf("Expected 2 IPv4 addresses, got: %d", len(ipv4))
		}
		for _, addr := range ipv4 {
			if !addr.Is4() {
				t.Errorf("Expected IPv4 address, got: %v", addr)
			}
		}
	})

	t.Run("ipv6Only", func(t *testing.T) {
		addrs := parseAddrs([]string{"192.0.2.1", "2001:db8::1", "198.51.100.1"})
		ipv6 := ipv6Only(addrs)
		if len(ipv6) != 1 {
			t.Errorf("Expected 1 IPv6 address, got: %d", len(ipv6))
		}
		for _, addr := range ipv6 {
			if !addr.Is6() {
				t.Errorf("Expected IPv6 address, got: %v", addr)
			}
		}
	})

	t.Run("contains", func(t *testing.T) {
		slice := []string{"foo", "bar", "baz"}
		if !contains(slice, "bar") {
			t.Error("Expected contains to return true for 'bar'")
		}
		if contains(slice, "qux") {
			t.Error("Expected contains to return false for 'qux'")
		}
	})

	t.Run("addRecords", func(t *testing.T) {
		records := make(map[string][]dns.RR)
		rrs := []dns.RR{
			&dns.A{
				Hdr: dns.RR_Header{Name: "test.example.com.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
				A:   []byte{192, 0, 2, 1},
			},
		}
		addRecords(records, "test.example.com.", rrs)
		if len(records["test.example.com."]) != 1 {
			t.Errorf("Expected 1 record, got: %d", len(records["test.example.com."]))
		}

		// Add more records to same key
		addRecords(records, "test.example.com.", rrs)
		if len(records["test.example.com."]) != 2 {
			t.Errorf("Expected 2 records, got: %d", len(records["test.example.com."]))
		}
	})
}
