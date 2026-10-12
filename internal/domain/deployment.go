package domain

import "time"

// State is the observed lifecycle state of a deployment.
type State string

const (
	StateQueued    State = "queued"    // Accepted; waiting for a build slot.
	StateBuilding  State = "building"  // Fetching source and building the image.
	StateStarting  State = "starting"  // Container started; waiting for the health check.
	StateRunning   State = "running"   // Healthy and serving traffic.
	StateStopped   State = "stopped"   // Stopped by its owner; can be started again.
	StateExpired   State = "expired"   // Lease ended; stopped until extended.
	StateFailed    State = "failed"    // Build, start, or health check failed.
	StateDestroyed State = "destroyed" // Resources removed; kept as history.
)

// InProgress reports whether the deployment has not yet become ready.
func (s State) InProgress() bool {
	return s == StateQueued || s == StateBuilding || s == StateStarting
}

// Live reports whether the deployment still owns resources and its name.
func (s State) Live() bool { return s != StateDestroyed }

// Deployment is one isolated preview environment built from a single commit.
type Deployment struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ProjectID string `json:"project_id"`
	Project   string `json:"project"`
	Ref       string `json:"ref"`
	Commit    string `json:"commit,omitempty"`
	State     State  `json:"state"`
	Error     *Error `json:"error,omitempty"`
	Hostname  string `json:"hostname"`
	URL       string `json:"url"`

	// TTLSeconds is the lifetime granted at first readiness.
	TTLSeconds int64      `json:"ttl_seconds"`
	CreatedBy  string     `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	ReadyAt    *time.Time `json:"ready_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	// PurgeAt is when retained resources are deleted automatically.
	PurgeAt *time.Time `json:"purge_at,omitempty"`
	// OperationID is the most recently submitted operation.
	OperationID string `json:"operation_id,omitempty"`

	// Image, Port, and HealthPath are recorded from the build and recipe.
	Image      string `json:"image,omitempty"`
	Port       int    `json:"port,omitempty"`
	HealthPath string `json:"health_path,omitempty"`

	// HealthTimeout and Env are runtime settings that stay server-side.
	HealthTimeout time.Duration     `json:"-"`
	Env           map[string]string `json:"-"`
}

// TTL returns the granted lifetime.
func (d Deployment) TTL() time.Duration { return time.Duration(d.TTLSeconds) * time.Second }

// ExpiredAt reports whether the lease has ended at time now.
func (d Deployment) ExpiredAt(now time.Time) bool {
	return d.ExpiresAt != nil && !now.Before(*d.ExpiresAt)
}
