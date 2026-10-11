package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestCredentialsNeverFollowRedirects(t *testing.T) {
	var called atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called.Store(true) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	c, err := New(source.URL, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Do(context.Background(), "GET", "/v1/projects", "", nil); err == nil {
		t.Fatal("accepted redirect")
	}
	if called.Load() {
		t.Fatal("forwarded credentials across redirect")
	}
}
func TestRejectPublicPlaintextAndURLCredentials(t *testing.T) {
	for _, raw := range []string{"http://example.com", "https://user:secret@example.com", "http://0.0.0.0:7420", "https://example.com?token=secret"} {
		if err := ValidateURL(raw); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	for _, raw := range []string{"http://127.0.0.1:7420", "http://[::1]:7420", "https://deployer.example.com"} {
		if err := ValidateURL(raw); err != nil {
			t.Fatal(err)
		}
	}
}
