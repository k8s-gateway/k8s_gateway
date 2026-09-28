package gateway

import (
	"strings"
	"testing"

	"github.com/coredns/caddy"
)

func TestSetup(t *testing.T) {
	tests := []struct {
		input         string
		shouldErr     bool
		expectedZone  string
		expectedZones int
	}{
		{`k8s_gateway`, false, "", 1},
		{`k8s_gateway example.org`, false, "example.org.", 1},
		{`k8s_gateway example.org sub.example.org`, false, "sub.example.org.", 2},
	}

	for i, test := range tests {
		c := caddy.NewTestController("dns", test.input)
		gw, err := parse(c)

		if test.shouldErr && err == nil {
			t.Errorf("Test %d: Expected error but found %s for input %s", i, err, test.input)
		}

		if err != nil {
			if !test.shouldErr {
				t.Errorf("Test %d: Expected no error but found one for input %s. Error was: %v", i, test.input, err)
			}
		}

		if !test.shouldErr && test.expectedZone != "" {
			if test.expectedZones != len(gw.Zones) {
				t.Errorf("Test %d, expected zone %q for input %s, got: %q", i, test.expectedZone, test.input, gw.Zones[0])
			}
		}
	}
}

func TestLoadBalancerAddressPreferenceParsing(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		shouldErr   bool
		expectedErr string
		expected    loadBalancerAddressPreference
	}{
		{
			name:     "hostname by default",
			input:    `k8s_gateway example.org`,
			expected: loadBalancerAddressPreferenceHostname,
		},
		{
			name: "hostname",
			input: `k8s_gateway example.org {
	loadBalancerAddressPreference hostname
}`,
			expected: loadBalancerAddressPreferenceHostname,
		},
		{
			name: "ip",
			input: `k8s_gateway example.org {
	loadBalancerAddressPreference ip
}`,
			expected: loadBalancerAddressPreferenceIP,
		},
		{
			name: "missing argument",
			input: `k8s_gateway example.org {
	loadBalancerAddressPreference
}`,
			shouldErr:   true,
			expectedErr: "requires exactly one argument",
		},
		{
			name: "extra argument",
			input: `k8s_gateway example.org {
	loadBalancerAddressPreference hostname ip
}`,
			shouldErr:   true,
			expectedErr: "requires exactly one argument",
		},
		{
			name: "invalid argument",
			input: `k8s_gateway example.org {
	loadBalancerAddressPreference IP
}`,
			shouldErr:   true,
			expectedErr: "must be 'hostname' or 'ip'",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gw, err := parse(caddy.NewTestController("dns", test.input))
			if test.shouldErr {
				if err == nil {
					t.Fatal("expected parse error")
				}
				if !strings.Contains(err.Error(), test.expectedErr) {
					t.Fatalf("expected parse error containing %q, got %v", test.expectedErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected parse error: %v", err)
			}
			if gw.loadBalancerAddressPreference != test.expected {
				t.Errorf("loadBalancerAddressPreference = %v, want %v", gw.loadBalancerAddressPreference, test.expected)
			}
		})
	}
}

func TestServiceLabelSelectorParsing(t *testing.T) {
	tests := []struct {
		input             string
		shouldErr         bool
		expectedErr       string
		expectedSelectors []string
	}{
		{
			input: `k8s_gateway example.org {
	serviceLabelSelectors "app=service1"
}`,
			shouldErr:         false,
			expectedSelectors: []string{"app=service1"},
		},
		{
			input: `k8s_gateway example.org {
	serviceLabelSelectors "app in (service1,service2)"
}`,
			shouldErr:         false,
			expectedSelectors: []string{"app in (service1,service2)"},
		},
		{
			input: `k8s_gateway example.org {
	serviceLabelSelectors "app=service1,tier!=cache"
}`,
			shouldErr:         false,
			expectedSelectors: []string{"app=service1,tier!=cache"},
		},
		{
			input: `k8s_gateway example.org {
	serviceLabelSelectors
}`,
			shouldErr:   true,
			expectedErr: "requires at least one argument",
		},
		{
			input: `k8s_gateway example.org {
	serviceLabelSelectors "!!!invalid"
}`,
			shouldErr:   true,
			expectedErr: "invalid serviceLabelSelectors",
		},
		{
			input: `k8s_gateway example.org {
	serviceLabelSelectors ""
}`,
			shouldErr:   true,
			expectedErr: "does not accept empty strings",
		},
		{
			input: `k8s_gateway example.org {
	serviceLabelSelectors "" "app=service1"
}`,
			shouldErr:   true,
			expectedErr: "does not accept empty strings",
		},
		{
			input: `k8s_gateway example.org {
	serviceLabelSelectors "app=service1" "app=service2"
}`,
			shouldErr:         false,
			expectedSelectors: []string{"app=service1", "app=service2"},
		},
		{
			input: `k8s_gateway example.org {
	serviceLabelSelectors "app = service1"
}`,
			shouldErr:         false,
			expectedSelectors: []string{"app=service1"},
		},
		{
			input: `k8s_gateway example.org {
	serviceLabelSelectors "app=service1,tier=frontend" "app=service2,tier=backend"
}`,
			shouldErr:         false,
			expectedSelectors: []string{"app=service1,tier=frontend", "app=service2,tier=backend"},
		},
	}

	for i, test := range tests {
		c := caddy.NewTestController("dns", test.input)
		gw, err := parse(c)

		if test.shouldErr {
			if err == nil {
				t.Errorf("Test %d: Expected error for input %s", i, test.input)
			} else if test.expectedErr != "" && !strings.Contains(err.Error(), test.expectedErr) {
				t.Errorf("Test %d: Expected error containing %q, got: %v", i, test.expectedErr, err)
			}
			continue
		}

		if err != nil {
			t.Errorf("Test %d: Unexpected error for input %s: %v", i, test.input, err)
			continue
		}

		if len(gw.resourceFilters.serviceLabelSelectors) != len(test.expectedSelectors) {
			t.Errorf("Test %d: Expected %d selectors, got %d: %v", i, len(test.expectedSelectors), len(gw.resourceFilters.serviceLabelSelectors), gw.resourceFilters.serviceLabelSelectors)
			continue
		}
		for j, expected := range test.expectedSelectors {
			if gw.resourceFilters.serviceLabelSelectors[j] != expected {
				t.Errorf("Test %d: Selector %d: expected %q, got %q", i, j, expected, gw.resourceFilters.serviceLabelSelectors[j])
			}
		}
	}
}
