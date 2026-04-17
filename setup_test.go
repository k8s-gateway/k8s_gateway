package gateway

import (
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

func TestServiceLabelSelectorParsing(t *testing.T) {
	tests := []struct {
		input             string
		shouldErr         bool
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
			shouldErr: true,
		},
		{
			input: `k8s_gateway example.org {
	serviceLabelSelectors "!!!invalid"
}`,
			shouldErr: true,
		},
		{
			input: `k8s_gateway example.org {
	serviceLabelSelectors ""
}`,
			shouldErr: true,
		},
		{
			input: `k8s_gateway example.org {
	serviceLabelSelectors "" "app=service1"
}`,
			shouldErr: true,
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
