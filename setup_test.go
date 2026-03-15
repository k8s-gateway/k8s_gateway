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

func TestSetupSentry(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		shouldErr   bool
		expectedDSN string
	}{
		{
			// When no sentry directive is given the default project DSN is used,
			// so error reporting is on out-of-the-box.
			name:        "no sentry directive uses default DSN",
			input:       `k8s_gateway example.org`,
			shouldErr:   false,
			expectedDSN: defaultSentryDSN,
		},
		{
			name: "sentry off disables reporting",
			input: `k8s_gateway example.org {
  sentry off
}`,
			shouldErr:   false,
			expectedDSN: "",
		},
		{
			name: "sentry custom DSN overrides default",
			input: `k8s_gateway example.org {
  sentry https://key@o0.ingest.sentry.io/123
}`,
			shouldErr:   false,
			expectedDSN: "https://key@o0.ingest.sentry.io/123",
		},
		{
			name: "sentry directive without argument returns error",
			input: `k8s_gateway example.org {
  sentry
}`,
			shouldErr:   true,
			expectedDSN: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := caddy.NewTestController("dns", tc.input)
			gw, err := parse(c)

			if tc.shouldErr {
				if err == nil {
					t.Errorf("expected error for input %q but got none", tc.input)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error for input %q: %v", tc.input, err)
			}

			if gw.sentryDSN != tc.expectedDSN {
				t.Errorf("expected sentryDSN %q, got %q", tc.expectedDSN, gw.sentryDSN)
			}
		})
	}
}
