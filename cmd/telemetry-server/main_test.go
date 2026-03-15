package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestServer creates a Server backed by an in-memory buffer and returns
// both the server and a pointer to the buffer so tests can inspect output.
func newTestServer() (*Server, *bytes.Buffer) {
	var buf bytes.Buffer
	return NewServer(&buf), &buf
}

// decodeRecord unmarshals the first NDJSON line written by the server into a
// UsageRecord, failing the test if that is not possible.
func decodeRecord(t *testing.T, buf *bytes.Buffer) UsageRecord {
	t.Helper()
	var r UsageRecord
	if err := json.Unmarshal(bytes.TrimRight(buf.Bytes(), "\n"), &r); err != nil {
		t.Fatalf("could not decode output record: %v\nraw: %s", err, buf.String())
	}
	return r
}

func TestHandleUsage_OK(t *testing.T) {
	srv, buf := newTestServer()
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	body := `{"plugin_version":"v0.4.0","kubernetes_version":"v1.28.3","resources":["Ingress","Service"],"in_cluster":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/usage", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Errorf("status: want %d, got %d", http.StatusAccepted, rr.Code)
	}

	before := time.Now().UTC()
	rec := decodeRecord(t, buf)
	after := time.Now().UTC()

	if rec.PluginVersion != "v0.4.0" {
		t.Errorf("PluginVersion: want %q, got %q", "v0.4.0", rec.PluginVersion)
	}
	if rec.KubernetesVersion != "v1.28.3" {
		t.Errorf("KubernetesVersion: want %q, got %q", "v1.28.3", rec.KubernetesVersion)
	}
	if len(rec.Resources) != 2 || rec.Resources[0] != "Ingress" || rec.Resources[1] != "Service" {
		t.Errorf("Resources: unexpected %v", rec.Resources)
	}
	if !rec.InCluster {
		t.Error("InCluster: want true, got false")
	}
	// ReceivedAt should be a sensible server-assigned time.
	if rec.ReceivedAt.Before(before.Add(-time.Second)) || rec.ReceivedAt.After(after.Add(time.Second)) {
		t.Errorf("ReceivedAt %v is outside expected range [%v, %v]", rec.ReceivedAt, before, after)
	}
}

func TestHandleUsage_WrongContentType(t *testing.T) {
	srv, _ := newTestServer()
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/v1/usage", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "text/plain")
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnsupportedMediaType {
		t.Errorf("status: want %d, got %d", http.StatusUnsupportedMediaType, rr.Code)
	}
}

func TestHandleUsage_MissingPluginVersion(t *testing.T) {
	srv, _ := newTestServer()
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	body := `{"kubernetes_version":"v1.28.3","resources":["Ingress"],"in_cluster":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/usage", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: want %d, got %d", http.StatusBadRequest, rr.Code)
	}
}

func TestHandleUsage_InvalidJSON(t *testing.T) {
	srv, _ := newTestServer()
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/v1/usage", strings.NewReader(`not json`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: want %d, got %d", http.StatusBadRequest, rr.Code)
	}
}

func TestHandleUsage_OversizedBody(t *testing.T) {
	srv, _ := newTestServer()
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	// Build a body larger than maxBodyBytes.
	large := `{"plugin_version":"v1","kubernetes_version":"` + strings.Repeat("x", maxBodyBytes) + `","resources":[],"in_cluster":false}`
	req := httptest.NewRequest(http.MethodPost, "/v1/usage", strings.NewReader(large))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: want %d, got %d", http.StatusBadRequest, rr.Code)
	}
}

func TestHandleUsage_UnknownFields(t *testing.T) {
	srv, _ := newTestServer()
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	body := `{"plugin_version":"v0.4.0","unknown_field":"surprise"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/usage", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: want %d, got %d (unknown fields should be rejected)", http.StatusBadRequest, rr.Code)
	}
}

func TestHandleUsage_NoIPInOutput(t *testing.T) {
	srv, buf := newTestServer()
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	body := `{"plugin_version":"v0.4.0","resources":[],"in_cluster":false}`
	req := httptest.NewRequest(http.MethodPost, "/v1/usage", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "203.0.113.42:12345" // simulated client IP
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("unexpected status %d", rr.Code)
	}
	// The output record must not contain the IP address in any form.
	if strings.Contains(buf.String(), "203.0.113.42") {
		t.Error("output record contains client IP address — PII must not be stored")
	}
}

func TestHealthz(t *testing.T) {
	srv, _ := newTestServer()
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: want %d, got %d", http.StatusOK, rr.Code)
	}
}
