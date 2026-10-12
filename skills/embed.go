// Package skills embeds Deployer's agent skill, which `deployer skill` prints.
// The skills CLI installs the same files from the repository:
// npx skills add filstawski/deployer.
package skills

import "embed"

// FS holds the skill: deployer/SKILL.md and deployer/references/*.md.
//
//go:embed deployer
var FS embed.FS
