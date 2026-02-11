package gateway

import (
	"testing"

	"github.com/coredns/caddy"
)

func TestSOAConfig(t *testing.T) {
	tests := []struct {
		name        string
		config      string
		wantRefresh uint32
		wantRetry   uint32
		wantExpire  uint32
		wantErr     bool
	}{
		{
			name: "default values",
			config: `k8s_gateway example.com {
			}`,
			wantRefresh: defaultSOARefresh,
			wantRetry:   defaultSOARetry,
			wantExpire:  defaultSOAExpire,
			wantErr:     false,
		},
		{
			name: "custom SOA values",
			config: `k8s_gateway example.com {
				soa 3600 900 604800
			}`,
			wantRefresh: 3600,
			wantRetry:   900,
			wantExpire:  604800,
			wantErr:     false,
		},
		{
			name: "invalid SOA - missing arguments",
			config: `k8s_gateway example.com {
				soa 3600 900
			}`,
			wantErr: true,
		},
		{
			name: "invalid SOA - non-numeric value",
			config: `k8s_gateway example.com {
				soa 3600 invalid 604800
			}`,
			wantErr: true,
		},
		{
			name: "invalid SOA - negative value",
			config: `k8s_gateway example.com {
				soa -1 900 604800
			}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := caddy.NewTestController("dns", tt.config)
			gw, err := parse(c)
			
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				return
			}
			
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			
			if gw.soaRefresh != tt.wantRefresh {
				t.Errorf("soaRefresh = %d, want %d", gw.soaRefresh, tt.wantRefresh)
			}
			if gw.soaRetry != tt.wantRetry {
				t.Errorf("soaRetry = %d, want %d", gw.soaRetry, tt.wantRetry)
			}
			if gw.soaExpire != tt.wantExpire {
				t.Errorf("soaExpire = %d, want %d", gw.soaExpire, tt.wantExpire)
			}
		})
	}
}
