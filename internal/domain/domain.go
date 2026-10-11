package domain

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

func (e *Error) Error() string         { return e.Code + ": " + e.Message }
func Fail(code, message string) *Error { return &Error{Code: code, Message: message} }

func ID(prefix string) string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + "_" + hex.EncodeToString(b)
}

var nameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

func ValidateName(s string) error {
	if !nameRE.MatchString(s) {
		return Fail("INVALID_INPUT", "name must start with a lowercase letter and contain at most 63 lowercase letters, digits, or hyphens")
	}
	return nil
}

// Initially register only explicit HTTPS/SSH Git URLs. Credentials belong in the
// server credential store, not in URLs or Git command options.
func ValidateRepository(raw string) error {
	if strings.ContainsAny(raw, "\n\r\x00") {
		return Fail("INVALID_INPUT", "invalid repository URL")
	}
	if strings.HasPrefix(raw, "git@") {
		parts := strings.SplitN(raw[4:], ":", 2)
		if len(parts) == 2 && parts[0] != "" && !strings.ContainsAny(parts[0], "/ @") && strings.HasSuffix(parts[1], ".git") && !strings.HasPrefix(parts[1], "-") && parts[1] != ".git" {
			return nil
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.Path == "" || u.Path == "/" || u.RawQuery != "" || u.Fragment != "" {
		return Fail("INVALID_INPUT", "repository must be an HTTPS or SSH Git URL")
	}
	if u.Scheme == "https" && u.User == nil {
		return nil
	}
	if u.Scheme == "ssh" && u.User != nil && u.User.Username() == "git" {
		if _, password := u.User.Password(); !password {
			return nil
		}
	}
	return Fail("INVALID_INPUT", "repository must use credential-free HTTPS or git SSH authentication")
}

type Project struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Repository string    `json:"repository"`
	CreatedAt  time.Time `json:"created_at"`
}

type Token struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Scopes    []string   `json:"scopes"`
	Projects  []string   `json:"projects"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Revoked   bool       `json:"revoked"`
}

func (t Token) Has(scope string) bool {
	for _, s := range t.Scopes {
		if s == "admin" || s == scope {
			return true
		}
	}
	return false
}
func (t Token) AllowsProject(id string) bool {
	for _, p := range t.Projects {
		if p == "*" || p == id {
			return true
		}
	}
	return false
}
func ValidateGrants(scopes, projects []string) error {
	if len(scopes) == 0 || len(projects) == 0 {
		return Fail("INVALID_INPUT", "at least one scope and project grant are required")
	}
	allowed := map[string]bool{"admin": true, "server:read": true, "projects:read": true, "projects:write": true, "deployments:read": true, "deployments:write": true}
	for _, s := range scopes {
		if !allowed[s] {
			return Fail("INVALID_INPUT", fmt.Sprintf("unsupported scope %q", s))
		}
	}
	for _, p := range projects {
		if p != "*" && !strings.HasPrefix(p, "prj_") {
			return Fail("INVALID_INPUT", "project grants must be project IDs or *")
		}
	}
	for _, s := range scopes {
		if s == "admin" && (len(projects) != 1 || projects[0] != "*") {
			return Fail("INVALID_INPUT", "admin tokens require the * project grant")
		}
	}
	return nil
}

type Envelope struct {
	SchemaVersion int    `json:"schema_version"`
	Data          any    `json:"data,omitempty"`
	Error         *Error `json:"error,omitempty"`
	RequestID     string `json:"request_id,omitempty"`
}
