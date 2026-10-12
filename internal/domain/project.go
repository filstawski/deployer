package domain

import "time"

// Project is a registered Git repository that deployments are built from.
type Project struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Repository string    `json:"repository"`
	CreatedAt  time.Time `json:"created_at"`
}
