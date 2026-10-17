// Package api is the REST layer: it mounts every operation registered in
// kernel.APIRegistry under /api/v1 (with scope enforcement, panic recovery
// and a JSON default content type), serves the generated OpenAPI 3.1
// document at /api/v1/openapi.json and the Scalar reference at /api/docs,
// and provides the core operations (/site, /me, /health) plus helpers
// (pagination, decoding, error envelopes) for other modules' handlers.
package api

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"runtime/debug"
	"slices"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

// Module is the api kernel.Module.
type Module struct{}

// New returns the api module.
func New() *Module { return &Module{} }

// Name implements kernel.Module.
func (*Module) Name() string { return "api" }

// Migrations implements kernel.Module (the api module has no tables).
func (*Module) Migrations() fs.FS { return nil }

// Register adds the core operations and installs the routes.
//
// Mounting is lazy: modules after api in BootOrder (admin, commerce,
// wpmigrate, mcp, ext) register operations after this Register returns, so
// one catch-all handler on /api/v1/* builds the operation router from
// d.API.Operations() on the first request (sync.Once) and never rebuilds
// it; operations added after serving starts are ignored. The OpenAPI
// document is built from the same snapshot, also once.
func (*Module) Register(d *kernel.Deps) error {
	logger.Store(d.Log)
	registerCoreOps(d)
	s := &server{deps: d}
	// /api/v1/openapi.json and /api/docs are in auth.AnonymousAPIPaths.
	d.Router.API.Get("/v1/openapi.json", s.openapi)
	d.Router.API.Get("/docs", s.docsPage)
	d.Router.API.Handle("/v1/*", http.HandlerFunc(s.serveOps))
	return nil
}

type server struct {
	deps *kernel.Deps

	once sync.Once
	mux  http.Handler
	ops  []kernel.APIOperation // successfully mounted

	docOnce sync.Once
	doc     []byte
	docErr  error
}

func (s *server) serveOps(w http.ResponseWriter, r *http.Request) {
	s.once.Do(s.build)
	s.mux.ServeHTTP(w, r)
}

// build mounts every registered operation. s.mux is first set to a
// fallback answering the 500 envelope, so a panic here never leaves a nil
// handler (the panic is logged; the API stays down but answers cleanly).
// An operation chi rejects (e.g. a conflicting pattern) is logged and
// skipped so one bad registration cannot take the whole API down. Two
// operations equal after /v1 normalization fail closed: the first handler
// is mounted with the union of both scope lists. Every GET also answers
// HEAD (body discarded) unless a HEAD operation is registered, and 405
// responses carry an Allow header.
func (s *server) build() {
	s.mux = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		WriteError(w, http.StatusInternalServerError, CodeInternal, "internal server error", nil)
	})
	defer func() {
		if p := recover(); p != nil {
			s.ops = nil
			log().Error("api: building the operation router failed", "err", fmt.Sprint(p), "stack", string(debug.Stack()))
		}
	}()
	mux := chi.NewRouter()
	mux.NotFound(func(w http.ResponseWriter, _ *http.Request) { NotFound(w) })
	mux.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		if allow := allowedMethods(mux, r); allow != "" {
			w.Header().Set("Allow", allow)
		}
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed", nil)
	})
	ops := s.mergedOps()
	explicitHead := map[string]bool{}
	for _, op := range ops {
		if op.Method == http.MethodHead {
			explicitHead[op.Path] = true
		}
	}
	var mounted []kernel.APIOperation
	for _, op := range ops {
		if err := mount(mux, op.Method, op.Path, s.guard(op)); err != nil {
			s.deps.Log.Error("api: operation not mounted", "op", op.Method+" "+op.Path, "err", err)
			continue
		}
		if op.Method == http.MethodGet && !explicitHead[op.Path] {
			if err := mount(mux, http.MethodHead, op.Path, headOnly(s.guard(op))); err != nil {
				s.deps.Log.Error("api: HEAD not mounted", "op", op.Method+" "+op.Path, "err", err)
			}
		}
		mounted = append(mounted, op)
	}
	s.ops, s.mux = mounted, mux
}

// mergedOps returns the registered operations, normalized, without
// handler-less ones, with duplicates (same method + normalized path)
// folded into the first registration with the union of their scopes.
func (s *server) mergedOps() []kernel.APIOperation {
	var out []kernel.APIOperation
	index := map[string]int{}
	for _, op := range s.deps.API.Operations() {
		op.Method, op.Path = strings.ToUpper(op.Method), normalizePath(op.Path)
		key := op.Method + " " + op.Path
		if op.Handler == nil {
			s.deps.Log.Error("api: operation without handler skipped", "op", key)
			continue
		}
		if i, dup := index[key]; dup {
			first := &out[i]
			scopes := slices.Clone(first.Scopes)
			for _, sc := range op.Scopes {
				if !slices.Contains(scopes, sc) {
					scopes = append(scopes, sc)
				}
			}
			first.Scopes = scopes
			s.deps.Log.Error("api: duplicate operation; first handler kept with the union of scopes", "op", key, "scopes", scopes)
			continue
		}
		index[key] = len(out)
		out = append(out, op)
	}
	return out
}

// allowedMethods lists the methods routed for the request path.
func allowedMethods(mux *chi.Mux, r *http.Request) string {
	path := r.URL.Path
	if rctx := chi.RouteContext(r.Context()); rctx != nil && rctx.RoutePath != "" {
		path = rctx.RoutePath
	} else {
		path = strings.TrimPrefix(path, "/api")
	}
	var allow []string
	for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions} {
		if mux.Match(chi.NewRouteContext(), m, path) {
			allow = append(allow, m)
		}
	}
	return strings.Join(allow, ", ")
}

// headOnly runs h with the response body discarded.
func headOnly(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(&headWriter{ResponseWriter: w}, r)
	})
}

type headWriter struct{ http.ResponseWriter }

func (h *headWriter) Write(b []byte) (int, error) { return len(b), nil }
func (h *headWriter) Unwrap() http.ResponseWriter { return h.ResponseWriter }

func mount(mux chi.Router, method, path string, h http.Handler) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("%v", p)
		}
	}()
	mux.Method(method, path, h)
	return nil
}

// guard wraps an operation: scope enforcement (401 anonymous, 403 missing
// scope), panic recovery (500 envelope, stack only in the log) and a JSON
// default Content-Type.
func (s *server) guard(op kernel.APIOperation) http.Handler {
	name := op.Method + " " + op.Path
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gw := &guardWriter{ResponseWriter: w}
		defer func() {
			p := recover()
			if p == nil {
				return
			}
			if p == http.ErrAbortHandler {
				panic(p)
			}
			log().Error("api: panic", "op", name, "path", httpx.RedactPath(r.URL.Path), "err", fmt.Sprint(p),
				"request_id", httpx.RequestIDFromContext(r.Context()), "stack", string(debug.Stack()))
			if !gw.wrote {
				WriteError(w, http.StatusInternalServerError, CodeInternal, "internal server error", nil)
			}
		}()
		for _, scope := range op.Scopes {
			if auth.Can(r.Context(), scope) {
				continue
			}
			if auth.UserFromContext(r.Context()) == nil {
				WriteError(w, http.StatusUnauthorized, CodeUnauthorized, "API key or session required", nil)
				return
			}
			WriteError(w, http.StatusForbidden, CodeForbidden, "missing scope: "+scope, nil)
			return
		}
		gw.Header().Set("Content-Type", "application/json; charset=utf-8")
		op.Handler(gw, r)
	})
}

// guardWriter records whether the response has started.
type guardWriter struct {
	http.ResponseWriter
	wrote bool
}

func (g *guardWriter) WriteHeader(code int) {
	g.wrote = true
	g.ResponseWriter.WriteHeader(code)
}

func (g *guardWriter) Write(b []byte) (int, error) {
	g.wrote = true
	return g.ResponseWriter.Write(b)
}

// Flush keeps streaming handlers working through the guard.
func (g *guardWriter) Flush() {
	g.wrote = true
	_ = http.NewResponseController(g.ResponseWriter).Flush()
}

// Unwrap exposes the underlying writer to http.ResponseController.
func (g *guardWriter) Unwrap() http.ResponseWriter { return g.ResponseWriter }

// baseURL is the public base URL (kernel.BaseURL).
func (s *server) baseURL(r *http.Request) string { return kernel.BaseURL(r.Context(), s.deps) }

// openapi serves the OpenAPI document, built once from the mounted
// operations.
func (s *server) openapi(w http.ResponseWriter, r *http.Request) {
	s.once.Do(s.build)
	s.docOnce.Do(func() { s.doc, s.docErr = json.Marshal(buildOpenAPI(s.ops, s.baseURL(r))) })
	if s.docErr != nil {
		log().Error("api: openapi document", "err", s.docErr)
		WriteError(w, http.StatusInternalServerError, CodeInternal, "internal server error", nil)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(s.doc)
}
