// Package gateway is the preview data plane. It routes visitors to the
// deployment that owns their hostname and serves status pages while a
// preview is building, stopped, failed, or expired.
//
// The gateway runs in its own container attached to each deployment's private
// network. It holds no Docker access and no database: the controller pushes
// complete route snapshots to its authenticated control API.
package gateway

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/filstawski/deployer/internal/domain"
)

// Route tells the gateway how to serve one preview hostname.
type Route struct {
	Host  string       `json:"host"`
	State domain.State `json:"state"`
	// Upstream is the application's address on its private network, such as
	// http://deployer-app-dep_x:3000. It is empty until an image is built.
	Upstream   string `json:"upstream,omitempty"`
	HealthPath string `json:"health_path,omitempty"`
	// ExpiresAt is enforced by the gateway itself, so an expired preview stops
	// serving even if the controller is down.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// visibleState returns the state visitors should see at time now.
func (r Route) visibleState(now time.Time) domain.State {
	if r.ExpiresAt != nil && !now.Before(*r.ExpiresAt) && r.State != domain.StateFailed {
		return domain.StateExpired
	}
	return r.State
}

func (r Route) validate() error {
	if r.Host == "" || strings.ToLower(r.Host) != r.Host || strings.ContainsAny(r.Host, ":/ ") {
		return fmt.Errorf("invalid host %q", r.Host)
	}
	if r.HealthPath != "" && (!strings.HasPrefix(r.HealthPath, "/") || strings.HasPrefix(r.HealthPath, "//")) {
		return fmt.Errorf("invalid health path for %s", r.Host)
	}
	if r.Upstream != "" {
		u, err := url.Parse(r.Upstream)
		if err != nil || u.Scheme != "http" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" {
			return fmt.Errorf("invalid upstream for %s", r.Host)
		}
	}
	return nil
}
