package gateway

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

// Reserved paths under /_deployer/ are served by the gateway on every preview
// hostname; applications cannot use them.
const (
	pathHealth = "/_deployer/health"
	pathStatus = "/_deployer/status"
	pathWaitJS = "/_deployer/wait.js"
)

// Options configures a gateway.
type Options struct {
	// ControlToken authenticates the controller on the control API.
	ControlToken string
	// PublicScheme is sent to applications as X-Forwarded-Proto, because TLS
	// terminates in front of the gateway.
	PublicScheme string
	// Now overrides the clock in tests.
	Now func() time.Time
}

// Gateway serves preview traffic. Create one with New.
type Gateway struct {
	opts   Options
	routes atomic.Pointer[map[string]Route]
	proxy  *httputil.ReverseProxy
	probe  *http.Client
}

type routeKey struct{}

// New returns a gateway with no routes. Until the first snapshot arrives,
// every preview reports that it is temporarily unavailable.
func New(opts Options) *Gateway {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	g := &Gateway{opts: opts}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 5 * time.Minute,
	}
	g.proxy = &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(p *httputil.ProxyRequest) {
			route := p.In.Context().Value(routeKey{}).(Route)
			target, _ := url.Parse(route.Upstream)
			p.SetURL(target)
			p.Out.Host = p.In.Host
			// The gateway is reachable only from the local reverse proxy, so
			// the client address it reports is trustworthy.
			p.Out.Header["X-Forwarded-For"] = p.In.Header["X-Forwarded-For"]
			p.SetXForwarded()
			if opts.PublicScheme != "" {
				p.Out.Header.Set("X-Forwarded-Proto", opts.PublicScheme)
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			if resp.Header.Get("X-Robots-Tag") == "" {
				resp.Header.Set("X-Robots-Tag", "noindex, nofollow")
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if r.Context().Err() == context.DeadlineExceeded {
				renderStatus(w, r, pageExpired)
				return
			}
			renderStatus(w, r, pageUnreachable)
		},
	}
	g.probe = &http.Client{
		Transport:     transport,
		Timeout:       3 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return g
}

// SetRoutes replaces the route table.
func (g *Gateway) SetRoutes(routes []Route) error {
	next := make(map[string]Route, len(routes))
	for _, r := range routes {
		if err := r.validate(); err != nil {
			return err
		}
		next[r.Host] = r
	}
	g.routes.Store(&next)
	return nil
}

func (g *Gateway) lookup(host string) (Route, bool, bool) {
	table := g.routes.Load()
	if table == nil {
		return Route{}, false, false
	}
	r, ok := (*table)[host]
	return r, ok, true
}

// Public returns the handler for visitor traffic.
func (g *Gateway) Public() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == pathHealth {
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
			return
		}
		if r.URL.Path == pathWaitJS {
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			io.WriteString(w, waitJS)
			return
		}
		host := strings.ToLower(r.Host)
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		now := g.opts.Now()
		route, found, loaded := g.lookup(host)
		page := pageFor(route.visibleState(now))
		switch {
		case !loaded:
			page = pageStarting
		case !found:
			page = pageNotFound
		case page == pageReady && route.Upstream == "":
			page = pageStarting
		}
		if r.URL.Path == pathStatus {
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, http.StatusOK, map[string]any{"state": page.state, "ready": page == pageReady})
			return
		}
		if page != pageReady {
			renderStatus(w, r, page)
			return
		}
		ctx := context.WithValue(r.Context(), routeKey{}, route)
		if route.ExpiresAt != nil {
			// Long-lived requests and WebSockets end when the lease does.
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, route.ExpiresAt.Sub(now))
			defer cancel()
		}
		g.proxy.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Control returns the handler for the controller's control API.
func (g *Gateway) Control() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("PUT /routes", func(w http.ResponseWriter, r *http.Request) {
		var routes []Route
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&routes); err != nil {
			http.Error(w, "invalid route snapshot: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := g.SetRoutes(routes); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /probe", func(w http.ResponseWriter, r *http.Request) {
		if err := g.check(r.Context(), r.URL.Query().Get("host")); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := "Bearer " + g.opts.ControlToken
		if g.opts.ControlToken == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte(want)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// check performs one health check against a route's upstream. Any response
// below 400 counts as healthy.
func (g *Gateway) check(ctx context.Context, host string) error {
	route, found, _ := g.lookup(host)
	if !found || route.Upstream == "" {
		return errString("no upstream is registered for " + host)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, route.Upstream+route.HealthPath, nil)
	if err != nil {
		return err
	}
	req.Host = host
	req.Header.Set("User-Agent", "deployer-healthcheck")
	resp, err := g.probe.Do(req)
	if err != nil {
		return errString("connection failed; the app must listen on 0.0.0.0 and the configured port")
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 400 {
		return errString(route.HealthPath + " returned HTTP " + http.StatusText(resp.StatusCode))
	}
	return nil
}

type errString string

func (e errString) Error() string { return string(e) }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
