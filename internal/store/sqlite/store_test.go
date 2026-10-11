package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"deployer/internal/domain"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	var e *domain.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}
func TestTokenLifecycleAndHashedStorage(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }
	token, secret, err := s.IssueToken(ctx, TokenInput{Name: "operator", Scopes: []string{"admin"}, Projects: []string{"*"}, TTLSeconds: 60}, "local", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Authenticate(ctx, secret); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err = s.DB.QueryRow("SELECT hash FROM tokens WHERE id=?", token.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == secret || strings.Contains(stored, "dpl_") || len(stored) != 64 {
		t.Fatal("token is not stored as hash")
	}
	_, _, err = s.IssueToken(ctx, TokenInput{Name: "operator", Scopes: []string{"admin"}, Projects: []string{"*"}, TTLSeconds: 60}, "local", true)
	assertCode(t, err, "ALREADY_INITIALIZED")
	now = now.Add(time.Minute)
	_, err = s.Authenticate(ctx, secret)
	assertCode(t, err, "UNAUTHENTICATED")
	now = now.Add(-time.Minute)
	if err = s.Revoke(ctx, token.ID, "local"); err != nil {
		t.Fatal(err)
	}
	_, err = s.Authenticate(ctx, secret)
	assertCode(t, err, "UNAUTHENTICATED")
}
func TestConcurrentIdempotencyPersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := s.CreateProject(context.Background(), "portal", "https://example.com/team/portal.git", "actor", "same")
			ids <- p.ID
			errs <- err
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	first := ""
	for id := range ids {
		if first == "" {
			first = id
		}
		if id != first {
			t.Fatal("duplicate project created")
		}
	}
	_, err = s.CreateProject(context.Background(), "different", "https://example.com/team/other.git", "actor", "same")
	assertCode(t, err, "IDEMPOTENCY_CONFLICT")
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err := s.Project(context.Background(), first)
	if err != nil || p.Name != "portal" {
		t.Fatalf("project did not survive restart: %+v %v", p, err)
	}
	info, err := os.Stat(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0077 != 0 {
		t.Fatal("state database is publicly readable")
	}
}
func TestOperationRecoveryAndConflict(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p, err := s.CreateProject(ctx, "portal", "https://example.com/repo.git", "actor", "")
	if err != nil {
		t.Fatal(err)
	}
	o, err := s.EnqueueVerify(ctx, p.ID, "actor", "verify")
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.Claim(ctx)
	if err != nil || claimed.ID != o.ID || claimed.Attempts != 1 {
		t.Fatalf("bad claim %+v %v", claimed, err)
	}
	if err = s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	claimed, err = s.Claim(ctx)
	if err != nil || claimed.Attempts != 2 {
		t.Fatalf("not resumed %+v %v", claimed, err)
	}
	if err = s.Finish(ctx, claimed, map[string]bool{"verified": true}, nil); err != nil {
		t.Fatal(err)
	}
	ret, err := s.EnqueueVerify(ctx, p.ID, "actor", "verify")
	if err != nil || ret.Status != "succeeded" || ret.ID != o.ID {
		t.Fatalf("retry did not return original operation %+v %v", ret, err)
	}
	_, err = s.CreateProject(ctx, "other", "https://example.com/other.git", "actor", "verify")
	assertCode(t, err, "IDEMPOTENCY_CONFLICT")
}
