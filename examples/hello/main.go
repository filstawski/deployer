package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"
)

// Revision can be set with -ldflags '-X main.Revision=<commit>' when building.
var Revision = "development"

func main() {
	if v := os.Getenv("DEPLOYER_COMMIT"); v != "" {
		Revision = v
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "Hello from Deployer", "revision": Revision, "path": r.URL.Path})
	})
	s := &http.Server{Addr: "0.0.0.0:" + port, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(s.ListenAndServe())
}
