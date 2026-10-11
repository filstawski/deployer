package cli

import (
	"context"
	"crypto/sha256"
	"deployer/internal/command"
	"deployer/internal/domain"
	"deployer/internal/githubapp"
	"encoding/hex"
	"encoding/json"
	"strings"
)

func canonicalGitHub(remote string) (string, error) {
	remote = strings.TrimSpace(remote)
	if strings.HasPrefix(remote, "git@github.com:") {
		remote = "https://github.com/" + strings.TrimPrefix(remote, "git@github.com:")
	}
	if strings.HasPrefix(remote, "ssh://git@github.com/") {
		remote = "https://github.com/" + strings.TrimPrefix(remote, "ssh://git@github.com/")
	}
	owner, repo, ok := githubapp.Repository(remote)
	if !ok {
		return "", domain.Fail("INVALID_INPUT", "automatic project detection requires a github.com origin remote without credentials")
	}
	return "https://github.com/" + strings.ToLower(owner+"/"+repo) + ".git", nil
}
func (a *app) autoProject(ctx context.Context) (string, error) {
	remote, err := command.Run(ctx, "", nil, nil, "git", "remote", "get-url", "origin")
	if err != nil {
		return "", domain.Fail("INVALID_INPUT", "run inside a Git repository with origin, or specify a project")
	}
	repository, err := canonicalGitHub(remote)
	if err != nil {
		return "", err
	}
	data, err := a.call(ctx, "GET", "/v1/projects", "", nil)
	if err != nil {
		return "", err
	}
	var projects []domain.Project
	if err = json.Unmarshal(data, &projects); err != nil {
		return "", err
	}
	match := ""
	for _, p := range projects {
		canonical, e := canonicalGitHub(p.Repository)
		if e == nil && canonical == repository {
			if match != "" {
				return "", domain.Fail("INVALID_INPUT", "multiple projects use this repository; specify the project name")
			}
			match = p.ID
		}
	}
	if match != "" {
		return match, nil
	}
	owner, repo, _ := githubapp.Repository(repository)
	name := strings.Trim(strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, owner+"-"+repo), "-")
	if len(name) > 35 {
		name = strings.TrimRight(name[:35], "-")
	}
	sum := sha256.Sum256([]byte(repository))
	suffix := hex.EncodeToString(sum[:])[:8]
	name = "repo-" + name + "-" + suffix
	data, err = a.call(ctx, "POST", "/v1/projects", "auto-project-"+suffix, map[string]string{"name": name, "repository": repository})
	if err != nil {
		return "", err
	}
	var project domain.Project
	err = json.Unmarshal(data, &project)
	return project.ID, err
}
