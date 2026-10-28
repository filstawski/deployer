package domain

import "time"

// Project is a registered Git repository that deployments are built from.
type Project struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Repository string    `json:"repository"`
	CreatedAt  time.Time `json:"created_at"`
}

// Branch is a branch of a project's repository, as the Git host reports it.
type Branch struct {
	Name   string `json:"name"`
	Commit string `json:"commit"`
	// Default marks the repository's default branch.
	Default bool `json:"default,omitempty"`
}
