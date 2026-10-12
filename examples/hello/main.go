// Command hello is a minimal app for trying Deployer. It reports which commit
// it was built from, so you can tell previews of different branches apart.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"message": "Hello from Deployer",
			"ref":     os.Getenv("DEPLOYER_REF"),
			"commit":  os.Getenv("DEPLOYER_COMMIT"),
			"url":     os.Getenv("DEPLOYER_URL"),
			"path":    r.URL.Path,
		})
	})
	// Listen on all interfaces: the gateway reaches the app over its network.
	server := &http.Server{Addr: "0.0.0.0:" + port, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("listening on %s", server.Addr)
	log.Fatal(server.ListenAndServe())
}
