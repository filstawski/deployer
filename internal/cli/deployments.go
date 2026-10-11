package cli

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"deployer/internal/domain"
	"deployer/internal/store/sqlite"
)

func (a *app) deploy(ctx context.Context, args []string) (any, error) {
	project := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		project = args[0]
		args = args[1:]
	}
	f := a.flags("deploy")
	ref := f.String("ref", "", "branch or full commit SHA")
	ttl := f.String("ttl", "1d", "lifetime after readiness (up to 7d)")
	sub := f.String("subdomain", "", "unique subdomain; generated when omitted")
	key := f.String("idempotency-key", domain.ID("request"), "retry key")
	wait := f.Bool("wait", false, "wait for readiness")
	timeout := f.Duration("timeout", 20*time.Minute, "client wait timeout")
	if err := parse(f, args); err != nil {
		return nil, err
	}
	if err := domain.ValidateRef(*ref); err != nil {
		return nil, err
	}
	duration, err := domain.Duration(*ttl)
	if err != nil {
		return nil, err
	}
	if duration > 7*24*time.Hour || *timeout <= 0 {
		return nil, domain.Fail("INVALID_INPUT", "TTL must be at most 7d and timeout must be positive")
	}
	if project == "" {
		project, err = a.autoProject(ctx)
		if err != nil {
			return nil, err
		}
	}
	data, err := a.call(ctx, "POST", "/v1/deployments", *key, domain.DeployInput{Project: project, Ref: *ref, Subdomain: *sub, TTLSeconds: int64(duration.Seconds())})
	if err != nil {
		return nil, err
	}
	return a.maybeWait(ctx, data, *wait, *timeout)
}
func (a *app) maybeWait(ctx context.Context, data json.RawMessage, wait bool, timeout time.Duration) (any, error) {
	if !wait {
		return data, nil
	}
	var op sqlite.Operation
	if err := json.Unmarshal(data, &op); err != nil {
		return nil, err
	}
	return a.wait(ctx, op.ID, timeout)
}
func (a *app) deployment(ctx context.Context, command string, args []string) (any, error) {
	if command == "list" {
		if len(args) != 0 {
			return nil, domain.Fail("INVALID_INPUT", "list takes no arguments")
		}
		return a.call(ctx, "GET", "/v1/deployments", "", nil)
	}
	if len(args) == 0 {
		return nil, domain.Fail("INVALID_INPUT", command+" requires a deployment ID")
	}
	path := "/v1/deployments/" + url.PathEscape(args[0])
	f := a.flags(command)
	if command == "inspect" {
		if err := parse(f, args[1:]); err != nil {
			return nil, err
		}
		return a.call(ctx, "GET", path, "", nil)
	}
	if command == "logs" {
		build := f.Bool("build", false, "show build log")
		if err := parse(f, args[1:]); err != nil {
			return nil, err
		}
		if *build {
			path += "/logs?build=true"
		} else {
			path += "/logs"
		}
		return a.call(ctx, "GET", path, "", nil)
	}
	key := f.String("idempotency-key", domain.ID("request"), "retry key")
	wait := f.Bool("wait", false, "wait for completion")
	timeout := f.Duration("timeout", 5*time.Minute, "client wait timeout")
	var by *string
	var yes *bool
	if command == "extend" {
		by = f.String("by", "", "additional lifetime")
	}
	if command == "destroy" {
		yes = f.Bool("yes", false, "permanently delete the preview and its files")
	}
	if err := parse(f, args[1:]); err != nil {
		return nil, err
	}
	if *timeout <= 0 {
		return nil, domain.Fail("INVALID_INPUT", "timeout must be positive")
	}
	if yes != nil && !*yes {
		return nil, domain.Fail("INVALID_INPUT", "destroy requires --yes")
	}
	seconds := int64(0)
	if by != nil {
		duration, err := domain.Duration(*by)
		if err != nil {
			return nil, err
		}
		seconds = int64(duration.Seconds())
	}
	data, err := a.call(ctx, "POST", path+"/actions/"+command, *key, map[string]int64{"seconds": seconds})
	if err != nil {
		return nil, err
	}
	return a.maybeWait(ctx, data, *wait, *timeout)
}
