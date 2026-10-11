package gateway

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Route struct {
	Hostname   string    `json:"hostname"`
	State      string    `json:"state"`
	Upstream   string    `json:"upstream,omitempty"`
	HealthPath string    `json:"health_path,omitempty"`
	Deadline   time.Time `json:"deadline"`
}
type Gateway struct {
	PublicScheme string
	mu           sync.RWMutex
	routes       map[string]Route
	Token        string
	Now          func() time.Time
}

func New(token string) *Gateway {
	return &Gateway{routes: map[string]Route{}, Token: token, Now: time.Now}
}
func Handler() http.Handler { return New("").Public() }

func (g *Gateway) Control() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if g.Token == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+g.Token)) != 1 {
			http.Error(w, "unauthorized", 401)
			return
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/healthz":
			w.WriteHeader(204)
		case r.Method == "PUT" && r.URL.Path == "/routes":
			var routes []Route
			d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
			d.DisallowUnknownFields()
			if d.Decode(&routes) != nil {
				http.Error(w, "invalid routes", 400)
				return
			}
			var extra any
			if d.Decode(&extra) != io.EOF {
				http.Error(w, "invalid routes", 400)
				return
			}
			next := map[string]Route{}
			for _, v := range routes {
				if v.Hostname == "" || v.Deadline.IsZero() || v.HealthPath != "" && (!strings.HasPrefix(v.HealthPath, "/") || strings.HasPrefix(v.HealthPath, "//") || strings.ContainsAny(v.HealthPath, "\r\n#")) {
					http.Error(w, "invalid route", 400)
					return
				}
				if v.Upstream != "" {
					u, e := url.Parse(v.Upstream)
					if e != nil || u.Scheme != "http" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
						http.Error(w, "invalid upstream", 400)
						return
					}
				}
				next[strings.ToLower(v.Hostname)] = v
			}
			g.mu.Lock()
			g.routes = next
			g.mu.Unlock()
			w.WriteHeader(204)
		case r.Method == "POST" && r.URL.Path == "/probe":
			v, ok := g.lookup(r.URL.Query().Get("host"))
			if !ok || v.Upstream == "" {
				http.Error(w, "unknown upstream", 404)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, "GET", v.Upstream+v.HealthPath, nil)
			if err != nil {
				http.Error(w, "invalid probe", 400)
				return
			}
			req.Host = v.Hostname
			client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Timeout: 2 * time.Second}
			resp, err := client.Do(req)
			if err != nil {
				http.Error(w, "not ready", 503)
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				http.Error(w, "not ready", 503)
				return
			}
			w.WriteHeader(204)
		default:
			http.NotFound(w, r)
		}
	})
}
func (g *Gateway) lookup(host string) (Route, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	v, ok := g.routes[strings.ToLower(host)]
	return v, ok
}
func (g *Gateway) Public() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_deployer/healthz" && r.Method == "GET" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
			return
		}
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		v, ok := g.lookup(host)
		if !ok {
			g.unavailable(w, r, "not_found")
			return
		}
		if !g.Now().Before(v.Deadline) {
			if v.State == "running" || v.State == "stopped" {
				v.State = "expired"
			} else if v.State != "expired" && v.State != "destroyed" {
				v.State = "failed"
			}
		}
		if r.URL.Path == "/_deployer/status" && r.Method == "GET" {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(map[string]any{"state": v.State, "ready": v.State == "running"})
			return
		}
		if r.URL.Path == "/_deployer/wait.js" && r.Method == "GET" {
			w.Header().Set("Content-Type", "text/javascript")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write([]byte(waitJS))
			return
		}
		if v.State != "running" || v.Upstream == "" {
			g.unavailable(w, r, v.State)
			return
		}
		u, _ := url.Parse(v.Upstream)
		proxy := &httputil.ReverseProxy{Rewrite: func(p *httputil.ProxyRequest) {
			p.SetURL(u)
			p.Out.Host = p.In.Host
			p.SetXForwarded()
			if g.PublicScheme != "" {
				p.Out.Header.Set("X-Forwarded-Proto", g.PublicScheme)
			}
		}}
		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			if !g.Now().Before(v.Deadline) {
				g.unavailable(w, r, "expired")
			} else {
				g.unavailable(w, r, "failed")
			}
		}
		ctx, cancel := context.WithDeadline(r.Context(), v.Deadline)
		defer cancel()
		proxy.ServeHTTP(w, r.WithContext(ctx))
	})
}

var page = template.Must(template.New("status").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Preview status</title><style>body{font:17px system-ui,sans-serif;background:#f6f7f9;color:#17202a;margin:0;min-height:100vh;display:grid;place-items:center}main{max-width:440px;padding:40px}small{color:#67717f}h1{font-size:28px;line-height:1.2}a{color:#3157bb}p{line-height:1.6}</style><main><small>PREVIEW</small><h1>{{.Title}}</h1><p id="message" role="status">{{.Message}}</p><a href="">Check again</a></main>{{if .Poll}}<script src="/_deployer/wait.js" defer></script>{{end}}</html>`))

const waitJS = `(function(){async function check(){try{const r=await fetch('/_deployer/status',{cache:'no-store'});if(!r.ok)throw new Error();const s=await r.json();if(s.ready){location.reload();return;}if(['failed','expired','stopped','destroyed','destroying'].includes(s.state)){location.reload();return;}}catch(e){}setTimeout(check,2000);}setTimeout(check,2000);})();`

func (g *Gateway) unavailable(w http.ResponseWriter, r *http.Request, state string) {
	code := 503
	title := "Your preview is starting."
	message := "This page will open automatically when the preview is ready."
	poll := true
	switch state {
	case "queued", "building":
		title = "We're preparing your preview."
	case "stopped":
		title = "This preview is currently offline."
		message = "The owner needs to start it before you can visit."
		poll = false
	case "expired":
		title = "This preview has expired."
		message = "Contact its owner if you need access again."
		poll = false
		code = 410
	case "failed":
		title = "We couldn't open this preview."
		message = "The owner can inspect the deployment logs for details."
		poll = false
	case "destroying", "destroyed", "not_found":
		title = "Preview not found."
		message = "This preview is unavailable."
		poll = false
		code = 404
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if poll {
		w.Header().Set("Retry-After", "2")
	}
	document := r.Method == "GET" && strings.Contains(r.Header.Get("Accept"), "text/html") && (r.Header.Get("Sec-Fetch-Dest") == "" || r.Header.Get("Sec-Fetch-Dest") == "document")
	if document {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'")
		w.WriteHeader(code)
		_ = page.Execute(w, struct {
			Title, Message string
			Poll           bool
		}{title, message, poll})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"state": state, "message": title})
}
