package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"deployer/internal/domain"
)

type Client struct {
	URL   string
	Token string
	HTTP  *http.Client
}
type Result struct {
	SchemaVersion int             `json:"schema_version"`
	Data          json.RawMessage `json:"data,omitempty"`
	Error         *domain.Error   `json:"error,omitempty"`
	RequestID     string          `json:"request_id,omitempty"`
}

func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
		return fmt.Errorf("server URL must contain only a scheme and host/port")
	}
	if u.Scheme == "https" {
		return nil
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme == "http" && (u.Hostname() == "localhost" || ip != nil && ip.IsLoopback()) {
		return nil
	}
	return fmt.Errorf("unencrypted API access is only allowed on loopback; use SSH tunneling or HTTPS")
}
func New(raw, token string) (*Client, error) {
	if err := ValidateURL(raw); err != nil {
		return nil, err
	}
	return &Client{URL: strings.TrimRight(raw, "/"), Token: token, HTTP: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) Do(ctx context.Context, method, path, key string, input any) (Result, error) {
	var result Result
	var body io.Reader
	if input != nil {
		b, err := json.Marshal(input)
		if err != nil {
			return result, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.URL+path, body)
	if err != nil {
		return result, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return result, fmt.Errorf("cannot reach Deployer API: %w", err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&result); err != nil {
		return result, fmt.Errorf("server returned an invalid API response (HTTP %d)", resp.StatusCode)
	}
	if result.SchemaVersion != 1 {
		return result, fmt.Errorf("unsupported API schema version %d", result.SchemaVersion)
	}
	if result.Error != nil {
		return result, result.Error
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return result, fmt.Errorf("API request failed (HTTP %d)", resp.StatusCode)
	}
	return result, nil
}
