package git

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func TestGitAuthenticationScope(t *testing.T) {
	calls := 0
	provider := func(context.Context, string) (string, error) { calls++; return "private-token", nil }
	env, err := authentication(context.Background(), "https://github.com/owner/repo.git", []TokenProvider{provider})
	if err != nil || len(env) != 3 || !strings.Contains(env[1], "http.https://github.com/owner/repo.git.extraheader") || !strings.Contains(env[2], base64.StdEncoding.EncodeToString([]byte("x-access-token:private-token"))) {
		t.Fatal("missing scoped auth", err)
	}
	for _, repo := range []string{"https://example.com/repo.git", "https://github.com.evil/owner/repo.git"} {
		env, err = authentication(context.Background(), repo, []TokenProvider{provider})
		if err != nil || len(env) != 0 || calls != 1 {
			t.Fatal("credentials crossed host boundary")
		}
	}
	_, err = authentication(context.Background(), "https://github.com/owner/repo.git", []TokenProvider{func(context.Context, string) (string, error) { return "", errors.New("denied") }})
	if err == nil {
		t.Fatal("credential failure ignored")
	}
}
