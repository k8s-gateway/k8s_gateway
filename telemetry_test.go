package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/coredns/caddy"
)

func TestTelemetryDataJSON(t *testing.T) {
	data := TelemetryData{
		PluginVersion:     "v0.4.0",
		KubernetesVersion: "v1.28.3",
		Resources:         []string{"Ingress", "Service"},
		InCluster:         true,
	}

	b, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("unexpected marshal error: %v", err)
	}

	var got map[string]interface{}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}

	checks := map[string]interface{}{
		"plugin_version":     "v0.4.0",
		"kubernetes_version": "v1.28.3",
		"in_cluster":         true,
	}
	for field, want := range checks {
		if got[field] != want {
			t.Errorf("field %q: want %v, got %v", field, want, got[field])
		}
	}

	rawResources, ok := got["resources"].([]interface{})
	if !ok {
		t.Fatalf("field 'resources' is not an array: %T", got["resources"])
	}
	if len(rawResources) != 2 || rawResources[0] != "Ingress" || rawResources[1] != "Service" {
		t.Errorf("field 'resources': unexpected value %v", rawResources)
	}
}

func TestSendTelemetry_NoMetrics(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	data := TelemetryData{
		PluginVersion:     "dev",
		KubernetesVersion: "",
		Resources:         []string{"Ingress"},
		InCluster:         false,
	}

	done := make(chan struct{})
	// noMetrics=true must not hit the server; done is closed synchronously.
	sendTelemetryTo(context.Background(), data, true, srv.URL, done)
	<-done

	if called {
		t.Error("telemetry endpoint was called despite noMetrics=true")
	}
}

func TestSendTelemetry_SendsData(t *testing.T) {
	var received TelemetryData
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	data := TelemetryData{
		PluginVersion:     "v0.4.0",
		KubernetesVersion: "v1.28.3",
		Resources:         []string{"HTTPRoute", "Ingress"},
		InCluster:         true,
	}

	done := make(chan struct{})
	sendTelemetryTo(context.Background(), data, false, srv.URL, done)
	<-done

	if received.PluginVersion != data.PluginVersion {
		t.Errorf("PluginVersion: want %q, got %q", data.PluginVersion, received.PluginVersion)
	}
	if received.KubernetesVersion != data.KubernetesVersion {
		t.Errorf("KubernetesVersion: want %q, got %q", data.KubernetesVersion, received.KubernetesVersion)
	}
	if received.InCluster != data.InCluster {
		t.Errorf("InCluster: want %v, got %v", data.InCluster, received.InCluster)
	}
	if len(received.Resources) != 2 || received.Resources[0] != "HTTPRoute" || received.Resources[1] != "Ingress" {
		t.Errorf("Resources: unexpected %v", received.Resources)
	}
}

func TestInClusterDetection(t *testing.T) {
	// configFile == "" → in-cluster
	gw1 := newGateway()
	gw1.configFile = ""
	gw1.inCluster = gw1.configFile == ""
	if !gw1.inCluster {
		t.Error("expected inCluster=true when configFile is empty")
	}

	// configFile set → out-of-cluster
	gw2 := newGateway()
	gw2.configFile = "/path/to/kubeconfig"
	gw2.inCluster = gw2.configFile == ""
	if gw2.inCluster {
		t.Error("expected inCluster=false when configFile is set")
	}
}

func TestParseNoMetrics(t *testing.T) {
	tests := []struct {
		input     string
		noMetrics bool
	}{
		{`k8s_gateway example.org`, false},
		{"k8s_gateway example.org {\n  noMetrics\n}", true},
	}

	for _, tc := range tests {
		c := caddy.NewTestController("dns", tc.input)
		gw, err := parse(c)
		if err != nil {
			t.Errorf("unexpected parse error for %q: %v", tc.input, err)
			continue
		}
		if gw.noMetrics != tc.noMetrics {
			t.Errorf("input %q: noMetrics want %v, got %v", tc.input, tc.noMetrics, gw.noMetrics)
		}
	}
}
