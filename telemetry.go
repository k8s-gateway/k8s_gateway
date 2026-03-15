package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"
)

const (
	// TelemetryEndpoint is the URL where anonymous usage data is sent.
	// See the "Telemetry" section of the README for the full list of fields
	// that are collected and how to opt out.
	TelemetryEndpoint = "https://telemetry.k8s-gw.skylab.fi/api/v1/usage"

	telemetryTimeout = 10 * time.Second
)

// PluginVersion is the version of this plugin. It defaults to "dev" and is
// overridden at build time via -ldflags in cmd/coredns.go.
var PluginVersion = "dev"

// TelemetryData holds anonymous, non-PII usage information that is reported
// once at startup. No personally identifiable information is collected.
type TelemetryData struct {
	// PluginVersion is the semver tag of k8s_gateway (e.g. "v0.4.0").
	PluginVersion string `json:"plugin_version"`
	// KubernetesVersion is the GitVersion returned by the API-server
	// (e.g. "v1.28.3"). Empty when the version cannot be determined.
	KubernetesVersion string `json:"kubernetes_version"`
	// Resources is the list of Kubernetes resource types that are enabled
	// in the Corefile (e.g. ["Ingress","Service"]).
	Resources []string `json:"resources"`
	// InCluster is true when the plugin is running inside a Kubernetes Pod
	// (using in-cluster config) and false when a kubeconfig file is used.
	InCluster bool `json:"in_cluster"`
}

// sendTelemetry logs telemetry data and, unless noMetrics is true, sends it
// asynchronously to TelemetryEndpoint. Errors are logged as warnings and
// never affect plugin operation.
func sendTelemetry(ctx context.Context, data TelemetryData, noMetrics bool) {
	sendTelemetryTo(ctx, data, noMetrics, TelemetryEndpoint, nil)
}

// sendTelemetryTo is the internal implementation used by sendTelemetry and
// tests. When done is non-nil it is closed once the async work completes (or
// is skipped). endpoint is the URL to POST the JSON payload to.
func sendTelemetryTo(ctx context.Context, data TelemetryData, noMetrics bool, endpoint string, done chan<- struct{}) {
	log.Infof("telemetry: plugin_version=%q kubernetes_version=%q resources=%v in_cluster=%v",
		data.PluginVersion, data.KubernetesVersion, data.Resources, data.InCluster)

	if noMetrics {
		log.Infof("telemetry: reporting disabled via 'noMetrics' option")
		if done != nil {
			close(done)
		}
		return
	}

	go func() {
		if done != nil {
			defer close(done)
		}

		tctx, cancel := context.WithTimeout(ctx, telemetryTimeout)
		defer cancel()

		payload, err := json.Marshal(data)
		if err != nil {
			log.Warningf("telemetry: failed to marshal data: %v", err)
			return
		}

		req, err := http.NewRequestWithContext(tctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		if err != nil {
			log.Warningf("telemetry: failed to build request: %v", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := (&http.Client{
			// DisableKeepAlives because this is a one-shot startup request;
			// no connection pooling is needed. Timeout is handled by tctx.
			Transport: &http.Transport{DisableKeepAlives: true},
		}).Do(req)
		if err != nil {
			log.Warningf("telemetry: failed to send data: %v", err)
			return
		}
		defer resp.Body.Close()

		log.Debugf("telemetry: response status=%d", resp.StatusCode)
	}()
}
