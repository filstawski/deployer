package domain

import (
	"net/url"
	"regexp"
	"strings"
)

var (
	labelPattern     = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	tokenNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	refPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$`)
	commitPattern    = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)
	githubComponent  = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)
)

// MaxProjectNameLength bounds project names so that generated deployment
// names (project-ref-random) still fit in a 63-character DNS label.
const MaxProjectNameLength = 40

// ReservedNames cannot be used as deployment names because the installation
// uses them for its own hostnames, such as api.<domain>.
var ReservedNames = map[string]bool{"api": true, "www": true, "admin": true, "deployer": true, "status": true}

// ValidateDeploymentName checks that name is a usable DNS label for a preview
// hostname such as <name>.<domain>.
func ValidateDeploymentName(name string) error {
	if len(name) > 63 || !labelPattern.MatchString(name) {
		return Errorf(CodeInvalidInput, "name %q must be 1-63 lowercase letters, digits, or hyphens, starting and ending with a letter or digit", name)
	}
	if ReservedNames[name] {
		return Errorf(CodeInvalidInput, "name %q is reserved", name)
	}
	return nil
}

// ValidateProjectName checks a project name. Project names are DNS labels so
// they can prefix generated deployment names.
func ValidateProjectName(name string) error {
	if len(name) > MaxProjectNameLength || !labelPattern.MatchString(name) {
		return Errorf(CodeInvalidInput, "project name %q must be 1-%d lowercase letters, digits, or hyphens", name, MaxProjectNameLength)
	}
	return nil
}

// ValidateTokenName checks a human-readable API token name.
func ValidateTokenName(name string) error {
	if !tokenNamePattern.MatchString(name) {
		return Errorf(CodeInvalidInput, "token name %q must be 1-64 letters, digits, dots, dashes, or underscores", name)
	}
	return nil
}

// ValidateRef checks that ref is a plausible branch name or full commit SHA.
// The Git host decides whether it exists.
func ValidateRef(ref string) error {
	if !refPattern.MatchString(ref) || strings.Contains(ref, "..") || strings.Contains(ref, "//") ||
		strings.HasSuffix(ref, "/") || strings.HasSuffix(ref, ".") || strings.HasSuffix(ref, ".lock") {
		return Errorf(CodeInvalidInput, "ref %q must be a branch name or a full commit SHA", ref)
	}
	return nil
}

// IsCommitSHA reports whether s is a full SHA-1 or SHA-256 commit ID.
func IsCommitSHA(s string) bool { return commitPattern.MatchString(s) }

// NormalizeRepository validates a Git repository URL and returns its canonical
// HTTPS form. GitHub SSH remotes (git@github.com:owner/repo.git and
// ssh://git@github.com/owner/repo) are converted to HTTPS, because the server
// fetches over HTTPS with its own credentials. URLs carrying credentials,
// queries, or fragments are rejected.
func NormalizeRepository(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if strings.ContainsAny(raw, " \t\r\n\x00") {
		return "", Errorf(CodeInvalidInput, "repository URL contains whitespace or control characters")
	}
	for _, prefix := range []string{"git@github.com:", "ssh://git@github.com/"} {
		if rest, ok := strings.CutPrefix(raw, prefix); ok {
			raw = "https://github.com/" + rest
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", Errorf(CodeInvalidInput, "repository must be an HTTPS Git URL such as https://github.com/owner/repo.git")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", Errorf(CodeInvalidInput, "repository URL must not contain credentials, a query, or a fragment")
	}
	path := strings.Trim(u.Path, "/")
	if path == "" || strings.Contains(path, "..") {
		return "", Errorf(CodeInvalidInput, "repository URL must include a repository path")
	}
	host := strings.ToLower(u.Host)
	if host == "github.com" {
		owner, repo, ok := strings.Cut(strings.TrimSuffix(path, ".git"), "/")
		if !ok || !githubComponent.MatchString(owner) || !githubComponent.MatchString(repo) || repo == "." || strings.Contains(repo, "/") {
			return "", Errorf(CodeInvalidInput, "GitHub repository URLs must look like https://github.com/owner/repo")
		}
		return "https://github.com/" + strings.ToLower(owner) + "/" + strings.ToLower(repo) + ".git", nil
	}
	return "https://" + host + "/" + path, nil
}

// GitHubRepository returns the owner and name of a canonical github.com
// repository URL produced by NormalizeRepository.
func GitHubRepository(normalized string) (owner, repo string, ok bool) {
	rest, ok := strings.CutPrefix(normalized, "https://github.com/")
	if !ok {
		return "", "", false
	}
	owner, repo, ok = strings.Cut(strings.TrimSuffix(rest, ".git"), "/")
	return owner, repo, ok && owner != "" && repo != ""
}

// Slug converts arbitrary text, such as a branch name, into a DNS-label
// fragment of at most max characters. It may return an empty string.
func Slug(s string, max int) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	out := b.String()
	if len(out) > max {
		out = out[:max]
	}
	return strings.Trim(out, "-")
}
