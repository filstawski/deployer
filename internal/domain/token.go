package domain

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

// Scope grants a class of API actions to a token.
type Scope string

const (
	ScopeAdmin            Scope = "admin" // Everything, including token management.
	ScopeServerRead       Scope = "server:read"
	ScopeProjectsRead     Scope = "projects:read"
	ScopeProjectsWrite    Scope = "projects:write"
	ScopeDeploymentsRead  Scope = "deployments:read"
	ScopeDeploymentsWrite Scope = "deployments:write"
)

// AllProjects is the project grant that covers every project, including
// projects created later.
const AllProjects = "*"

var knownScopes = []Scope{ScopeAdmin, ScopeServerRead, ScopeProjectsRead, ScopeProjectsWrite, ScopeDeploymentsRead, ScopeDeploymentsWrite}

// Roles are named scope bundles for common kinds of tokens.
var Roles = map[string][]Scope{
	"admin":    {ScopeAdmin},
	"deployer": {ScopeServerRead, ScopeProjectsRead, ScopeProjectsWrite, ScopeDeploymentsRead, ScopeDeploymentsWrite},
	"viewer":   {ScopeServerRead, ScopeProjectsRead, ScopeDeploymentsRead},
}

// RoleNames returns the role names in a stable order.
func RoleNames() []string {
	names := make([]string, 0, len(Roles))
	for name := range Roles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ParseScopes parses a comma-separated scope list.
func ParseScopes(s string) ([]Scope, error) {
	var out []Scope
	for _, part := range strings.Split(s, ",") {
		scope := Scope(strings.TrimSpace(part))
		if !slices.Contains(knownScopes, scope) {
			return nil, Errorf(CodeInvalidInput, "unknown scope %q", scope)
		}
		if !slices.Contains(out, scope) {
			out = append(out, scope)
		}
	}
	return out, nil
}

// Token is an API credential. Only a hash of its secret is stored.
type Token struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Scopes    []Scope    `json:"scopes"`
	Projects  []string   `json:"projects"`
	CreatedBy string     `json:"created_by"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

// Has reports whether the token grants scope. Admin grants every scope.
func (t Token) Has(scope Scope) bool {
	return slices.Contains(t.Scopes, ScopeAdmin) || slices.Contains(t.Scopes, scope)
}

// CanAccess reports whether the token's project grants include projectID.
func (t Token) CanAccess(projectID string) bool {
	return slices.Contains(t.Projects, AllProjects) || slices.Contains(t.Projects, projectID)
}

// Active reports whether the token is neither revoked nor expired at now.
func (t Token) Active(now time.Time) bool {
	return t.RevokedAt == nil && (t.ExpiresAt == nil || now.Before(*t.ExpiresAt))
}

// ValidateGrants checks a token's scopes and project grants for consistency.
func ValidateGrants(scopes []Scope, projects []string) error {
	if len(scopes) == 0 {
		return Errorf(CodeInvalidInput, "a token needs at least one scope")
	}
	for _, s := range scopes {
		if !slices.Contains(knownScopes, s) {
			return Errorf(CodeInvalidInput, "unknown scope %q", s)
		}
	}
	if len(projects) == 0 {
		return Errorf(CodeInvalidInput, "a token needs a project grant: project IDs or %q", AllProjects)
	}
	for _, p := range projects {
		if p != AllProjects && !strings.HasPrefix(p, PrefixProject+"_") {
			return Errorf(CodeInvalidInput, "project grant %q must be a project ID (%s_...) or %q", p, PrefixProject, AllProjects)
		}
	}
	if slices.Contains(scopes, ScopeAdmin) && !slices.Equal(projects, []string{AllProjects}) {
		return Errorf(CodeInvalidInput, "admin tokens must grant %q", AllProjects)
	}
	return nil
}

// FormatScopes renders scopes as a comma-separated list.
func FormatScopes(scopes []Scope) string {
	parts := make([]string, len(scopes))
	for i, s := range scopes {
		parts[i] = string(s)
	}
	return strings.Join(parts, ",")
}

// String implements fmt.Stringer for log output.
func (t Token) String() string { return fmt.Sprintf("%s (%s)", t.ID, t.Name) }

// TokenSecretPrefix starts every API token secret, which makes leaked tokens
// easy to recognize and scan for.
const TokenSecretPrefix = "dpl_"

// NewTokenSecret returns a new random API token secret.
func NewTokenSecret() string {
	b := make([]byte, 32)
	rand.Read(b) // Never fails; see crypto/rand.Read.
	return TokenSecretPrefix + hex.EncodeToString(b)
}

// ValidTokenSecret reports whether s has the shape of a token secret.
func ValidTokenSecret(s string) bool {
	hexPart, ok := strings.CutPrefix(s, TokenSecretPrefix)
	if !ok || len(hexPart) != 64 {
		return false
	}
	_, err := hex.DecodeString(hexPart)
	return err == nil
}
