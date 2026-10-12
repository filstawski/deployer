package gateway

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/filstawski/deployer/internal/domain"
	"github.com/filstawski/deployer/internal/webui"
)

// page is a status page shown instead of the application.
type page struct {
	state   string
	status  int
	title   string
	message string
	tone    string // a webui icon: muted, warn, or error; ignored while busy
	busy    bool   // shows a spinner and polls until the state changes
}

var (
	pageReady    = &page{state: "running", status: http.StatusOK}
	pageBuilding = &page{state: "building", status: http.StatusServiceUnavailable, busy: true,
		title: "Preparing your preview", message: "This page opens automatically as soon as it's ready."}
	pageStarting = &page{state: "starting", status: http.StatusServiceUnavailable, busy: true,
		title: "Your preview is starting", message: "This page opens automatically as soon as it's ready."}
	pageStopped = &page{state: "stopped", status: http.StatusServiceUnavailable, tone: "muted",
		title: "This preview is offline", message: "Its owner has paused it. Check back later."}
	pageExpired = &page{state: "expired", status: http.StatusGone, tone: "warn",
		title: "This preview has expired", message: "Ask its owner for a new link."}
	pageFailed = &page{state: "failed", status: http.StatusServiceUnavailable, tone: "error",
		title: "This preview couldn't start", message: "Its owner can see what went wrong in the deployment logs."}
	pageNotFound = &page{state: "not_found", status: http.StatusNotFound, tone: "muted",
		title: "Preview not found", message: "Check the address, or ask for a new link."}
	pageUnreachable = &page{state: "unreachable", status: http.StatusBadGateway, tone: "error",
		title: "This preview isn't responding", message: "The app didn't answer. Try again in a moment."}
)

func pageFor(state domain.State) *page {
	switch state {
	case domain.StateRunning:
		return pageReady
	case domain.StateQueued, domain.StateBuilding:
		return pageBuilding
	case domain.StateStarting:
		return pageStarting
	case domain.StateStopped:
		return pageStopped
	case domain.StateExpired:
		return pageExpired
	case domain.StateFailed:
		return pageFailed
	default:
		return pageNotFound
	}
}

// renderStatus writes a status page for browser navigations and a small JSON
// body for everything else, so API clients and asset requests never receive
// an HTML document with a misleading status.
func renderStatus(w http.ResponseWriter, r *http.Request, p *page) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("X-Robots-Tag", "noindex, nofollow")
	h.Set("X-Content-Type-Options", "nosniff")
	if p.busy {
		h.Set("Retry-After", strconv.Itoa(5))
	}
	if !isDocumentRequest(r) {
		writeJSON(w, p.status, map[string]string{"state": p.state, "message": p.title})
		return
	}
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Content-Security-Policy", webui.CSP(""))
	w.WriteHeader(p.status)
	if r.Method == http.MethodHead {
		return
	}
	host := r.Host
	if i := strings.LastIndexByte(host, ':'); i > 0 && !strings.Contains(host[i:], "]") {
		host = host[:i]
	}
	view := webui.Page{Title: p.title, Message: p.message, Icon: webui.Icon(p.tone), Body: `<a href="">Check again</a>`, Footer: host}
	if p.busy {
		view.Icon, view.Script = webui.IconSpinner, pathWaitJS
	}
	webui.Render(w, view)
}

// isDocumentRequest reports whether a browser is navigating to a page.
func isDocumentRequest(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if dest := r.Header.Get("Sec-Fetch-Dest"); dest != "" && dest != "document" {
		return false
	}
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// waitJS polls the gateway's status endpoint and reloads the original URL,
// including its path and query, once the preview is ready or has reached a
// state with its own page.
const waitJS = `(function () {
  var delay = 2000;
  var final = ["running", "stopped", "expired", "failed", "not_found"];
  function schedule() { setTimeout(check, delay); delay = Math.min(delay * 1.3, 15000); }
  function check() {
    fetch("/_deployer/status", { cache: "no-store" })
      .then(function (r) { return r.json(); })
      .then(function (s) { if (s.ready || final.indexOf(s.state) >= 0) { location.reload(); } else { schedule(); } })
      .catch(schedule);
  }
  schedule();
})();
`
