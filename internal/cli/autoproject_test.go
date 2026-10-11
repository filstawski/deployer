package cli

import (
	"context"
	"deployer/internal/client"
	"deployer/internal/command"
	"deployer/internal/domain"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalGitHubRemote(t *testing.T) {
	for _, remote := range []string{"git@github.com:Owner/Repo.git", "ssh://git@github.com/Owner/Repo.git", "https://github.com/Owner/Repo"} {
		got, e := canonicalGitHub(remote)
		if e != nil || got != "https://github.com/owner/repo.git" {
			t.Fatal(got, e)
		}
	}
	for _, remote := range []string{"https://secret@github.com/owner/repo", "https://github.com.evil/owner/repo", "file:///repo"} {
		if _, e := canonicalGitHub(remote); e == nil {
			t.Fatal("accepted", remote)
		}
	}
}
func TestAutoProjectRegistersOnceAndReusesExistingRepository(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	for _, args := range [][]string{{"init"}, {"remote", "add", "origin", "git@github.com:Owner/Repo.git"}} {
		if _, e := command.Run(context.Background(), dir, nil, nil, "git", args...); e != nil {
			t.Fatal(e)
		}
	}
	projects := []domain.Project{}
	created := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects" {
			t.Fatal("unexpected path", r.URL.Path)
		}
		if r.Method == "POST" {
			created++
			var p domain.Project
			json.NewDecoder(r.Body).Decode(&p)
			if p.Repository != "https://github.com/owner/repo.git" {
				t.Error(p.Repository)
			}
			if err := domain.ValidateName(p.Name); err != nil {
				t.Error(err)
			}
			p.ID = "prj_one"
			projects = append(projects, p)
			json.NewEncoder(w).Encode(domain.Envelope{SchemaVersion: 1, Data: p})
			return
		}
		json.NewEncoder(w).Encode(domain.Envelope{SchemaVersion: 1, Data: projects})
	}))
	defer server.Close()
	c, _ := client.New(server.URL, "test")
	a := &app{connection: c}
	for range 2 {
		id, e := a.autoProject(context.Background())
		if e != nil || id != "prj_one" {
			t.Fatal(id, e)
		}
	}
	if created != 1 {
		t.Fatal("duplicate project registered")
	}
	projects = append(projects, domain.Project{ID: "prj_two", Repository: "https://github.com/owner/repo"})
	if _, e := a.autoProject(context.Background()); e == nil {
		t.Fatal("ambiguous repository silently selected")
	}
	if _, e := os.Stat(filepath.Join(dir, ".git")); e != nil {
		t.Fatal(e)
	}
}
