package domain

import (
	"encoding/json"
	"time"
)

// OperationKind identifies the work an operation performs.
type OperationKind string

const (
	OpVerifyProject     OperationKind = "project.verify"
	OpCreateDeployment  OperationKind = "deployment.create"
	OpStartDeployment   OperationKind = "deployment.start"
	OpStopDeployment    OperationKind = "deployment.stop"
	OpDestroyDeployment OperationKind = "deployment.destroy"
	OpUpdateDeployment  OperationKind = "deployment.update"
)

// OperationStatus is the progress of an operation.
type OperationStatus string

const (
	OpQueued    OperationStatus = "queued"
	OpRunning   OperationStatus = "running"
	OpSucceeded OperationStatus = "succeeded"
	OpFailed    OperationStatus = "failed"
	OpCancelled OperationStatus = "cancelled"
)

// Done reports whether the operation has reached a final status.
func (s OperationStatus) Done() bool {
	return s == OpSucceeded || s == OpFailed || s == OpCancelled
}

// Operation is durable, asynchronous work accepted by the server. It keeps
// running when the client that submitted it disconnects.
type Operation struct {
	ID           string          `json:"id"`
	Kind         OperationKind   `json:"kind"`
	Status       OperationStatus `json:"status"`
	ProjectID    string          `json:"project_id"`
	DeploymentID string          `json:"deployment_id,omitempty"`
	Actor        string          `json:"actor"`
	Attempts     int             `json:"attempts"`
	Error        *Error          `json:"error,omitempty"`
	// Request holds the operation's parameters, for kinds that take any.
	Request json.RawMessage `json:"request,omitempty"`
	// Checkpoint is the handler's recorded progress, for resuming after an
	// interruption. It is internal to the server.
	Checkpoint json.RawMessage `json:"-"`
	Result     json.RawMessage `json:"result,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
	StartedAt  *time.Time      `json:"started_at,omitempty"`
	FinishedAt *time.Time      `json:"finished_at,omitempty"`
	Deadline   time.Time       `json:"deadline"`
}

// UpdateResult is the result of a successful deployment.update operation.
// Changed is false when the ref still pointed at the running commit, and
// nothing was rebuilt.
type UpdateResult struct {
	PreviousCommit string `json:"previous_commit"`
	Commit         string `json:"commit"`
	Changed        bool   `json:"changed"`
}

// VerifyResult is the result of a successful project.verify operation.
type VerifyResult struct {
	HeadCommit string `json:"head_commit"`
}
