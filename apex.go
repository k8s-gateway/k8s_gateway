package gateway

import (
	"time"

	"github.com/coredns/coredns/plugin/pkg/dnsutil"
	"github.com/coredns/coredns/request"

	"github.com/miekg/dns"
)

// serveSubApex serves requests that hit the zones fake 'dns' subdomain where our nameservers live.
func (gw *Gateway) serveSubApex(state request.Request) (int, error) {
	base, _ := dnsutil.TrimZone(state.Name(), state.Zone)

	m := new(dns.Msg)
	m.SetReply(state.Req)
	m.Authoritative = true

	// base is gw.apex, if it's longer return nxdomain
	switch labels := dns.CountLabel(base); labels {
	default:
		m.SetRcode(m, dns.RcodeNameError)
		m.Ns = []dns.RR{gw.soa(state)}
		if err := state.W.WriteMsg(m); err != nil {
			log.Errorf("Failed to send a response: %s", err)
		}
		return 0, nil
	case 2:
		if base != gw.apex {
			// nxdomain
			m.SetRcode(m, dns.RcodeNameError)
			m.Ns = []dns.RR{gw.soa(state)}
			if err := state.W.WriteMsg(m); err != nil {
				log.Errorf("Failed to send a response: %s", err)
			}
			return 0, nil
		}

		addr := gw.ExternalAddrFunc(state)
		for _, rr := range addr {
			rr.Header().Ttl = gw.ttlSOA
			rr.Header().Name = state.QName()
			switch state.QType() {
			case dns.TypeA:
				if rr.Header().Rrtype == dns.TypeA {
					m.Answer = append(m.Answer, rr)
				}
			case dns.TypeAAAA:
				if rr.Header().Rrtype == dns.TypeAAAA {
					m.Answer = append(m.Answer, rr)
				}
			}
		}

		if len(m.Answer) == 0 {
			m.Ns = []dns.RR{gw.soa(state)}
		}

		if err := state.W.WriteMsg(m); err != nil {
			log.Errorf("Failed to send a response: %s", err)
		}
		return 0, nil

	}
}

func (gw *Gateway) soa(state request.Request) *dns.SOA {
	header := dns.RR_Header{Name: state.Zone, Rrtype: dns.TypeSOA, Ttl: gw.ttlSOA, Class: dns.ClassINET}

	soa := &dns.SOA{Hdr: header,
		Mbox:    dnsutil.Join(gw.hostmaster, gw.apex, state.Zone),
		Ns:      dnsutil.Join(gw.apex, state.Zone),
		Serial:  gw.calculateSerial(), // Dynamic serial based on current time
		Refresh: gw.soaRefresh,
		Retry:   gw.soaRetry,
		Expire:  gw.soaExpire,
		Minttl:  gw.ttlSOA,
	}
	return soa
}

// calculateSerial returns a content-driven SOA serial number.
// The serial only changes when the dirty flag is set, indicating that
// underlying DNS records have changed. This prevents unnecessary serial
// increments and makes zone transfers more efficient.
func (gw *Gateway) calculateSerial() uint32 {
	gw.serialMutex.Lock()
	defer gw.serialMutex.Unlock()

	if gw.dirty {
		// Content has changed, generate a new serial
		// Use Unix timestamp to ensure monotonic increase
		newSerial := uint32(time.Now().Unix())
		// Ensure serial always increases
		if newSerial <= gw.lastSerial {
			newSerial = gw.lastSerial + 1
		}
		gw.lastSerial = newSerial
		gw.dirty = false
	}

	return gw.lastSerial
}

func (gw *Gateway) nameservers(state request.Request) (result []dns.RR) {
	primaryNS := gw.ns1(state)
	result = append(result, primaryNS)

	secondaryNS := gw.ns2(state)
	if secondaryNS != nil {
		result = append(result, secondaryNS)
	}

	return result
}

func (gw *Gateway) ns1(state request.Request) *dns.NS {
	header := dns.RR_Header{Name: state.Zone, Rrtype: dns.TypeNS, Ttl: gw.ttlSOA, Class: dns.ClassINET}
	ns := &dns.NS{Hdr: header, Ns: dnsutil.Join(gw.apex, state.Zone)}

	return ns
}

func (gw *Gateway) ns2(state request.Request) *dns.NS {
	if gw.secondNS == "" { // If second NS is undefined, return nothing
		return nil
	}
	header := dns.RR_Header{Name: state.Zone, Rrtype: dns.TypeNS, Ttl: gw.ttlSOA, Class: dns.ClassINET}
	ns := &dns.NS{Hdr: header, Ns: dnsutil.Join(gw.secondNS, state.Zone)}

	return ns
}
