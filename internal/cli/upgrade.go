package cli

import (
	"context"
	"deployer/internal/install"
	"deployer/internal/version"
)

func (a *app) upgrade(ctx context.Context, args []string) (any, error) {
	f := a.flags("upgrade")
	bundle := f.String("bundle", "", "directory containing new release binaries")
	dry := f.Bool("dry-run", false, "show upgrade plan")
	if err := parse(f, args); err != nil {
		return nil, err
	}
	return install.New(a.errOut).Upgrade(ctx, *bundle, version.Version, *dry)
}
