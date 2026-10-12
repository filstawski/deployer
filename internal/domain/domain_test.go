package domain

import (
	"strings"
	"testing"
	"time"
)

func TestNormalizeRepository(t *testing.T) {
	valid := map[string]string{
		"https://github.com/Owner/Repo.git":     "https://github.com/owner/repo.git",
		"https://github.com/owner/repo":         "https://github.com/owner/repo.git",
		"git@github.com:owner/repo.git":         "https://github.com/owner/repo.git",
		"ssh://git@github.com/owner/repo":       "https://github.com/owner/repo.git",
		"https://gitlab.example.com/team/a.git": "https://gitlab.example.com/team/a.git",
	}
	for in, want := range valid {
		got, err := NormalizeRepository(in)
		if err != nil || got != want {
			t.Errorf("NormalizeRepository(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{
		"http://github.com/owner/repo", "https://user:pw@github.com/o/r", "https://github.com/o/r?x=1",
		"https://github.com/", "https://github.com/only-owner", "git@gitlab.com:o/r.git", "https://example.com/../x",
		"https://github.com/o/r\nx",
	} {
		if _, err := NormalizeRepository(in); CodeOf(err) != CodeInvalidInput {
			t.Errorf("NormalizeRepository(%q) accepted", in)
		}
	}
	owner, repo, ok := GitHubRepository("https://github.com/owner/repo.git")
	if !ok || owner != "owner" || repo != "repo" {
		t.Fatalf("GitHubRepository = %q %q %v", owner, repo, ok)
	}
}

func TestValidateNamesAndRefs(t *testing.T) {
	for _, ok := range []string{"a", "design-a", "x1", strings.Repeat("a", 63)} {
		if err := ValidateDeploymentName(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-a", "a-", "A", "a_b", "api", strings.Repeat("a", 64)} {
		if ValidateDeploymentName(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	for _, ok := range []string{"main", "design/a", "release-1.2", strings.Repeat("a", 40)} {
		if err := ValidateRef(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-x", "a..b", "a//b", "a/", "a.lock", "a b", "a~1"} {
		if ValidateRef(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if !IsCommitSHA(strings.Repeat("f", 40)) || IsCommitSHA("main") {
		t.Fatal("IsCommitSHA")
	}
}

func TestDurations(t *testing.T) {
	cases := map[string]time.Duration{"30m": 30 * time.Minute, "2d": 48 * time.Hour, "1w": 168 * time.Hour, "1d12h": 36 * time.Hour}
	for in, want := range cases {
		if got, err := ParseDuration(in); err != nil || got != want {
			t.Errorf("ParseDuration(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := ParseDuration("soon"); err == nil {
		t.Fatal("accepted invalid duration")
	}
	formats := map[time.Duration]string{49 * time.Hour: "2d 1h", 48 * time.Hour: "2d", 90 * time.Minute: "1h 30m", 5 * time.Second: "5s", 3 * time.Minute: "3m"}
	for in, want := range formats {
		if got := FormatDuration(in); got != want {
			t.Errorf("FormatDuration(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestSlug(t *testing.T) {
	if got := Slug("Feature/New_Login!!", 30); got != "feature-new-login" {
		t.Fatal(got)
	}
	if got := Slug("abc-def", 4); got != "abc" {
		t.Fatal(got)
	}
}

func TestTokenGrants(t *testing.T) {
	tok := Token{Scopes: []Scope{ScopeDeploymentsRead}, Projects: []string{"prj_1"}}
	if !tok.Has(ScopeDeploymentsRead) || tok.Has(ScopeDeploymentsWrite) || !tok.CanAccess("prj_1") || tok.CanAccess("prj_2") {
		t.Fatal("token grants")
	}
	admin := Token{Scopes: []Scope{ScopeAdmin}, Projects: []string{AllProjects}}
	if !admin.Has(ScopeDeploymentsWrite) || !admin.CanAccess("prj_x") {
		t.Fatal("admin grants")
	}
	if ValidateGrants([]Scope{ScopeAdmin}, []string{"prj_1"}) == nil {
		t.Fatal("admin token restricted to a project accepted")
	}
	if ValidateGrants([]Scope{"root"}, []string{AllProjects}) == nil || ValidateGrants(Roles["deployer"], []string{"oops"}) == nil {
		t.Fatal("invalid grants accepted")
	}
	if _, err := ParseScopes("deployments:read,nope"); err == nil {
		t.Fatal("ParseScopes accepted unknown scope")
	}
}

func TestNewID(t *testing.T) {
	id := NewID(PrefixDeployment)
	if !strings.HasPrefix(id, "dep_") || len(id) != 16 || NewID(PrefixDeployment) == id {
		t.Fatal(id)
	}
}
