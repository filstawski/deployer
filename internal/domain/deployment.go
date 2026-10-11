package domain

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Deployment struct {
	ID            string            `json:"id"`
	ProjectID     string            `json:"project_id"`
	Ref           string            `json:"ref"`
	Commit        string            `json:"commit,omitempty"`
	Hostname      string            `json:"hostname"`
	URL           string            `json:"url"`
	State         string            `json:"state"`
	Image         string            `json:"image,omitempty"`
	Port          int               `json:"port,omitempty"`
	HealthPath    string            `json:"health_path,omitempty"`
	HealthTimeout string            `json:"health_timeout,omitempty"`
	Environment   map[string]string `json:"environment,omitempty"`
	TTLSeconds    int64             `json:"ttl_seconds"`
	CreatedAt     time.Time         `json:"created_at"`
	ExpiresAt     *time.Time        `json:"expires_at,omitempty"`
	PurgeAt       *time.Time        `json:"purge_at,omitempty"`
	OperationID   string            `json:"operation_id"`
	Deadline      time.Time         `json:"deadline"`
	Error         *Error            `json:"error,omitempty"`
}
type DeployInput struct {
	Project    string `json:"project"`
	Ref        string `json:"ref"`
	Subdomain  string `json:"subdomain,omitempty"`
	TTLSeconds int64  `json:"ttl_seconds"`
}

var refPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$`)

func ValidateRef(ref string) error {
	if !refPattern.MatchString(ref) || strings.Contains(ref, "..") || strings.Contains(ref, "//") || strings.HasSuffix(ref, "/") || strings.HasSuffix(ref, ".") || strings.HasSuffix(ref, ".lock") {
		return Fail("INVALID_INPUT", "ref must be a branch name or full commit SHA")
	}
	return nil
}
func Duration(raw string) (time.Duration, error) {
	if strings.HasSuffix(raw, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(raw, "d"))
		if err != nil || n < 1 || n > 365 {
			return 0, Fail("INVALID_INPUT", "duration in days must be between 1d and 365d")
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < time.Second || d > 365*24*time.Hour {
		return 0, Fail("INVALID_INPUT", "duration must be between 1s and 365d")
	}
	return d, nil
}
