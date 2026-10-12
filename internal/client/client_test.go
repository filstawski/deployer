package client_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/filstawski/deployer/internal/client"
	"github.com/filstawski/deployer/internal/domain"
	"github.com/filstawski/deployer/internal/fakes"
	"github.com/filstawski/deployer/internal/testenv"
)

func TestValidateURL(t *testing.T) {
	for _, ok := range []string{"https://api.example.com", "http://127.0.0.1:7420", "http://localhost:7420/"} {
		if err := client.ValidateURL(ok, false); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"http://api.example.com", "ftp://x", "https://u:p@x", "https://x/path", "x"} {
		if client.ValidateURL(bad, false) == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	if err := client.ValidateURL("http://deployer", true); err != nil {
		t.Fatal("tunneled http rejected")
	}
}

func TestClientAgainstServer(t *testing.T) {
	env := testenv.New(t)
	c, err := client.New(env.Server.URL, env.AdminToken, client.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := c.CreateProject(ctx, domain.CreateProjectRequest{Name: "shop", Repository: "https://github.com/acme/shop"}, ""); err != nil {
		t.Fatal(err)
	}
	resp, err := c.Deploy(ctx, domain.CreateDeploymentRequest{Project: "shop", Ref: "main"}, "k")
	if err != nil {
		t.Fatal(err)
	}
	var polls int
	op, err := c.Wait(ctx, resp.Operation.ID, func(domain.Operation) { polls++ })
	if err != nil || op.Status != domain.OpSucceeded || polls == 0 {
		t.Fatalf("wait: %+v %v", op, err)
	}
	_, err = c.Deployment(ctx, "missing")
	if e, ok := domain.AsError(err); !ok || e.Code != domain.CodeNotFound {
		t.Fatalf("not found: %v", err)
	}
}

func TestWaitTimeoutLeavesOperationRunning(t *testing.T) {
	env := testenv.New(t)
	c, _ := client.New(env.Server.URL, env.AdminToken, client.Options{})
	ctx := context.Background()
	c.CreateProject(ctx, domain.CreateProjectRequest{Name: "shop", Repository: "https://github.com/acme/shop"}, "")
	resp, err := c.Deploy(ctx, domain.CreateDeploymentRequest{Project: "shop", Ref: "main"}, "")
	if err != nil {
		t.Fatal(err)
	}
	env.Runtime.Set(func(r *fakes.Runtime) { r.BlockBuild = resp.Deployment.ID })
	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	_, err = c.Wait(short, resp.Operation.ID, nil)
	if e, ok := domain.AsError(err); !ok || e.Code != domain.CodeWaitTimeout || e.Hint == "" {
		t.Fatalf("wait timeout: %v", err)
	}
	if op, _ := c.Operation(ctx, resp.Operation.ID); op.Status.Done() {
		t.Fatal("client timeout ended the operation")
	}
}

func TestRetriesOnlySafeRequests(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("<html>bad gateway</html>"))
	}))
	defer srv.Close()
	c, _ := client.New(srv.URL, "dpl_x", client.Options{})
	ctx := context.Background()
	if _, err := c.Status(ctx); domain.CodeOf(err) != domain.CodeUnavailable || calls.Load() != 3 {
		t.Fatalf("GET retried %d times: %v", calls.Load(), err)
	}
	calls.Store(0)
	c.Stop(ctx, "x", "")
	if calls.Load() != 1 {
		t.Fatalf("write without idempotency key retried: %d", calls.Load())
	}
	calls.Store(0)
	c.Stop(ctx, "x", "key")
	if calls.Load() != 3 {
		t.Fatalf("write with idempotency key not retried: %d", calls.Load())
	}
}
