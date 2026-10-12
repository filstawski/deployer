package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/filstawski/deployer/internal/domain"
)

const token = "dpl_control"

func echoApp(t *testing.T) string {
	t.Helper()
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			return
		}
		json.NewEncoder(w).Encode(map[string]string{
			"path": r.URL.RequestURI(), "host": r.Host,
			"proto": r.Header.Get("X-Forwarded-Proto"), "for": r.Header.Get("X-Forwarded-For"),
		})
	}))
	t.Cleanup(app.Close)
	return app.URL
}

func setup(t *testing.T) (pub, ctl *httptest.Server, now *time.Time) {
	t.Helper()
	clock := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	g := New(Options{ControlToken: token, PublicScheme: "https", Now: func() time.Time { return clock }})
	pub = httptest.NewServer(g.Public())
	ctl = httptest.NewServer(g.Control())
	t.Cleanup(pub.Close)
	t.Cleanup(ctl.Close)
	return pub, ctl, &clock
}

func get(t *testing.T, base, host, path, accept string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", base+path, nil)
	req.Host = host
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestRoutingAndStatusPages(t *testing.T) {
	pub, ctl, now := setup(t)

	// Before the first snapshot every host reports a temporary outage.
	if resp, _ := get(t, pub.URL, "ok.example.com", "/", "text/html"); resp.StatusCode != 503 {
		t.Fatalf("before snapshot: %d", resp.StatusCode)
	}

	client := NewClient(ctl.URL, token)
	expires := now.Add(time.Hour)
	upstream := echoApp(t)
	routes := []Route{
		{Host: "ok.example.com", State: domain.StateRunning, Upstream: upstream, HealthPath: "/health", ExpiresAt: &expires},
		{Host: "build.example.com", State: domain.StateBuilding},
		{Host: "off.example.com", State: domain.StateStopped, Upstream: upstream},
		{Host: "bad.example.com", State: domain.StateFailed},
	}
	if err := client.Publish(context.Background(), routes); err != nil {
		t.Fatal(err)
	}

	resp, body := get(t, pub.URL, "ok.example.com:8080", "/demo?x=1", "")
	var echoed map[string]string
	json.Unmarshal([]byte(body), &echoed)
	if resp.StatusCode != 200 || echoed["path"] != "/demo?x=1" || echoed["host"] != "ok.example.com:8080" || echoed["proto"] != "https" ||
		!strings.HasPrefix(echoed["for"], "203.0.113.9, ") || resp.Header.Get("X-Robots-Tag") != "noindex, nofollow" {
		t.Fatalf("proxy: %d %v %v", resp.StatusCode, echoed, resp.Header)
	}

	cases := []struct {
		host   string
		status int
		text   string
		poll   bool
	}{
		{"build.example.com", 503, "Preparing your preview", true},
		{"off.example.com", 503, "offline", false},
		{"bad.example.com", 503, "couldn&#39;t start", false},
		{"missing.example.com", 404, "Preview not found", false},
	}
	for _, c := range cases {
		resp, body := get(t, pub.URL, c.host, "/", "text/html,application/xhtml+xml")
		if resp.StatusCode != c.status || !strings.Contains(body, c.text) || strings.Contains(body, "wait.js") != c.poll {
			t.Errorf("%s: %d %q", c.host, resp.StatusCode, body)
		}
		if resp.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("%s: cacheable status page", c.host)
		}
		// API clients get JSON with the same status code.
		resp, body = get(t, pub.URL, c.host, "/api/items", "application/json")
		if resp.StatusCode != c.status || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
			t.Errorf("%s JSON: %d %s", c.host, resp.StatusCode, body)
		}
	}

	_, body = get(t, pub.URL, "build.example.com", "/_deployer/status", "")
	if !strings.Contains(body, `"ready":false`) {
		t.Fatalf("status: %s", body)
	}
	_, body = get(t, pub.URL, "ok.example.com", "/_deployer/status", "")
	if !strings.Contains(body, `"ready":true`) {
		t.Fatalf("status: %s", body)
	}

	// The gateway enforces expiry by itself.
	*now = expires
	if resp, body := get(t, pub.URL, "ok.example.com", "/", "text/html"); resp.StatusCode != 410 || !strings.Contains(body, "expired") {
		t.Fatalf("expired: %d %s", resp.StatusCode, body)
	}
}

func TestControlAPI(t *testing.T) {
	_, ctl, _ := setup(t)
	if err := NewClient(ctl.URL, "dpl_wrong").Healthy(context.Background()); err == nil {
		t.Fatal("wrong control token accepted")
	}
	client := NewClient(ctl.URL, token)
	if err := client.Healthy(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Route{
		{Host: "UPPER.example.com"},
		{Host: "x.example.com", Upstream: "https://evil"},
		{Host: "x.example.com", Upstream: "http://app:80/path"},
		{Host: "x.example.com", HealthPath: "//evil"},
	} {
		if err := client.Publish(context.Background(), []Route{bad}); err == nil {
			t.Errorf("invalid route accepted: %+v", bad)
		}
	}

	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "a.example.com" || r.URL.Path != "/health" {
			w.WriteHeader(500)
		}
	}))
	defer healthy.Close()
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer broken.Close()
	err := client.Publish(context.Background(), []Route{
		{Host: "a.example.com", State: domain.StateStarting, Upstream: healthy.URL, HealthPath: "/health"},
		{Host: "b.example.com", State: domain.StateStarting, Upstream: broken.URL, HealthPath: "/health"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Probe(context.Background(), "a.example.com"); err != nil {
		t.Fatalf("healthy probe: %v", err)
	}
	if err := client.Probe(context.Background(), "b.example.com"); err == nil || !strings.Contains(err.Error(), "HTTP") {
		t.Fatalf("failing probe: %v", err)
	}
	if err := client.Probe(context.Background(), "nope.example.com"); err == nil {
		t.Fatal("probe of unknown host succeeded")
	}
}
