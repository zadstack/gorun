package analytics

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/settings"
)

// script is the beacon served at ScriptPath (spec §2): it sends one view on
// load and after each pushState/popstate that changes the path or utm query,
// and nothing under Do Not Track or Global Privacy Control.
//
//go:embed script.js
var script string

// Collection endpoints and settings.
const (
	ScriptPath = "/_a/s.js"
	BeaconPath = "/_a/e"

	// SettingEnabled turns tracking on or off (JSON bool, default true).
	SettingEnabled = "analytics.enabled"
	// SettingExclude lists excluded path prefixes, one per line (JSON string).
	SettingExclude = "analytics.exclude"
)

const (
	// permStaff marks site staff: their pages get no script and their
	// beacons are ignored (ruling A4).
	permStaff = "content.edit"
	// previewParam is theme.PreviewParam (a draft-override theme preview);
	// TestNoInjectionForAdminPreviewStaffOrExcluded uses the theme constant.
	previewParam = "gp_preview_override"

	maxBeacon  = 2 << 10 // beacon body cap, bytes
	rateLimit  = 120     // beacons per IP per rateWindow
	rateWindow = time.Minute
	maxLocale  = 35
	// maxInjectBody bounds the HTML buffered for injection; a larger page
	// is passed through untouched.
	maxInjectBody = 8 << 20
)

var scriptTag = []byte(`<script defer src="` + ScriptPath + `"></script>`)

// siteIDFor derives the stable per-install site ID mixed into visitor
// hashes: the first 16 hex characters of sha256(secretKey || "analytics").
// The raw key never enters the visitor hash.
func siteIDFor(secretKey []byte) string {
	h := sha256.New()
	h.Write(secretKey)
	h.Write([]byte("analytics"))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// siteOrigin returns the lower-case scheme://host of baseURL and its host
// name, or "" for both when baseURL is not absolute.
func siteOrigin(baseURL string) (origin, host string) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", ""
	}
	return strings.ToLower(u.Scheme + "://" + u.Host), strings.ToLower(u.Hostname())
}

// serveScript answers GET/HEAD ScriptPath.
func serveScript(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Type", "text/javascript; charset=utf-8")
	h.Set("Cache-Control", "public, max-age=86400")
	h.Set("Content-Length", strconv.Itoa(len(script)))
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.WriteString(w, script)
}

// tracking reads the tracking switch (default on) and the excluded path
// prefixes. The settings store memoizes both between writes.
func (m *Module) tracking(ctx context.Context) (on bool, exclude []string) {
	on = true
	var v bool
	switch err := m.settings.Get(ctx, SettingEnabled, &v); {
	case err == nil:
		on = v
	case !errors.Is(err, settings.ErrNotFound):
		m.log.Error("analytics: read setting", "key", SettingEnabled, "err", err)
	}
	if !on {
		return false, nil
	}
	for _, line := range strings.Split(m.settings.GetString(ctx, SettingExclude, ""), "\n") {
		if p := strings.TrimSpace(line); p != "" {
			exclude = append(exclude, p)
		}
	}
	return true, exclude
}

func excluded(path string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// beacon answers POST BeaconPath: always 204 with an empty body, whatever
// happened, so a client or bot learns nothing.
func (m *Module) beacon(w http.ResponseWriter, r *http.Request) {
	m.collect(r)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// beaconBody is the JSON the script sends.
type beaconBody struct {
	P string  `json:"p"` // location.pathname plus the utm_* query
	R string  `json:"r"` // document.referrer (first view only)
	W float64 `json:"w"` // innerWidth
	L string  `json:"l"` // <html lang>
}

// collect validates one beacon and queues its view event. Every rejection
// is silent.
func (m *Module) collect(r *http.Request) {
	ctx := r.Context()
	now := time.Now().UTC()
	ip := audit.IPFromContext(ctx)
	if !m.limiter.allow(ip, now) || !m.sameOrigin(r) || r.ContentLength > maxBeacon {
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBeacon+1))
	if err != nil || len(body) > maxBeacon {
		return
	}
	if auth.Can(ctx, permStaff) {
		return
	}
	ua := r.UserAgent()
	browser, os, bot := parseUA(ua)
	if bot {
		return
	}
	var in beaconBody
	if err := json.Unmarshal(body, &in); err != nil {
		return
	}
	path, utm := cleanPath(in.P)
	if path == "" {
		return
	}
	on, exclude := m.tracking(ctx)
	if !on || excluded(path, exclude) {
		return
	}
	visitor, err := m.store.VisitorHash(ctx, now, m.siteID, ip, ua)
	if err != nil {
		m.log.Error("analytics: visitor hash", "err", err)
		return
	}
	ref := refHost(in.R, m.siteHost)
	if ref == requestHost(r) {
		ref = "direct"
	}
	width := 0
	if in.W > 0 && in.W < 1e6 {
		width = int(in.W)
	}
	m.buf.Add(Event{TS: now, Kind: KindView, Path: path, Visitor: visitor, RefHost: ref,
		UTMSource: utm[0], UTMMedium: utm[1], UTMCampaign: utm[2], Browser: browser, OS: os,
		Device: deviceFor(width), Country: m.geo.Country(ip), Locale: cleanLocale(in.L)})
}

// sameOrigin accepts a beacon only from the site itself: Sec-Fetch-Site is
// same-origin or absent, and Origin, when present, is the configured site
// origin or this request's own origin.
func (m *Module) sameOrigin(r *http.Request) bool {
	if s := r.Header.Get("Sec-Fetch-Site"); s != "" && s != "same-origin" {
		return false
	}
	o := strings.ToLower(r.Header.Get("Origin"))
	if o == "" {
		return true
	}
	if m.origin != "" && o == m.origin {
		return true
	}
	scheme := "http"
	if httpx.RequestIsHTTPS(r) {
		scheme = "https"
	}
	return o == scheme+"://"+strings.ToLower(r.Host)
}

// requestHost is the lower-case host name (no port) the request was sent to.
func requestHost(r *http.Request) string {
	h := r.Host
	if hh, _, err := net.SplitHostPort(h); err == nil {
		h = hh
	}
	return strings.ToLower(strings.Trim(h, "[]"))
}

// cleanLocale keeps a language tag of letters, digits and '-' (lower case).
func cleanLocale(l string) string {
	l = strings.ToLower(strings.TrimSpace(l))
	if len(l) > maxLocale {
		return ""
	}
	for _, c := range l {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return ""
		}
	}
	return l
}

// limiter is a fixed-window per-key counter.
type limiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	hits   map[string]*hitWindow
	pruned time.Time
}

type hitWindow struct {
	start time.Time
	n     int
}

// maxTracked bounds the limiter's memory: once that many keys are live in
// the current window, new keys are refused until it rolls over.
const maxTracked = 50000

func newLimiter(limit int, window time.Duration) *limiter {
	return &limiter{limit: limit, window: window, hits: map[string]*hitWindow{}}
}

func (l *limiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.pruned) >= l.window {
		for k, w := range l.hits {
			if now.Sub(w.start) >= l.window {
				delete(l.hits, k)
			}
		}
		l.pruned = now
	}
	w := l.hits[key]
	switch {
	case w == nil:
		if len(l.hits) >= maxTracked {
			return false
		}
		l.hits[key] = &hitWindow{start: now, n: 1}
		return true
	case now.Sub(w.start) >= l.window:
		w.start, w.n = now, 1
		return true
	case w.n >= l.limit:
		return false
	default:
		w.n++
		return true
	}
}

// inject is the public-router middleware that inserts the script tag
// before the first </head> of GET 200 text/html responses (ruling A3).
// Admin paths, the collection endpoints and theme previews are skipped
// outright; staff, the tracking switch and excluded prefixes are checked
// only once a response turns out to be an HTML page.
func (m *Module) inject(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if r.Method != http.MethodGet || p == "/admin" || strings.HasPrefix(p, "/admin/") || strings.HasPrefix(p, "/_a/") ||
			r.URL.Query().Has(previewParam) {
			next.ServeHTTP(w, r)
			return
		}
		iw := &injectWriter{ResponseWriter: w, want: func() bool {
			if auth.Can(r.Context(), permStaff) {
				return false
			}
			on, exclude := m.tracking(r.Context())
			return on && !excluded(p, exclude)
		}}
		next.ServeHTTP(iw, r)
		iw.finish()
	})
}

// injectWriter buffers a qualifying response (200, text/html, not
// encoded) so finish can insert the tag; anything else, and any response
// that flushes or outgrows maxInjectBody, passes through untouched.
type injectWriter struct {
	http.ResponseWriter
	want      func() bool
	decided   bool
	buffering bool
	buf       bytes.Buffer
}

func (iw *injectWriter) WriteHeader(code int) {
	if iw.decided {
		if !iw.buffering {
			iw.ResponseWriter.WriteHeader(code)
		}
		return
	}
	if code >= 100 && code < 200 { // informational: not the final status
		iw.ResponseWriter.WriteHeader(code)
		return
	}
	iw.decided = true
	h := iw.Header()
	if code == http.StatusOK && isHTML(h.Get("Content-Type")) && h.Get("Content-Encoding") == "" && iw.want() {
		iw.buffering = true
		return
	}
	iw.ResponseWriter.WriteHeader(code)
}

func (iw *injectWriter) Write(b []byte) (int, error) {
	if !iw.decided {
		iw.WriteHeader(http.StatusOK)
	}
	if !iw.buffering {
		return iw.ResponseWriter.Write(b)
	}
	if iw.buf.Len()+len(b) > maxInjectBody {
		if err := iw.spill(); err != nil {
			return 0, err
		}
		return iw.ResponseWriter.Write(b)
	}
	return iw.buf.Write(b)
}

// Flush gives up injection (a streaming response) and flushes.
func (iw *injectWriter) Flush() {
	if !iw.decided {
		iw.decided = true
	} else if iw.buffering {
		_ = iw.spill()
	}
	_ = http.NewResponseController(iw.ResponseWriter).Flush()
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (iw *injectWriter) Unwrap() http.ResponseWriter { return iw.ResponseWriter }

// spill writes the buffered bytes unchanged and stops buffering.
func (iw *injectWriter) spill() error {
	iw.buffering = false
	iw.ResponseWriter.WriteHeader(http.StatusOK)
	_, err := iw.ResponseWriter.Write(iw.buf.Bytes())
	iw.buf = bytes.Buffer{}
	return err
}

// finish writes a buffered response with the tag inserted.
func (iw *injectWriter) finish() {
	if !iw.buffering {
		return
	}
	iw.buffering = false
	body := insertTag(iw.buf.Bytes())
	h := iw.Header()
	h.Set("Content-Length", strconv.Itoa(len(body)))
	h.Del("ETag") // the body differs from what the handler tagged
	iw.ResponseWriter.WriteHeader(http.StatusOK)
	_, _ = iw.ResponseWriter.Write(body)
}

func isHTML(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	return err == nil && mt == "text/html"
}

// insertTag puts scriptTag right before the first "</head>" (any case);
// a page without one is returned unchanged.
func insertTag(body []byte) []byte {
	i := indexFold(body, []byte("</head>"))
	if i < 0 {
		return body
	}
	out := make([]byte, 0, len(body)+len(scriptTag))
	out = append(out, body[:i]...)
	out = append(out, scriptTag...)
	return append(out, body[i:]...)
}

// indexFold is bytes.Index with ASCII case folding; sep must be lower case.
func indexFold(s, sep []byte) int {
	for i := 0; i+len(sep) <= len(s); i++ {
		if s[i] == '<' && bytes.EqualFold(s[i:i+len(sep)], sep) {
			return i
		}
	}
	return -1
}
