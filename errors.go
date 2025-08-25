package gateway

import (
	"fmt"
	"net/netip"

	"github.com/coredns/coredns/plugin"
	"github.com/miekg/dns"
)

// DNSError represents a DNS-specific error with proper response codes
type DNSError struct {
	Rcode   int
	Message string
	Err     error
}

func (e DNSError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("DNS error (%s): %s: %v", dns.RcodeToString[e.Rcode], e.Message, e.Err)
	}
	return fmt.Sprintf("DNS error (%s): %s", dns.RcodeToString[e.Rcode], e.Message)
}

// NewDNSError creates a new DNS error with proper response code
func NewDNSError(rcode int, message string, err error) DNSError {
	return DNSError{
		Rcode:   rcode,
		Message: message,
		Err:     err,
	}
}

// PluginError wraps errors for CoreDNS plugin framework
func PluginError(err error) error {
	return plugin.Error(thisPlugin, err)
}

// LogAndReturnError logs an error and returns it properly formatted
func LogAndReturnError(message string, err error) error {
	wrappedErr := fmt.Errorf("%s: %w", message, err)
	log.Errorf("%v", wrappedErr)
	return wrappedErr
}

// LogAndContinue logs an error but allows execution to continue
func LogAndContinue(message string, err error) {
	log.Warningf("%s: %v", message, err)
}

// WrapPluginError wraps an error for the plugin framework with context
func WrapPluginError(context string, err error) error {
	return plugin.Error(thisPlugin, fmt.Errorf("%s: %w", context, err))
}

// ParseIPSafely parses an IP address and logs errors for debugging
func ParseIPSafely(ipStr, source string) (addr netip.Addr, valid bool) {
	addr, err := netip.ParseAddr(ipStr)
	if err != nil {
		log.Debugf("Invalid IP address '%s' from %s: %v", ipStr, source, err)
		return netip.Addr{}, false
	}
	return addr, true
}
