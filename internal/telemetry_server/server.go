package telemetry_server

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

const (
	// maxBodyBytes is the maximum accepted request body size. Payloads from
	// the plugin are always well under 1 KiB; this limit prevents DoS via
	// large uploads.
	maxBodyBytes = 8 * 1024 // 8 KiB
)

// UsagePayload mirrors gateway.TelemetryData — the JSON body sent by the
// plugin. It is deliberately a separate type so the server has no import
// dependency on the plugin package.
type UsagePayload struct {
	PluginVersion     string   `json:"plugin_version"`
	KubernetesVersion string   `json:"kubernetes_version"`
	Resources         []string `json:"resources"`
	InCluster         bool     `json:"in_cluster"`
}

// UsageRecord is what the server writes to stdout: the received payload plus
// the server-assigned ingestion timestamp. No IP address or other PII is
// included.
type UsageRecord struct {
	ReceivedAt        time.Time `json:"received_at"`
	PluginVersion     string    `json:"plugin_version"`
	KubernetesVersion string    `json:"kubernetes_version"`
	Resources         []string  `json:"resources"`
	InCluster         bool      `json:"in_cluster"`
}

// Server holds the dependencies for the HTTP handlers.
type Server struct {
	out *json.Encoder // NDJSON output (usually os.Stdout)
}

// NewServer creates a Server that writes NDJSON records to out.
func NewServer(out io.Writer) *Server {
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	return &Server{out: enc}
}

// RegisterRoutes wires the handlers into mux.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/usage", s.handleUsage)
	mux.HandleFunc("GET /healthz", handleHealthz)
}

// handleUsage processes an incoming usage report.
func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	ct := r.Header.Get("Content-Type")
	if ct != "application/json" {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	var payload UsagePayload
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&payload); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	if payload.PluginVersion == "" {
		http.Error(w, "plugin_version is required", http.StatusBadRequest)
		return
	}

	record := UsageRecord{
		ReceivedAt:        time.Now().UTC(),
		PluginVersion:     payload.PluginVersion,
		KubernetesVersion: payload.KubernetesVersion,
		Resources:         payload.Resources,
		InCluster:         payload.InCluster,
	}

	if err := s.out.Encode(record); err != nil {
		log.Printf("error writing record: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

// handleHealthz is a simple liveness probe.
func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintln(w, "ok")
}
