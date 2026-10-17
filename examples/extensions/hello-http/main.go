// Package hello is a minimal GoRun extension speaking the HTTP bridge
// protocol (docs/EXTENSIONS.md). It verifies every delivery's HMAC
// signature, logs actions and events, and implements one filter:
// content.before_save appends " — via hello" to the title.
//
// It uses only the standard library on purpose: it is what a third-party
// extension written in Go looks like. Run it with
//
//	HELLO_SECRET=<secret shown at install> go run ./examples/extensions/hello-http/cmd/hello-http
//
// GoRun's tests mount Handler behind httptest.
package hello

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Suffix is what the content.before_save filter appends to titles.
const Suffix = " — via hello"

const (
	maxBody = 1 << 20
	skew    = 5 * time.Minute
)

// Delivery is one verified bridge request.
type Delivery struct {
	Hook       string          `json:"hook"`
	Kind       string          `json:"kind"`
	Payload    json.RawMessage `json:"payload"`
	Site       json.RawMessage `json:"site"`
	TS         int64           `json:"ts"`
	DeliveryID string          `json:"delivery_id"`
}

// Server is the extension's HTTP handler.
type Server struct {
	mu     sync.Mutex
	secret []byte
	events []Delivery
	Log    *slog.Logger
}

// Handler returns the extension handler verifying deliveries with secret
// (the HMAC secret GoRun shows once at install).
func Handler(secret string) *Server {
	return &Server{secret: []byte(secret), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// SetSecret replaces the HMAC secret (e.g. after reinstalling).
func (s *Server) SetSecret(secret string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.secret = []byte(secret)
}

// Events returns the verified action/event deliveries received so far.
func (s *Server) Events() []Delivery {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Delivery(nil), s.events...)
}

func (s *Server) verify(r *http.Request, body []byte) bool {
	ts := r.Header.Get("X-GoRun-Timestamp")
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false
	}
	if d := time.Since(time.Unix(sec, 0)); d > skew || d < -skew {
		return false
	}
	s.mu.Lock()
	mac := hmac.New(sha256.New, s.secret)
	s.mu.Unlock()
	mac.Write([]byte(ts + "\n"))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(want), []byte(r.Header.Get("X-GoRun-Signature")))
}

// ServeHTTP handles one bridge delivery.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	if !s.verify(r, body) {
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}
	var d Delivery
	if err := json.Unmarshal(body, &d); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if d.Kind == "filter" {
		_ = json.NewEncoder(w).Encode(map[string]any{"value": filter(d)})
		return
	}
	s.Log.Info("hello: delivery", "hook", d.Hook, "kind", d.Kind, "delivery_id", d.DeliveryID)
	s.mu.Lock()
	s.events = append(s.events, d)
	s.mu.Unlock()
	_, _ = w.Write([]byte(`{"ok":true}`))
}

// filter returns the replacement value: content.before_save gets the
// suffix appended to its title, every other filter returns the value as is.
func filter(d Delivery) json.RawMessage {
	if d.Hook != "content.before_save" {
		return d.Payload
	}
	var in map[string]any
	if err := json.Unmarshal(d.Payload, &in); err != nil {
		return d.Payload
	}
	if title, ok := in["title"].(string); ok && !strings.HasSuffix(title, Suffix) {
		in["title"] = title + Suffix
	}
	out, err := json.Marshal(in)
	if err != nil {
		return d.Payload
	}
	return out
}
