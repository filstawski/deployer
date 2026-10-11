package sqlite

import (
	"context"
	"sync"
	"testing"
	"time"

	"deployer/internal/domain"
)

func TestDeploymentAdmissionIdempotencyAndLeases(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p, err := s.CreateProject(ctx, "demo", "https://example.com/repo.git", "actor", "")
	if err != nil {
		t.Fatal(err)
	}
	in := domain.DeployInput{Ref: "design/a", TTLSeconds: 60}
	var wg sync.WaitGroup
	ids := make(chan string, 6)
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			o, err := s.QueueDeploy(ctx, in, p, "actor", "one", "localhost", 8080, 1)
			if err != nil {
				t.Error(err)
			}
			ids <- o.ID
		}()
	}
	wg.Wait()
	close(ids)
	id := ""
	for got := range ids {
		if id == "" {
			id = got
		}
		if got != id {
			t.Fatal("retry created multiple deployments")
		}
	}
	_, err = s.QueueDeploy(ctx, in, p, "actor", "two", "localhost", 8080, 1)
	assertCode(t, err, "QUOTA_EXCEEDED")
	changed := in
	changed.Ref = "main"
	_, err = s.QueueDeploy(ctx, changed, p, "actor", "one", "localhost", 8080, 1)
	assertCode(t, err, "IDEMPOTENCY_CONFLICT")
	o, err := s.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.QueueAction(ctx, o.DeploymentID, "stop", "actor", "stop", 0)
	assertCode(t, err, "OPERATION_IN_PROGRESS")
	s.Finish(ctx, o, nil, nil)
	d, err := s.Deployment(ctx, o.DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	expires := s.Now().Add(time.Minute)
	d.ExpiresAt = &expires
	d.State = "running"
	s.SaveDeployment(ctx, d)
	first, err := s.QueueAction(ctx, d.ID, "extend", "actor", "extend", 60)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.QueueAction(ctx, d.ID, "extend", "actor", "extend", 60)
	if err != nil || again.ID != first.ID {
		t.Fatal("extension not idempotent")
	}
	d, _ = s.Deployment(ctx, d.ID)
	if !d.ExpiresAt.Equal(expires.Add(time.Minute)) {
		t.Fatal("retry extended twice")
	}
}

func TestPreviewURLUsesExternalHTTPSOrigin(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p, _ := s.CreateProject(ctx, "demo", "https://example.com/demo.git", "actor", "")
	o, err := s.QueueDeploy(ctx, domain.DeployInput{Ref: "main", Subdomain: "preview", TTLSeconds: 60}, p, "actor", "https", "example.com", 443, 8, "https")
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.Deployment(ctx, o.DeploymentID)
	if err != nil || d.URL != "https://preview.example.com" {
		t.Fatalf("wrong public URL: %+v %v", d, err)
	}
}
