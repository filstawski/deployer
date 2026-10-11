package cli

import (
	"context"
	"deployer/internal/domain"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
)

func (a *app) github(ctx context.Context, args []string) (any, error) {
	f := a.flags("github " + args[0])
	noBrowser := f.Bool("no-browser", false, "print the link without opening a browser")
	publicURL := f.String("public-url", "", "public HTTPS API origin (setup only)")
	organization := f.String("organization", "", "GitHub organization that owns the app (setup only)")
	if err := parse(f, args[1:]); err != nil {
		return nil, err
	}
	if args[0] == "status" {
		return a.call(ctx, "GET", "/v1/github", "", nil)
	}
	if args[0] != "setup" && args[0] != "connect" {
		return nil, domain.Fail("INVALID_INPUT", "github requires setup, connect, or status")
	}
	input := map[string]string{}
	if args[0] == "setup" {
		input["public_url"] = *publicURL
		input["organization"] = *organization
	}
	data, err := a.call(ctx, "POST", "/v1/github/"+args[0], "", input)
	if err != nil {
		return nil, err
	}
	var result struct {
		URL string `json:"url"`
	}
	if err = json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	// Machine-readable calls never launch UI. Linux server users open the printed
	// link on their laptop; macOS users get the browser directly.
	if !*noBrowser && !a.opts.JSON && runtime.GOOS == "darwin" {
		if err = exec.CommandContext(ctx, "open", result.URL).Run(); err != nil {
			fmt.Fprintln(a.errOut, "Open the returned URL in your browser.")
		}
	}
	return data, nil
}
