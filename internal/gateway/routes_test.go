package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestVisitorStatesAndControlAuthentication(t *testing.T) {
	g := New("private")
	now := time.Now()
	g.Now = func() time.Time { return now }
	route := Route{Hostname: "demo.localhost", State: "building", Deadline: now.Add(time.Minute)}
	publish := func(token string) int {
		b, _ := json.Marshal([]Route{route})
		r := httptest.NewRequest("PUT", "/routes", bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		g.Control().ServeHTTP(w, r)
		return w.Code
	}
	if code := publish("wrong"); code != 401 {
		t.Fatalf("unauthorized update %d", code)
	}
	if code := publish("private"); code != 204 {
		t.Fatal(code)
	}
	visit := func(method, path, accept string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://demo.localhost"+path, nil)
		r.Header.Set("Accept", accept)
		w := httptest.NewRecorder()
		g.Public().ServeHTTP(w, r)
		return w
	}
	w := visit("GET", "/customer/demo?view=1", "text/html")
	if w.Code != 503 || !strings.Contains(w.Body.String(), "wait.js") || w.Header().Get("Retry-After") == "" {
		t.Fatalf("waiting page %d %s", w.Code, w.Body)
	}
	w = visit("POST", "/checkout", "text/html")
	if w.Code != 503 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") || strings.Contains(w.Body.String(), "wait.js") {
		t.Fatalf("POST should never get reload HTML: %s", w.Body)
	}
	now = now.Add(2 * time.Minute)
	w = visit("GET", "/", "text/html")
	if w.Code != 503 || strings.Contains(w.Body.String(), "wait.js") || !strings.Contains(w.Body.String(), "couldn") {
		t.Fatal("expired build deadline spins forever")
	}
	route.State = "running"
	publish("private")
	w = visit("GET", "/", "text/html")
	if w.Code != 410 || strings.Contains(w.Body.String(), "wait.js") {
		t.Fatal("expired lease did not close independently of controller")
	}
	route.State = "failed"
	route.Deadline = now.Add(time.Minute)
	publish("private")
	w = visit("GET", "/", "text/html")
	if strings.Contains(w.Body.String(), "wait.js") {
		t.Fatal("failed preview must stop polling")
	}
}
func TestProxyPreservesRequestAndDoesNotForwardSpoofedHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "demo.localhost" || r.URL.RequestURI() != "/nested?q=test" || r.Header.Get("X-Forwarded-For") == "spoofed" {
			t.Errorf("unexpected proxy request: %s %s %s", r.Host, r.URL, r.Header.Get("X-Forwarded-For"))
		}
		b, _ := io.ReadAll(r.Body)
		w.Write(b)
	}))
	defer upstream.Close()
	g := New("private")
	g.routes["demo.localhost"] = Route{Hostname: "demo.localhost", State: "running", Upstream: upstream.URL, Deadline: time.Now().Add(time.Minute)}
	r := httptest.NewRequest("POST", "http://demo.localhost/nested?q=test", strings.NewReader("payload"))
	r.Header.Set("X-Forwarded-For", "spoofed")
	w := httptest.NewRecorder()
	g.Public().ServeHTTP(w, r)
	if w.Code != 200 || w.Body.String() != "payload" {
		t.Fatalf("proxy changed body %d %s", w.Code, w.Body)
	}
}

func TestBrokenUpstreamDoesNotCreateReloadLoop(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	upstream.Close()
	g := New("private")
	g.routes["demo.localhost"] = Route{Hostname: "demo.localhost", State: "running", Upstream: upstream.URL, Deadline: time.Now().Add(time.Minute)}
	r := httptest.NewRequest("GET", "http://demo.localhost/demo", nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	g.Public().ServeHTTP(w, r)
	if w.Code != 503 || strings.Contains(w.Body.String(), "wait.js") {
		t.Fatalf("broken upstream spins forever: %d %s", w.Code, w.Body)
	}
}

func TestLeaseDeadlineCancelsInFlightProxy(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer upstream.Close()
	g := New("private")
	g.routes["demo.localhost"] = Route{Hostname: "demo.localhost", State: "running", Upstream: upstream.URL, Deadline: time.Now().Add(100 * time.Millisecond)}
	w := httptest.NewRecorder()
	g.Public().ServeHTTP(w, httptest.NewRequest("GET", "http://demo.localhost/long-request", nil))
	if w.Code != 410 {
		t.Fatalf("in-flight request outlived lease: %d %s", w.Code, w.Body)
	}
}

func TestConfiguredExternalSchemeOverridesVisitorHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Forwarded-Proto") != "https" {
			t.Errorf("external scheme lost: %q", r.Header.Get("X-Forwarded-Proto"))
		}
		w.WriteHeader(204)
	}))
	defer upstream.Close()
	g := New("private")
	g.PublicScheme = "https"
	g.routes["demo.example"] = Route{Hostname: "demo.example", State: "running", Upstream: upstream.URL, Deadline: time.Now().Add(time.Minute)}
	r := httptest.NewRequest("GET", "http://demo.example/", nil)
	r.Header.Set("X-Forwarded-Proto", "injected")
	w := httptest.NewRecorder()
	g.Public().ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatal(w.Code)
	}
}
