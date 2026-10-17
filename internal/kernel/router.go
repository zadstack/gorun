package kernel

import (
	"net/http"
	"sync"

	"github.com/go-chi/chi/v5"
)

// lateRouter is a chi.Router whose Use may be called at any point before the
// first request, even after routes were added, so a module later in
// BootOrder (e.g. redirects) can wrap routes an earlier module (e.g. media)
// already registered. Middlewares run in Use order, before routing, around
// every route of the router. Use after serving has started panics.
type lateRouter struct {
	chi.Router
	mu      sync.Mutex // guards mws and serving
	mws     []func(http.Handler) http.Handler
	serving bool
	once    sync.Once
	handler http.Handler // built once, on the first request
}

func newLateRouter() *lateRouter { return &lateRouter{Router: chi.NewRouter()} }

// Use appends middlewares; they take effect when the router first serves.
func (l *lateRouter) Use(mws ...func(http.Handler) http.Handler) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.serving {
		panic("kernel: Router.Use called after the router started serving; call Use during Register")
	}
	l.mws = append(l.mws, mws...)
}

func (l *lateRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	l.once.Do(func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.serving = true
		l.handler = chi.Chain(l.mws...).Handler(l.Router)
	})
	l.handler.ServeHTTP(w, r)
}
