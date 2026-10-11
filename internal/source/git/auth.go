package git

import (
	"context"
	"deployer/internal/githubapp"
	"encoding/base64"
)

type TokenProvider func(context.Context, string) (string, error)

// Scope the header to the exact repository URL. Never place credentials in
// arguments, repository config, files, build context, or command error output.
func authentication(ctx context.Context, repository string, providers []TokenProvider) ([]string, error) {
	if len(providers) == 0 || providers[0] == nil {
		return nil, nil
	}
	if _, _, ok := githubapp.Repository(repository); !ok {
		return nil, nil
	}
	token, err := providers[0](ctx, repository)
	if err != nil {
		return nil, err
	}
	if token == "" {
		return nil, nil
	}
	header := "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:"+token))
	return []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http." + repository + ".extraheader", "GIT_CONFIG_VALUE_0=" + header}, nil
}
func CheckoutWithAuth(provider TokenProvider) func(context.Context, string, string, string, string) (string, error) {
	return func(ctx context.Context, repository, ref, pinned, destination string) (string, error) {
		return checkoutPublic(ctx, repository, ref, pinned, destination, provider)
	}
}
