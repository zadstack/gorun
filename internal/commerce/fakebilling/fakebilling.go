// Package fakebilling is an in-memory billing server implementing the
// mcp-billing/v1 protocol (see docs/COMMERCE.md) for tests and local e2e
// runs: signature verification (same scheme as the real servers),
// Idempotency-Key replay on mutations, and failure injection.
//
//	srv := fakebilling.New("client-1", "secret")
//	defer srv.Close()
//	srv.FailNext("POST", "/accounts", 503, 2) // next two creates fail
package fakebilling

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Prefix is the protocol path prefix.
const Prefix = "/mcp-billing/v1"

// MaxSkew is the accepted clock difference for X-MCBB-Timestamp.
const MaxSkew = 5 * time.Minute

// Plan is a catalog plan.
type Plan struct {
	Code   string `json:"code"`
	Name   string `json:"name"`
	NameAr string `json:"name_ar,omitempty"`
}

// Account is a provisioned account, keyed by external id.
type Account struct {
	ExternalID string   `json:"external_id"`
	PlanCode   string   `json:"plan_code"`
	Status     string   `json:"status"`
	PeriodEnd  string   `json:"period_end"`
	Orders     []string `json:"orders"`
}

// Call records one request that passed signature verification.
type Call struct {
	Method, Path, IdempotencyKey string
	Body                         map[string]any
	RawBody                      string // exactly as received (signed bytes)
	Replayed                     bool   // answered from the idempotency store
	Failed                       int    // rejected (injected failure or missing key): the status, else 0
}

type recorded struct {
	status int
	body   []byte
}

type failure struct {
	method, path string
	status, left int
}

// Server is the fake billing server. All methods are safe for concurrent use.
type Server struct {
	*httptest.Server
	ClientID, Secret string

	mu             sync.Mutex
	plans          []Plan
	accounts       map[string]*Account
	idem           map[string]recorded
	calls          []Call
	failures       []*failure
	now            func() time.Time
	periodOverride any // when set, POST /accounts answers this period_end
}

// SetPeriodEndAnswer makes POST /accounts answer period_end v (e.g. an
// epoch number or an unparseable string) instead of the stored value; nil
// restores the default.
func (s *Server) SetPeriodEndAnswer(v any) { s.mu.Lock(); s.periodOverride = v; s.mu.Unlock() }

// New starts a fake billing server accepting clientID/secret.
func New(clientID, secret string) *Server {
	s := &Server{ClientID: clientID, Secret: secret, accounts: map[string]*Account{}, idem: map[string]recorded{}, now: time.Now,
		plans: []Plan{{Code: "pro", Name: "Pro", NameAr: "احترافي"}, {Code: "team", Name: "Team", NameAr: "فريق"}}}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// SetPlans replaces the catalog plans.
func (s *Server) SetPlans(p ...Plan) { s.mu.Lock(); s.plans = p; s.mu.Unlock() }

// FailNext makes the next times requests matching method and path (relative
// to the prefix; a trailing "*" matches a prefix) answer status.
func (s *Server) FailNext(method, path string, status, times int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures = append(s.failures, &failure{method: method, path: path, status: status, left: times})
}

// ClearFailures drops every pending injected failure.
func (s *Server) ClearFailures() { s.mu.Lock(); s.failures = nil; s.mu.Unlock() }

// Account returns a copy of the account with external id id.
func (s *Server) Account(id string) (Account, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.accounts[id]
	if !ok {
		return Account{}, false
	}
	cp := *a
	cp.Orders = append([]string(nil), a.Orders...)
	return cp, true
}

// SetAccountStatus changes an account's status (e.g. to test reconcile).
func (s *Server) SetAccountStatus(id, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a, ok := s.accounts[id]; ok {
		a.Status = status
	}
}

// Calls returns the verified requests so far.
func (s *Server) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Call(nil), s.calls...)
}

// CallCount counts verified, non-replayed, non-failed requests for method path.
func (s *Server) CallCount(method, path string) int {
	n := 0
	for _, c := range s.Calls() {
		if c.Method == method && c.Path == path && !c.Replayed && c.Failed == 0 {
			n++
		}
	}
	return n
}

func writeJSON(w http.ResponseWriter, status int, v any) []byte {
	b, _ := json.Marshal(v)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
	return b
}

func fail(w http.ResponseWriter, status int, msg string) []byte {
	return writeJSON(w, status, map[string]any{"ok": false, "error": map[string]string{"message": msg}})
}

func ok(w http.ResponseWriter, data any) []byte {
	return writeJSON(w, http.StatusOK, map[string]any{"ok": true, "data": data})
}

// verify checks client id, timestamp skew and the HMAC signature.
func (s *Server) verify(r *http.Request, body []byte) string {
	if r.Header.Get("X-MCBB-Client") != s.ClientID {
		return "unknown client"
	}
	ts, err := strconv.ParseInt(r.Header.Get("X-MCBB-Timestamp"), 10, 64)
	if err != nil || s.now().Sub(time.Unix(ts, 0)).Abs() > MaxSkew {
		return "stale or missing timestamp"
	}
	nonce := r.Header.Get("X-MCBB-Nonce")
	if len(nonce) != 32 {
		return "bad nonce"
	}
	if r.Method == http.MethodGet && len(body) != 0 {
		return "GET must not carry a body"
	}
	sum := sha256.Sum256(body)
	msg := strings.Join([]string{r.Header.Get("X-MCBB-Timestamp"), nonce, r.Method, r.URL.Path, hex.EncodeToString(sum[:])}, "\n")
	mac := hmac.New(sha256.New, []byte(s.Secret))
	mac.Write([]byte(msg))
	if !hmac.Equal([]byte(hex.EncodeToString(mac.Sum(nil))), []byte(r.Header.Get("X-MCBB-Signature"))) {
		return "bad signature"
	}
	return ""
}

func (s *Server) takeFailure(method, path string) int {
	for _, f := range s.failures {
		match := f.path == path || (strings.HasSuffix(f.path, "*") && strings.HasPrefix(path, strings.TrimSuffix(f.path, "*")))
		if f.left > 0 && f.method == method && match {
			f.left--
			return f.status
		}
	}
	return 0
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if !strings.HasPrefix(r.URL.Path, Prefix+"/") {
		fail(w, http.StatusNotFound, "not found")
		return
	}
	if msg := s.verify(r, body); msg != "" {
		fail(w, http.StatusUnauthorized, msg)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, Prefix)
	var parsed map[string]any
	if len(body) > 0 {
		if err := json.Unmarshal(body, &parsed); err != nil {
			fail(w, http.StatusBadRequest, "body is not a JSON object")
			return
		}
	}
	key := r.Header.Get("Idempotency-Key")
	s.mu.Lock()
	defer s.mu.Unlock()
	call := Call{Method: r.Method, Path: path, IdempotencyKey: key, Body: parsed, RawBody: string(body)}
	if status := s.takeFailure(r.Method, path); status != 0 {
		call.Failed = status
		s.calls = append(s.calls, call)
		fail(w, status, "injected failure")
		return
	}
	if r.Method != http.MethodGet {
		if key == "" {
			call.Failed = http.StatusBadRequest
			s.calls = append(s.calls, call)
			fail(w, http.StatusBadRequest, "Idempotency-Key required")
			return
		}
		if rec, seen := s.idem[r.Method+" "+path+" "+key]; seen {
			call.Replayed = true
			s.calls = append(s.calls, call)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Idempotent-Replayed", "true")
			w.WriteHeader(rec.status)
			_, _ = w.Write(rec.body)
			return
		}
	}
	s.calls = append(s.calls, call)
	rw := &recorder{ResponseWriter: w}
	s.route(rw, r.Method, path, parsed)
	if r.Method != http.MethodGet && rw.status < 500 {
		s.idem[r.Method+" "+path+" "+key] = recorded{status: rw.status, body: rw.body}
	}
}

type recorder struct {
	http.ResponseWriter
	status int
	body   []byte
}

func (r *recorder) WriteHeader(code int) { r.status = code; r.ResponseWriter.WriteHeader(code) }
func (r *recorder) Write(b []byte) (int, error) {
	r.body = append(r.body, b...)
	return r.ResponseWriter.Write(b)
}

func str(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// route handles a verified request; s.mu is held.
func (s *Server) route(w http.ResponseWriter, method, path string, body map[string]any) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	switch {
	case method == http.MethodGet && path == "/catalog":
		ok(w, map[string]any{"product": map[string]any{"slug": "fake-mcp", "name": "Fake MCP", "name_ar": "خادم تجريبي"},
			"plans": s.plans, "tools": []map[string]string{{"name": "search", "description": "Search"}}})
	case method == http.MethodPost && path == "/accounts":
		s.createAccount(w, body)
	case len(parts) == 2 && parts[0] == "accounts" && method == http.MethodGet:
		a, found := s.accounts[parts[1]]
		if !found {
			fail(w, http.StatusNotFound, "account not found")
			return
		}
		ok(w, a)
	case len(parts) == 3 && parts[0] == "accounts" && method == http.MethodPost:
		a, found := s.accounts[parts[1]]
		if !found {
			fail(w, http.StatusNotFound, "account not found")
			return
		}
		switch parts[2] {
		case "suspend":
			a.Status = "suspended"
			ok(w, a)
		case "resume":
			a.Status = "active"
			ok(w, a)
		case "login-link":
			if a.Status != "active" {
				fail(w, http.StatusConflict, "account is not active")
				return
			}
			ok(w, map[string]any{"url": "https://billing.example.test/login/" + a.ExternalID + "?t=" + strconv.Itoa(len(s.calls)),
				"expires_at": s.now().Add(10 * time.Minute).UTC().Format(time.RFC3339)})
		default:
			fail(w, http.StatusNotFound, "not found")
		}
	default:
		fail(w, http.StatusNotFound, "not found")
	}
}

// createAccount creates or extends an account. The body is the one
// mcp-billing-bridge 0.3.1 sends: external_customer_id, order_ref,
// plan_code, period_end (the new end of the period; a later end extends).
func (s *Server) createAccount(w http.ResponseWriter, body map[string]any) {
	id, plan, end := str(body, "external_customer_id"), str(body, "plan_code"), str(body, "period_end")
	if id == "" || plan == "" || end == "" {
		fail(w, http.StatusUnprocessableEntity, "external_customer_id, plan_code and period_end are required")
		return
	}
	known := false
	for _, p := range s.plans {
		known = known || p.Code == plan
	}
	if !known {
		fail(w, http.StatusUnprocessableEntity, fmt.Sprintf("unknown plan %q", plan))
		return
	}
	a, found := s.accounts[id]
	if !found {
		a = &Account{ExternalID: id}
		s.accounts[id] = a
	}
	a.PlanCode, a.Status = plan, "active"
	if end > a.PeriodEnd {
		a.PeriodEnd = end
	}
	if ref := str(body, "order_ref"); ref != "" {
		a.Orders = append(a.Orders, ref)
	}
	if s.periodOverride != nil {
		ok(w, map[string]any{"external_id": a.ExternalID, "plan_code": a.PlanCode, "status": a.Status, "period_end": s.periodOverride})
		return
	}
	ok(w, a)
}
