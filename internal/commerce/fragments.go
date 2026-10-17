package commerce

import (
	"bytes"
	"context"
	"html/template"
	"sync"
	"time"

	"github.com/zadstack/gorun/internal/i18n"
)

// statusTmpl is the order-status block on /checkout/success and the HTMX
// poll response of /checkout/status/{id}. While the order is not final it
// re-polls itself every 3s (outerHTML swap); the final block has no trigger.
var statusTmpl = template.Must(template.New("status").Parse(`<div id="gp-order-status" role="status" aria-live="polite" data-status="{{.V.Status}}"
{{- if not .V.Final}} hx-get="/checkout/status/{{.V.ID}}" hx-trigger="every 3s" hx-swap="outerHTML"{{end}}
 class="rounded border {{if .V.Active}}border-green bg-green-soft{{else if .V.Failed}}border-red bg-red-soft{{else}}border-line bg-panel{{end}} p-4">
<p class="font-bold">{{.Label}}</p>
<p class="mt-1 text-muted">{{.Help}}</p>
{{- if .V.Active}}
<a href="/account" class="mt-3 inline-block rounded bg-green px-4 py-2 font-medium text-white hover:bg-green-hover focus:outline-none focus:ring-[3px] focus:ring-green-soft">{{.Open}}</a>
{{- end}}
</div>`))

// statusFragment renders statusTmpl in the request locale.
func (s *service) statusFragment(ctx context.Context, v orderView) template.HTML {
	loc := i18n.Locale(ctx)
	t := func(k string) string { return s.d.I18n.T(loc, k) }
	label, help := t("commerce.order.status."+v.Status), t("commerce.order.help."+v.Status)
	if v.ManualRequest && v.Status == OrderPending {
		label = t("commerce.request.received")
		help = t("commerce.request.help")
	}
	var buf bytes.Buffer
	err := statusTmpl.Execute(&buf, map[string]any{
		"V": v, "Label": label, "Help": help, "Open": t("commerce.account.go"),
	})
	if err != nil {
		s.log.Error("commerce: status fragment", "err", err)
		return ""
	}
	return template.HTML(buf.String()) //nolint:gosec // rendered by html/template above
}

// rateLimiter is a fixed-window in-memory limiter keyed by string.
type rateLimiter struct {
	mu      sync.Mutex
	windows map[string]*window
	now     func() time.Time
}

type window struct {
	start time.Time
	count int
}

const (
	checkoutLimit = 10
	loginLimit    = 5
	openLimit     = 20
	limitWindow   = 10 * time.Minute
	limiterMax    = 10000
)

func newRateLimiter() *rateLimiter { return &rateLimiter{windows: map[string]*window{}, now: time.Now} }

// allow counts one hit for key and reports whether it is within limit per
// period.
func (l *rateLimiter) allow(key string, limit int, period time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.windows) > limiterMax { // drop expired windows
		for k, w := range l.windows {
			if now.Sub(w.start) >= period {
				delete(l.windows, k)
			}
		}
	}
	w, ok := l.windows[key]
	if !ok || now.Sub(w.start) >= period {
		l.windows[key] = &window{start: now, count: 1}
		return true
	}
	w.count++
	return w.count <= limit
}
