// Package api serves the authenticated management API under /v1.
//
// Every response is a JSON envelope: {"data": ...} on success and
// {"error": {"code", "message", "hint"}} on failure, with a request_id that
// also appears in the server log.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/filstawski/deployer/internal/config"
	"github.com/filstawski/deployer/internal/controller"
	"github.com/filstawski/deployer/internal/domain"
	"github.com/filstawski/deployer/internal/github"
	"github.com/filstawski/deployer/internal/store"
)

// Deps are the API's collaborators.
type Deps struct {
	Store      *store.Store
	Controller *controller.Controller
	GitHub     *github.App
	Config     config.Server
	Log        *slog.Logger
	Version    string
}

// Server is the management API.
type Server struct {
	Deps
	mux *http.ServeMux
}

// New returns an API server.
func New(d Deps) *Server {
	s := &Server{Deps: d, mux: http.NewServeMux()}
	s.routes()
	return s
}

// request is an authenticated API request.
type request struct {
	*http.Request
	token domain.Token
	id    string
}

// handler returns a status code and response data, or an error.
type handler func(r *request) (int, any, error)

// handle registers an authenticated route that requires scope. An empty
// scope admits any valid token; the handler then checks access itself.
func (s *Server) handle(pattern string, scope domain.Scope, h handler) {
	s.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		req := &request{Request: r, id: requestID(r)}
		token, err := s.authenticate(r)
		if err == nil && scope != "" && !token.Has(scope) {
			err = domain.Errorf(domain.CodeForbidden, "this token lacks the %s scope", scope)
		}
		if err != nil {
			s.writeError(w, req, err)
			return
		}
		req.token = token
		status, data, err := h(req)
		if err != nil {
			s.writeError(w, req, err)
			return
		}
		s.write(w, req, status, data)
	})
}

// Handler returns the API's HTTP handler.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := domain.NewID(domain.PrefixRequest)
		r = r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id))
		h := w.Header()
		h.Set("X-Request-ID", id)
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			if v := recover(); v != nil {
				s.Log.Error("panic serving request", "request_id", id, "panic", v)
				s.writeError(rec, &request{Request: r, id: id}, errors.New("panic"))
			}
			// Reads are frequent while clients wait, so only writes and errors
			// are logged at the default level.
			level := slog.LevelInfo
			if r.Method == http.MethodGet && rec.status < 500 {
				level = slog.LevelDebug
			}
			s.Log.Log(r.Context(), level, "request", "method", r.Method, "path", r.URL.Path, "status", rec.status, "duration_ms", time.Since(start).Milliseconds(), "request_id", id)
		}()
		s.mux.ServeHTTP(rec, r)
	})
}

type requestIDKey struct{}

func requestID(r *http.Request) string {
	id, _ := r.Context().Value(requestIDKey{}).(string)
	return id
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (s *Server) authenticate(r *http.Request) (domain.Token, error) {
	secret, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return domain.Token{}, domain.Errorf(domain.CodeUnauthenticated, "an API token is required (Authorization: Bearer dpl_...)")
	}
	return s.Store.Authenticate(r.Context(), strings.TrimSpace(secret))
}

func (s *Server) write(w http.ResponseWriter, r *request, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(domain.Envelope[any]{Data: data, RequestID: r.id})
}

func (s *Server) writeError(w http.ResponseWriter, r *request, err error) {
	e, ok := domain.AsError(err)
	if !ok {
		s.Log.Error("request failed", "request_id", r.id, "error", err)
		e = domain.Errorf(domain.CodeInternal, "internal server error (request %s)", r.id)
	}
	status := statusFor(e.Code)
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Bearer realm="deployer"`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(domain.Envelope[any]{Error: e, RequestID: r.id})
}

// statusFor maps error codes to HTTP status codes.
func statusFor(code domain.Code) int {
	switch code {
	case domain.CodeUnauthenticated:
		return http.StatusUnauthorized
	case domain.CodeForbidden:
		return http.StatusForbidden
	case domain.CodeNotFound:
		return http.StatusNotFound
	case domain.CodeAlreadyExists, domain.CodeNameTaken, domain.CodeIdempotencyConflict,
		domain.CodeInvalidState, domain.CodeBusy, domain.CodeGitHubNotConfigured:
		return http.StatusConflict
	case domain.CodeQuotaExceeded:
		return http.StatusTooManyRequests
	case domain.CodeUnavailable:
		return http.StatusServiceUnavailable
	case domain.CodeGitHubFailed:
		return http.StatusBadGateway
	case domain.CodeInternal:
		return http.StatusInternalServerError
	default:
		return http.StatusBadRequest
	}
}

// decode reads a JSON object body into v. An empty body leaves v unchanged.
func decode(r *request, v any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10+1))
	if err != nil {
		return domain.Errorf(domain.CodeInvalidInput, "could not read the request body")
	}
	if len(body) > 64<<10 {
		return domain.Errorf(domain.CodeInvalidInput, "the request body is larger than 64 KiB")
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return domain.Errorf(domain.CodeInvalidInput, "invalid JSON body: %v", err)
	}
	if dec.More() {
		return domain.Errorf(domain.CodeInvalidInput, "the body must contain a single JSON object")
	}
	return nil
}
