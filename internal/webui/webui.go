// Package webui renders the small standalone HTML pages Deployer serves to
// browsers: preview status pages and the GitHub connection flow.
package webui

import (
	"html/template"
	"io"
)

// Icon selects the page's status indicator.
type Icon string

const (
	IconNone    Icon = ""
	IconSpinner Icon = "spinner"
	IconMuted   Icon = "muted"
	IconOK      Icon = "ok"
	IconWarn    Icon = "warn"
	IconError   Icon = "error"
)

// Page is the content of one page.
type Page struct {
	Title   string
	Icon    Icon
	Message string
	// Body is trusted HTML placed after the message.
	Body   template.HTML
	Footer string
	// Script is the path of a same-origin script to load.
	Script string
}

// CSP is a Content-Security-Policy that permits exactly what pages need.
// formAction lists extra allowed form targets.
func CSP(formAction string) string {
	policy := "default-src 'none'; style-src 'unsafe-inline'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'"
	if formAction != "" {
		policy += "; form-action " + formAction
	}
	return policy
}

// Render writes p as a complete HTML document.
func Render(w io.Writer, p Page) error { return page.Execute(w, p) }

var page = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<title>{{.Title}}</title>
<style>
:root{color-scheme:light dark;--bg:#f6f6f7;--fg:#18181b;--muted:#6b6b76;--card:#fff;--line:#e5e5e9;--accent:#2563eb;--ok:#16a34a;--warn:#d97706;--error:#dc2626}
@media (prefers-color-scheme:dark){:root{--bg:#0c0c0e;--fg:#f4f4f5;--muted:#a1a1aa;--card:#16161a;--line:#2a2a30;--accent:#60a5fa;--ok:#4ade80;--warn:#fbbf24;--error:#f87171}}
*{box-sizing:border-box}
body{margin:0;min-height:100vh;display:grid;place-items:center;padding:24px;background:var(--bg);color:var(--fg);font:16px/1.55 system-ui,-apple-system,"Segoe UI",Roboto,sans-serif}
main{width:100%;max-width:440px;padding:32px;background:var(--card);border:1px solid var(--line);border-radius:16px}
.icon{width:40px;height:40px;margin-bottom:22px;border-radius:50%}
.spinner{border:3px solid var(--line);border-top-color:var(--accent);animation:spin .9s linear infinite}
@keyframes spin{to{transform:rotate(360deg)}}
@media (prefers-reduced-motion:reduce){.spinner{animation-duration:3s}}
.dot{display:grid;place-items:center;background:color-mix(in srgb,var(--tone) 16%,transparent)}
.dot::after{content:"";width:14px;height:14px;border-radius:50%;background:var(--tone)}
.muted{--tone:var(--muted)}.ok{--tone:var(--ok)}.warn{--tone:var(--warn)}.error{--tone:var(--error)}
h1{margin:0 0 6px;font-size:20px;line-height:1.3;font-weight:600}
p{margin:0 0 22px;color:var(--muted)}
ul{margin:0 0 22px;padding-left:20px;color:var(--muted)}
a{color:var(--accent);font-weight:500;text-decoration:none}
a:hover{text-decoration:underline}
button{font:inherit;font-weight:600;color:#fff;background:var(--accent);border:0;border-radius:10px;padding:10px 18px;cursor:pointer}
footer{margin-top:26px;padding-top:16px;border-top:1px solid var(--line);color:var(--muted);font-size:13px;overflow-wrap:anywhere}
</style>
</head>
<body>
<main>
{{if eq .Icon "spinner"}}<div class="icon spinner" aria-hidden="true"></div>{{else if .Icon}}<div class="icon dot {{.Icon}}" aria-hidden="true"></div>{{end}}
<h1>{{.Title}}</h1>
{{if .Message}}<p role="status">{{.Message}}</p>{{end}}
{{.Body}}
{{if .Footer}}<footer>{{.Footer}}</footer>{{end}}
</main>
{{if .Script}}<script src="{{.Script}}" defer></script>{{end}}
</body>
</html>
`))
