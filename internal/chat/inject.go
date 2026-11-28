package chat

import (
	"bytes"
	"encoding/json"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/zadstack/gorun/internal/httpx"
)

// maxInjectBody bounds the HTML buffered for injection; a larger page is
// passed through untouched (and its CSP is not widened).
const maxInjectBody = 8 << 20

// hardExcluded are the path prefixes that never get the widget: the back
// office, sign-in pages (live credentials in the path) and the analytics
// endpoints. Each also matches its bare form without the trailing slash.
var hardExcluded = []string{"/admin/", "/auth/", "/api/", "/_a/"}

func excluded(path string) bool {
	for _, p := range hardExcluded {
		if strings.HasPrefix(path, p) || path == strings.TrimSuffix(p, "/") {
			return true
		}
	}
	return false
}

// inject is the public-router middleware that puts the chat loader right
// before the last </body> of GET 200 text/html responses that the theme
// rendered (the analytics rule: a module's own HTML page is never touched),
// and widens that response's Content-Security-Policy for the configured
// host. The Metricool tracker, when its hash is set, goes in the same way.
// Nothing happens when neither is configured.
func (m *Module) inject(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || excluded(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		wg, chatOK := Load(r.Context(), m.settings)
		hash, trackerOK := LoadTracker(r.Context(), m.settings)
		if !chatOK && !trackerOK {
			next.ServeHTTP(w, r)
			return
		}
		ctx, mark := httpx.WithPageMark(r.Context())
		r = r.WithContext(ctx)
		iw := &injectWriter{ResponseWriter: w, nonce: httpx.Nonce(ctx), want: mark.Themed}
		if chatOK {
			iw.widget = &wg
		}
		if trackerOK {
			iw.tracker = hash
		}
		next.ServeHTTP(iw, r)
		iw.finish()
	})
}

// injectWriter buffers a qualifying response (200, text/html, not encoded,
// theme-rendered) so finish can insert the loader and widen the CSP before
// the status line goes out; anything else, and any response that flushes or
// outgrows maxInjectBody, passes through untouched.
type injectWriter struct {
	http.ResponseWriter
	widget    *Widget // nil: no chat widget
	tracker   string  // the Metricool tracker hash, "" for none
	nonce     string
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

// finish writes a buffered response with the loader inserted and the CSP
// widened; a page without </body> goes out unchanged, CSP included.
func (iw *injectWriter) finish() {
	if !iw.buffering {
		return
	}
	iw.buffering = false
	var tags []byte
	if iw.widget != nil {
		tags = append(tags, loaderTag(*iw.widget, iw.nonce)...)
	}
	if iw.tracker != "" {
		tags = append(tags, trackerTag(iw.tracker, iw.nonce)...)
	}
	body, ok := insertLoader(iw.buf.Bytes(), tags)
	h := iw.Header()
	if ok {
		if csp := h.Get("Content-Security-Policy"); csp != "" {
			if iw.widget != nil {
				csp = WidenCSP(csp, iw.widget.BaseURL)
			}
			if iw.tracker != "" {
				csp = WidenCSPForTracker(csp)
			}
			h.Set("Content-Security-Policy", csp)
		}
		h.Del("ETag") // the body differs from what the handler tagged
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	iw.ResponseWriter.WriteHeader(http.StatusOK)
	_, _ = iw.ResponseWriter.Write(body)
}

func isHTML(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	return err == nil && mt == "text/html"
}

// loaderTag is the nonced inline script that loads the Chatwoot SDK from
// the widget's host and starts it. The values go in JSON-encoded (which
// also escapes <, > and &), never concatenated raw.
func loaderTag(w Widget, nonce string) []byte {
	base, _ := json.Marshal(w.BaseURL)
	token, _ := json.Marshal(w.WebsiteToken)
	var b bytes.Buffer
	b.WriteString(`<script`)
	if nonce != "" {
		b.WriteString(` nonce="` + nonce + `"`)
	}
	b.WriteString(`>(function(d){var b=`)
	b.Write(base)
	b.WriteString(`,s=d.createElement("script");s.src=b+"/packs/js/sdk.js";s.async=true;s.onload=function(){window.chatwootSDK.run({websiteToken:`)
	b.Write(token)
	b.WriteString(`,baseUrl:b})};d.body.appendChild(s)})(document);</script>`)
	return b.Bytes()
}

// insertLoader puts tag right before the last "</body>" (any case).
func insertLoader(body, tag []byte) ([]byte, bool) {
	i := lastIndexFold(body, []byte("</body>"))
	if i < 0 {
		return body, false
	}
	out := make([]byte, 0, len(body)+len(tag))
	out = append(out, body[:i]...)
	out = append(out, tag...)
	return append(out, body[i:]...), true
}

// lastIndexFold is bytes.LastIndex with ASCII case folding; sep must be
// lower case.
func lastIndexFold(s, sep []byte) int {
	for i := len(s) - len(sep); i >= 0; i-- {
		if s[i] == '<' && bytes.EqualFold(s[i:i+len(sep)], sep) {
			return i
		}
	}
	return -1
}

// WidenCSP adds base (a bare https origin, see NormalizeBaseURL) to
// script-src, base and its wss:// form to connect-src, and base to
// frame-src, adding frame-src 'self' base when the policy has none (it fell
// back to default-src 'self'). Other directives are kept as they are.
func WidenCSP(csp, base string) string {
	wss := "wss://" + strings.TrimPrefix(base, "https://")
	return widen(csp, map[string][]string{
		"script-src":  {base},
		"connect-src": {base, wss},
		"frame-src":   {base},
	})
}

func hasSource(directive, src string) bool {
	for _, f := range strings.Fields(directive)[1:] {
		if f == src {
			return true
		}
	}
	return false
}
