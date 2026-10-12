package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client calls a gateway's control API on behalf of the controller.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// NewClient returns a control API client.
func NewClient(baseURL, token string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Token: token, HTTP: &http.Client{Timeout: 10 * time.Second}}
}

// Publish replaces the gateway's route table.
func (c *Client) Publish(ctx context.Context, routes []Route) error {
	if routes == nil {
		routes = []Route{}
	}
	body, err := json.Marshal(routes)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPut, "/routes", body)
}

// Probe runs one health check against the upstream registered for host.
func (c *Client) Probe(ctx context.Context, host string) error {
	return c.do(ctx, http.MethodPost, "/probe?host="+url.QueryEscape(host), nil)
}

// Healthy reports whether the control API answers.
func (c *Client) Healthy(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/healthz", nil)
}

func (c *Client) do(ctx context.Context, method, path string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("gateway control API: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("gateway control API: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}
