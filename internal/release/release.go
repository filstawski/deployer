// Package release downloads Deployer release binaries from GitHub and
// verifies them against the release's SHA256SUMS.
package release

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// DefaultURL is where releases are published.
const DefaultURL = "https://github.com/filstawski/deployer/releases"

var versionPattern = regexp.MustCompile(`^v\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`)

// ValidVersion reports whether v names a release, such as v1.2.3.
func ValidVersion(v string) bool { return versionPattern.MatchString(v) }

// Asset returns the file name of a binary in a release.
func Asset(binary, version, goos, goarch string) string {
	name := fmt.Sprintf("%s_%s_%s_%s", binary, version, goos, goarch)
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// Client fetches releases.
type Client struct {
	url  string
	http *http.Client
}

// New returns a client for the releases at baseURL, or DefaultURL if empty.
func New(baseURL string) *Client {
	if baseURL == "" {
		baseURL = DefaultURL
	}
	secure := strings.HasPrefix(baseURL, "https://")
	return &Client{url: strings.TrimRight(baseURL, "/"), http: &http.Client{
		Timeout: 10 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if secure && req.URL.Scheme != "https" || len(via) > 5 {
				return fmt.Errorf("refusing redirect to %s", req.URL.Redacted())
			}
			return nil
		},
	}}
}

// Latest returns the version of the latest stable release.
func (c *Client) Latest(ctx context.Context) (string, error) {
	body, err := c.get(ctx, c.url+"/latest/download/VERSION", 64)
	if err != nil {
		return "", err
	}
	v := strings.TrimSpace(string(body))
	if !ValidVersion(v) {
		return "", fmt.Errorf("the latest release reports an invalid version %q", v)
	}
	return v, nil
}

// Download fetches assets of a release, maps of asset name to destination
// path, and verifies each against SHA256SUMS. Each destination is replaced
// atomically with an executable file, and only after every checksum matched.
func (c *Client) Download(ctx context.Context, version string, assets map[string]string) error {
	if !ValidVersion(version) {
		return fmt.Errorf("invalid version %q (expected something like v1.2.3)", version)
	}
	base := c.url + "/download/" + url.PathEscape(version)
	sums, err := c.get(ctx, base+"/SHA256SUMS", 64<<10)
	if err != nil {
		return err
	}
	expected := parseSums(sums)
	staged := map[string]string{} // temp file → destination
	defer func() {
		for tmp := range staged {
			os.Remove(tmp)
		}
	}()
	for asset, dest := range assets {
		want, ok := expected[asset]
		if !ok {
			return fmt.Errorf("release %s has no %s", version, asset)
		}
		tmp, err := c.fetch(ctx, base+"/"+url.PathEscape(asset), filepath.Dir(dest), want)
		if err != nil {
			return fmt.Errorf("%s: %w", asset, err)
		}
		staged[tmp] = dest
	}
	for tmp, dest := range staged {
		if err := replace(tmp, dest); err != nil {
			return err
		}
		delete(staged, tmp)
	}
	return nil
}

// replace moves a verified download to dest. Windows refuses to replace a
// running program but lets it be renamed, so there the old file is moved
// aside, to dest.old. It can't be removed while it runs; RemoveReplaced
// removes it later.
func replace(tmp, dest string) error {
	if runtime.GOOS != "windows" {
		return os.Rename(tmp, dest)
	}
	RemoveReplaced(dest)
	old := dest + ".old"
	if _, err := os.Lstat(old); err == nil { // A version from before runs from it.
		old += "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	if err := os.Rename(dest, old); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		os.Rename(old, dest)
		return err
	}
	os.Remove(old) // Fails while it runs.
	return nil
}

// RemoveReplaced removes the versions of a program that replaced itself on
// Windows, once they no longer run.
func RemoveReplaced(path string) {
	if runtime.GOOS != "windows" {
		return
	}
	dir, prefix := filepath.Dir(path), filepath.Base(path)+".old"
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// fetch downloads url into a new file in dir and checks its SHA-256.
func (c *Client) fetch(ctx context.Context, url, dir, wantSum string) (string, error) {
	resp, err := c.request(ctx, url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	f, err := os.CreateTemp(dir, ".deployer-download-*")
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, hash), resp.Body)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil && hex.EncodeToString(hash.Sum(nil)) != wantSum {
		err = errors.New("checksum mismatch; the download is corrupt or was tampered with")
	}
	if err == nil {
		err = os.Chmod(f.Name(), 0o755)
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func (c *Client) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	resp, err := c.request(ctx, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

func (c *Client) request(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	return resp, nil
}

// parseSums reads sha256sum output: "<hex>  <name>" or "<hex> *<name>".
func parseSums(data []byte) map[string]string {
	sums := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && len(fields[0]) == 64 {
			sums[strings.TrimPrefix(fields[1], "*")] = fields[0]
		}
	}
	return sums
}
