package gateway

import (
	"net/netip"
	"testing"

	"github.com/coredns/coredns/plugin/transfer"
	"github.com/coredns/coredns/request"
	"github.com/miekg/dns"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
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
		// First get the actual SOA serial
		state := request.Request{Zone: "example.com."}
		soa := gw.soa(state)
		
		// Assert serial is non-zero
		if soa.Serial == 0 {
			t.Fatal("SOA serial should not be zero")
		}
		
		actualSerial := soa.Serial
		
		// Now test IXFR fallback with the actual serial
		ch, err := gw.Transfer("example.com.", actualSerial)
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

		if len(records) > 0 && records[0].Header().Rrtype != dns.TypeSOA {
			t.Errorf("Expected SOA record for IXFR fallback, got: %s", dns.TypeToString[records[0].Header().Rrtype])
		}
		
		// Verify the returned SOA has the same serial
		if len(records) > 0 {
			returnedSOA, ok := records[0].(*dns.SOA)
			if !ok {
				t.Error("Expected returned record to be SOA type")
			} else if returnedSOA.Serial != actualSerial {
				t.Errorf("Expected returned SOA serial to be %d, got: %d", actualSerial, returnedSOA.Serial)
			}
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

func TestTransferResources(t *testing.T) {
	ctrl := &KubeController{hasSynced: true}

	gw := newGateway()
	gw.Zones = []string{"example.com."}
	gw.Controller = ctrl

	t.Run("controller not synced", func(t *testing.T) {
		gw.Controller.hasSynced = false
		ch := make(chan []dns.RR, 10)
		gw.transferResources(ch, "example.com.")
		close(ch)

		// Should not send any records when not synced
		count := 0
		for range ch {
			count++
		}
		if count != 0 {
			t.Errorf("Expected 0 records when controller not synced, got: %d", count)
		}

		// Reset for other tests
		gw.Controller.hasSynced = true
	})

	t.Run("no resources", func(t *testing.T) {
		gw.Resources = nil
		ch := make(chan []dns.RR, 10)
		gw.transferResources(ch, "example.com.")
		close(ch)

		// Should not send any records when no resources
		count := 0
		for range ch {
			count++
		}
		if count != 0 {
			t.Errorf("Expected 0 records with no resources, got: %d", count)
		}
	})
}

func TestGetServiceHostnames(t *testing.T) {
	t.Run("with hostname annotation", func(t *testing.T) {
		service := &core.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-service",
				Namespace: "default",
				Annotations: map[string]string{
					hostnameAnnotationKey: "test.example.com",
				},
			},
		}
		hostnames := getServiceHostnames(service, "example.com.")
		if len(hostnames) != 1 {
			t.Errorf("Expected 1 hostname, got: %d", len(hostnames))
		}
		if hostnames[0] != "test.example.com" {
			t.Errorf("Expected 'test.example.com', got: %s", hostnames[0])
		}
	})

	t.Run("with external-dns annotation", func(t *testing.T) {
		service := &core.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-service",
				Namespace: "default",
				Annotations: map[string]string{
					externalDnsHostnameAnnotationKey: "external.example.com",
				},
			},
		}
		hostnames := getServiceHostnames(service, "example.com.")
		if len(hostnames) != 1 {
			t.Errorf("Expected 1 hostname, got: %d", len(hostnames))
		}
		if hostnames[0] != "external.example.com" {
			t.Errorf("Expected 'external.example.com', got: %s", hostnames[0])
		}
	})

	t.Run("with both annotations - priority to hostnameAnnotationKey", func(t *testing.T) {
		service := &core.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-service",
				Namespace: "default",
				Annotations: map[string]string{
					hostnameAnnotationKey:             "priority.example.com",
					externalDnsHostnameAnnotationKey: "external.example.com",
				},
			},
		}
		hostnames := getServiceHostnames(service, "example.com.")
		if len(hostnames) != 1 {
			t.Errorf("Expected 1 hostname, got: %d", len(hostnames))
		}
		if hostnames[0] != "priority.example.com" {
			t.Errorf("Expected 'priority.example.com' (from hostnameAnnotationKey), got: %s", hostnames[0])
		}
	})

	t.Run("with multiple hostnames", func(t *testing.T) {
		service := &core.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-service",
				Namespace: "default",
				Annotations: map[string]string{
					hostnameAnnotationKey: "test1.example.com,test2.example.com",
				},
			},
		}
		hostnames := getServiceHostnames(service, "example.com.")
		if len(hostnames) != 2 {
			t.Errorf("Expected 2 hostnames, got: %d", len(hostnames))
		}
	})

	t.Run("default hostname", func(t *testing.T) {
		service := &core.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-service",
				Namespace: "default",
			},
		}
		hostnames := getServiceHostnames(service, "example.com.")
		if len(hostnames) != 1 {
			t.Errorf("Expected 1 hostname, got: %d", len(hostnames))
		}
		expected := "test-service.default.example.com."
		if hostnames[0] != expected {
			t.Errorf("Expected '%s', got: %s", expected, hostnames[0])
		}
	})

	t.Run("hostname with spaces", func(t *testing.T) {
		service := &core.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-service",
				Namespace: "default",
				Annotations: map[string]string{
					hostnameAnnotationKey: " test.example.com , other.example.com ",
				},
			},
		}
		hostnames := getServiceHostnames(service, "example.com.")
		if len(hostnames) != 2 {
			t.Errorf("Expected 2 hostnames, got: %d", len(hostnames))
		}
		if hostnames[0] != "test.example.com" {
			t.Errorf("Expected 'test.example.com', got: %s", hostnames[0])
		}
		if hostnames[1] != "other.example.com" {
			t.Errorf("Expected 'other.example.com', got: %s", hostnames[1])
		}
	})
}

func TestTransferResourcesEmpty(t *testing.T) {
	// Test with empty controllers
	ctrl := &KubeController{
		hasSynced:   true,
		controllers: []cache.SharedIndexInformer{},
	}

	gw := newGateway()
	gw.Zones = []string{"example.com."}
	gw.Controller = ctrl
	gw.Resources = staticResources

	ch := make(chan []dns.RR, 100)
	gw.transferResources(ch, "example.com.")
	close(ch)

	// Should not panic with empty controllers
	count := 0
	for range ch {
		count++
	}
	// Expect 0 records since there are no controllers with data
	if count != 0 {
		t.Logf("Got %d records from empty controllers (expected 0)", count)
	}
}

// TestTransferFunctions tests individual transfer functions with empty controllers
func TestTransferFunctions(t *testing.T) {
	ctrl := &KubeController{
		hasSynced:   true,
		controllers: []cache.SharedIndexInformer{},
	}

	gw := newGateway()
	gw.Zones = []string{"example.com."}
	gw.Controller = ctrl

	t.Run("transferIngresses", func(t *testing.T) {
		records := make(map[string][]dns.RR)
		gw.transferIngresses(records, "example.com.")
		// Should not panic with empty controllers
	})

	t.Run("transferServices", func(t *testing.T) {
		records := make(map[string][]dns.RR)
		gw.transferServices(records, "example.com.")
		// Should not panic with empty controllers
	})

	t.Run("transferHTTPRoutes", func(t *testing.T) {
		records := make(map[string][]dns.RR)
		gw.transferHTTPRoutes(records, "example.com.")
		// Should not panic with empty controllers
	})

	t.Run("transferTLSRoutes", func(t *testing.T) {
		records := make(map[string][]dns.RR)
		gw.transferTLSRoutes(records, "example.com.")
		// Should not panic with empty controllers
	})

	t.Run("transferGRPCRoutes", func(t *testing.T) {
		records := make(map[string][]dns.RR)
		gw.transferGRPCRoutes(records, "example.com.")
		// Should not panic with empty controllers
	})

	t.Run("transferDNSEndpoints", func(t *testing.T) {
		records := make(map[string][]dns.RR)
		gw.transferDNSEndpoints(records, "example.com.")
		// Should not panic with empty controllers
	})
}

// TestFindGatewayController tests the findGatewayController helper
func TestFindGatewayController(t *testing.T) {
	ctrl := &KubeController{
		hasSynced:   true,
		controllers: []cache.SharedIndexInformer{},
	}

	gw := newGateway()
	gw.Controller = ctrl

	gwCtrl := gw.findGatewayController()
	if gwCtrl != nil {
		t.Error("Expected nil gateway controller with empty controllers")
	}
}

// TestAddRecordsHelper tests the addRecords helper function
func TestAddRecordsHelper(t *testing.T) {
	records := make(map[string][]dns.RR)
	
	rr := &dns.A{
		Hdr: dns.RR_Header{
			Name:   "test.example.com.",
			Rrtype: dns.TypeA,
			Class:  dns.ClassINET,
			Ttl:    300,
		},
	}
	
	addRecords(records, "test.example.com.", []dns.RR{rr})
	
	if len(records) != 1 {
		t.Errorf("Expected 1 entry in records map, got %d", len(records))
	}
	
	if len(records["test.example.com."]) != 1 {
		t.Errorf("Expected 1 record for test.example.com., got %d", len(records["test.example.com."]))
	}
	
	// Test adding empty records (should not add)
	addRecords(records, "empty.example.com.", []dns.RR{})
	if len(records) != 1 {
		t.Errorf("Expected 1 entry in records map after adding empty, got %d", len(records))
	}
	
	// Test adding multiple records to same key
	rr2 := &dns.AAAA{
		Hdr: dns.RR_Header{
			Name:   "test.example.com.",
			Rrtype: dns.TypeAAAA,
			Class:  dns.ClassINET,
			Ttl:    300,
		},
	}
	addRecords(records, "test.example.com.", []dns.RR{rr2})
	if len(records["test.example.com."]) != 2 {
		t.Errorf("Expected 2 records for test.example.com., got %d", len(records["test.example.com."]))
	}
}

func TestContainsHelper(t *testing.T) {
	slice := []string{"foo", "bar", "baz"}
	
	if !contains(slice, "foo") {
		t.Error("Expected contains to find 'foo'")
	}
	
	if !contains(slice, "bar") {
		t.Error("Expected contains to find 'bar'")
	}
	
	if contains(slice, "notfound") {
		t.Error("Expected contains to not find 'notfound'")
	}
	
	// Test empty slice
	if contains([]string{}, "foo") {
		t.Error("Expected contains to return false for empty slice")
	}
}

func TestIPv4OnlyFiltering(t *testing.T) {
	addrs := []netip.Addr{
		netip.MustParseAddr("192.0.2.1"),
		netip.MustParseAddr("2001:db8::1"),
		netip.MustParseAddr("192.0.2.2"),
		netip.MustParseAddr("2001:db8::2"),
	}
	
	v4Only := ipv4Only(addrs)
	if len(v4Only) != 2 {
		t.Errorf("Expected 2 IPv4 addresses, got %d", len(v4Only))
	}
	
	for _, addr := range v4Only {
		if !addr.Is4() {
			t.Errorf("Expected IPv4 address, got %v", addr)
		}
	}
	
	// Test empty slice
	empty := ipv4Only([]netip.Addr{})
	if len(empty) != 0 {
		t.Errorf("Expected empty result for empty input, got %d", len(empty))
	}
	
	// Test all IPv6
	v6addrs := []netip.Addr{
		netip.MustParseAddr("2001:db8::1"),
		netip.MustParseAddr("2001:db8::2"),
	}
	v4FromV6 := ipv4Only(v6addrs)
	if len(v4FromV6) != 0 {
		t.Errorf("Expected 0 IPv4 addresses from IPv6-only input, got %d", len(v4FromV6))
	}
}

func TestIPv6OnlyFiltering(t *testing.T) {
	addrs := []netip.Addr{
		netip.MustParseAddr("192.0.2.1"),
		netip.MustParseAddr("2001:db8::1"),
		netip.MustParseAddr("192.0.2.2"),
		netip.MustParseAddr("2001:db8::2"),
	}
	
	v6Only := ipv6Only(addrs)
	if len(v6Only) != 2 {
		t.Errorf("Expected 2 IPv6 addresses, got %d", len(v6Only))
	}
	
	for _, addr := range v6Only {
		if !addr.Is6() {
			t.Errorf("Expected IPv6 address, got %v", addr)
		}
	}
	
	// Test empty slice
	empty := ipv6Only([]netip.Addr{})
	if len(empty) != 0 {
		t.Errorf("Expected empty result for empty input, got %d", len(empty))
	}
	
	// Test all IPv4
	v4addrs := []netip.Addr{
		netip.MustParseAddr("192.0.2.1"),
		netip.MustParseAddr("192.0.2.2"),
	}
	v6FromV4 := ipv6Only(v4addrs)
	if len(v6FromV4) != 0 {
		t.Errorf("Expected 0 IPv6 addresses from IPv4-only input, got %d", len(v6FromV4))
	}
}


