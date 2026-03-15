package main

import (
	"flag"
	"log"
	"net/http"
	"os"

	ts "github.com/k8s-gateway/k8s_gateway/internal/telemetry_server"
)

func main() {
	addr := flag.String("addr", ":8080", "TCP address to listen on")
	flag.Parse()

	srv := ts.NewServer(os.Stdout)
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	log.Printf("telemetry-server listening on %s", *addr)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
