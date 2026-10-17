package ext

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/hooks"
	"github.com/zadstack/gorun/internal/jobs"
	"github.com/zadstack/gorun/internal/kernel"
)

// DeliverJobKind is the job that delivers actions and events to an
// extension endpoint (async, retried with the queue's backoff).
const DeliverJobKind = "ext.deliver"

const (
	deliverMaxAttempts = 5
	asyncTimeout       = maxTimeoutMS * time.Millisecond // actions/events
	maxResponseBody    = 1 << 20
	maxEnvelopeSize    = 1 << 20 // larger deliveries are skipped (logged, not counted)
	errorThreshold     = 20      // consecutive failures → status error
	hookPriority       = 100
)

// errTooLarge marks a delivery skipped because its envelope exceeds
// maxEnvelopeSize; it is logged but not counted as an endpoint failure.
var errTooLarge = errors.New("ext: delivery envelope over 1 MB, skipped")

// extState is the live delivery state of one activated extension. A new
// state replaces the old one on every activation. Deactivation unregisters
// the extension's hooks and clears active, which delivery re-checks, so it
// stops immediately without a restart.
type extState struct {
	slug     string
	active   atomic.Bool
	endpoint string
	secret   []byte
	subs     map[string]HookSpec // hook name + "|" + registry
}

// bridge tracks extension states and the hook registrations made for each
// active extension, so deactivate/remove can unregister them.
type bridge struct {
	mu     sync.RWMutex
	states map[string]*extState
	tokens map[string][]hooks.Token // slug → its hooks registrations
}

func newBridge() *bridge {
	return &bridge{states: map[string]*extState{}, tokens: map[string][]hooks.Token{}}
}

func subKey(hook, registry string) string { return hook + "|" + registry }

// activate installs a fresh active state for x and (re)registers one hooks
// subscription per declared hook, replacing any earlier registrations.
func (b *bridge) activate(s *service, x *Extension, secret []byte) {
	st := &extState{slug: x.Slug, endpoint: x.Manifest.Endpoint.URL, secret: secret, subs: map[string]HookSpec{}}
	for _, h := range x.Manifest.Hooks {
		st.subs[subKey(h.Name, registryOf(h.Kind))] = h
	}
	st.active.Store(true)
	// Lock order is bridge → hooks registry; the registry never calls back
	// while holding its own lock, so this cannot deadlock.
	b.mu.Lock()
	defer b.mu.Unlock()
	if old := b.states[x.Slug]; old != nil {
		old.active.Store(false)
	}
	b.unsubscribeLocked(s, x.Slug)
	b.states[x.Slug] = st
	seen := map[string]bool{}
	for _, h := range x.Manifest.Hooks {
		k := subKey(h.Name, registryOf(h.Kind))
		if seen[k] {
			continue
		}
		seen[k] = true
		b.tokens[x.Slug] = append(b.tokens[x.Slug], s.subscribe(x.Slug, h))
	}
}

// unsubscribeLocked removes slug's hooks registrations; b.mu must be held.
func (b *bridge) unsubscribeLocked(s *service, slug string) {
	for _, t := range b.tokens[slug] {
		s.hooks.Remove(t)
	}
	delete(b.tokens, slug)
}

// deactivate stops delivery for slug: the active flag drops first (calls
// already past the registry check see it), then its hooks are unregistered.
func (b *bridge) deactivate(s *service, slug string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if st := b.states[slug]; st != nil {
		st.active.Store(false)
	}
	b.unsubscribeLocked(s, slug)
}

func (b *bridge) remove(s *service, slug string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if st := b.states[slug]; st != nil {
		st.active.Store(false)
	}
	b.unsubscribeLocked(s, slug)
	delete(b.states, slug)
}

// lookup returns the active state of slug and its subscription to hook in
// registry, or ok=false when nothing should be delivered.
func (b *bridge) lookup(slug, hook, registry string) (*extState, HookSpec, bool) {
	b.mu.RLock()
	st := b.states[slug]
	b.mu.RUnlock()
	if st == nil || !st.active.Load() {
		return nil, HookSpec{}, false
	}
	spec, ok := st.subs[subKey(hook, registry)]
	return st, spec, ok
}

// subscribe registers the hooks subscription for (slug, h) and returns its
// token. Delivery still re-checks the active flag (belt and braces).
func (s *service) subscribe(slug string, h HookSpec) hooks.Token {
	hook := h.Name
	if registryOf(h.Kind) == KindFilter {
		return s.hooks.Filter(hook, hookPriority, func(ctx context.Context, v any) (any, error) {
			return s.applyFilter(ctx, slug, hook, v), nil
		})
	}
	return s.hooks.Action(hook, hookPriority, func(ctx context.Context, payload any) error {
		s.enqueue(ctx, slug, hook, payload)
		return nil // never fail other subscribers or retry hooks.event
	})
}

type deliverPayload struct {
	Slug       string          `json:"slug"`
	Hook       string          `json:"hook"`
	Kind       string          `json:"kind"`
	DeliveryID string          `json:"delivery_id"`
	Payload    json.RawMessage `json:"payload"`
}

// enqueue turns an action/event into an ext.deliver job (so a slow endpoint
// never blocks the request). Payloads arrive typed (hooks.Do) or as
// json.RawMessage (hooks.Emit via the hooks.event job).
func (s *service) enqueue(ctx context.Context, slug, hook string, payload any) {
	_, spec, ok := s.rt.lookup(slug, hook, KindAction)
	if !ok {
		return
	}
	raw, err := toJSON(payload)
	if err != nil {
		s.log.Error("ext: marshal payload", "ext", slug, "hook", hook, "err", err)
		return
	}
	p := deliverPayload{Slug: slug, Hook: hook, Kind: spec.Kind, DeliveryID: db.NewID(), Payload: raw}
	if _, err := s.jobs.Enqueue(context.WithoutCancel(ctx), DeliverJobKind, p); err != nil {
		s.log.Error("ext: enqueue delivery", "ext", slug, "hook", hook, "err", err)
	}
}

func toJSON(v any) (json.RawMessage, error) {
	switch p := v.(type) {
	case json.RawMessage:
		if len(p) == 0 {
			return json.RawMessage("null"), nil
		}
		return p, nil
	case []byte:
		if json.Valid(p) {
			return p, nil
		}
	}
	return json.Marshal(v)
}

// handleDeliver is the ext.deliver job handler. Deliveries for an
// extension that is no longer active are dropped.
func (s *service) handleDeliver(ctx context.Context, raw json.RawMessage) error {
	var p deliverPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil // malformed: retrying cannot help
	}
	st, spec, ok := s.rt.lookup(p.Slug, p.Hook, registryOf(p.Kind))
	if !ok {
		return nil
	}
	_, err := s.send(ctx, st, p.Hook, spec.Kind, p.Payload, p.DeliveryID, asyncTimeout, nil)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, errTooLarge):
		return nil // retrying cannot help
	}
	// One delivery counts as one failure, once its last attempt failed, so
	// a brief outage absorbed by retries does not push towards status error.
	if attempt, max := jobs.AttemptFromContext(ctx); max > 0 && attempt >= max {
		s.failed(ctx, st, trimErr(err.Error()))
	}
	return err
}

// applyFilter runs a filter round trip. Any failure (timeout, transport
// error, non-2xx, bad JSON) keeps the original value.
func (s *service) applyFilter(ctx context.Context, slug, hook string, v any) any {
	st, spec, ok := s.rt.lookup(slug, hook, KindFilter)
	if !ok {
		return v
	}
	raw, err := json.Marshal(v)
	if err != nil {
		s.log.Error("ext: marshal filter value", "ext", slug, "hook", hook, "err", err)
		return v
	}
	var out any
	check := func(body []byte) error {
		var resp struct {
			Value json.RawMessage `json:"value"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return fmt.Errorf("filter response is not JSON: %w", err)
		}
		if len(resp.Value) == 0 || string(resp.Value) == "null" || sameJSON(resp.Value, raw) {
			out = v // no value or echoed unchanged: keep the original (and any non-JSON state)
			return nil
		}
		nv, err := mergeLike(v, raw, resp.Value)
		if err != nil {
			return fmt.Errorf("filter value does not fit %T: %w", v, err)
		}
		out = nv
		return nil
	}
	timeout := time.Duration(spec.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = defaultTimeoutMS * time.Millisecond
	}
	if _, err := s.send(ctx, st, hook, KindFilter, raw, db.NewID(), timeout, check); err != nil {
		if !errors.Is(err, errTooLarge) {
			s.failed(ctx, st, trimErr(err.Error()))
		}
		s.log.Warn("ext: filter failed, keeping original value", "ext", slug, "hook", hook, "err", err)
		return v
	}
	return out
}

// sameJSON reports whether a and b are the same JSON text ignoring
// insignificant whitespace.
func sameJSON(a, b []byte) bool {
	var ca, cb bytes.Buffer
	if json.Compact(&ca, a) != nil || json.Compact(&cb, b) != nil {
		return false
	}
	return bytes.Equal(ca.Bytes(), cb.Bytes())
}

// mergeLike returns a new value of v's dynamic type (a pointer stays a
// pointer) holding the original JSON with the response decoded on top of
// it: keys missing from the response keep their values, so extensions may
// return partial objects. Callers that type-assert the result keep working.
func mergeLike(v any, orig, resp json.RawMessage) (any, error) {
	if v == nil {
		var x any
		err := json.Unmarshal(resp, &x)
		return x, err
	}
	t := reflect.TypeOf(v)
	ptr := t.Kind() == reflect.Pointer
	if ptr {
		t = t.Elem()
	}
	p := reflect.New(t)
	if err := json.Unmarshal(orig, p.Interface()); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(resp, p.Interface()); err != nil {
		return nil, err
	}
	if ptr {
		return p.Interface(), nil
	}
	return p.Elem().Interface(), nil
}

type siteInfo struct {
	BaseURL string `json:"base_url"`
	Name    string `json:"name"`
}

type envelope struct {
	Hook       string                     `json:"hook"`
	Kind       string                     `json:"kind"`
	Payload    json.RawMessage            `json:"payload"`
	Site       siteInfo                   `json:"site"`
	Settings   map[string]json.RawMessage `json:"settings"`
	TS         int64                      `json:"ts"`
	DeliveryID string                     `json:"delivery_id"`
}

func (s *service) site(ctx context.Context) siteInfo {
	return siteInfo{BaseURL: kernel.BaseURLOf(ctx, s.settings, s.cfg.BaseURL), Name: s.settings.GetString(ctx, "site.title", "")}
}

func (s *service) extSettings(ctx context.Context, slug string) map[string]json.RawMessage {
	prefix := "ext." + slug + ".settings."
	all, err := s.settings.All(ctx, prefix)
	out := map[string]json.RawMessage{}
	if err != nil {
		s.log.Warn("ext: read settings", "ext", slug, "err", err)
		return out
	}
	for k, v := range all {
		out[strings.TrimPrefix(k, prefix)] = v
	}
	return out
}

// send POSTs one signed delivery, logs it and updates the failure counter.
// check (filters) validates and consumes a 2xx response body.
func (s *service) send(ctx context.Context, st *extState, hook, kind string, payload json.RawMessage, deliveryID string,
	timeout time.Duration, check func([]byte) error) ([]byte, error) {
	now := s.now()
	ts := strconv.FormatInt(now.Unix(), 10)
	body, err := json.Marshal(envelope{Hook: hook, Kind: kind, Payload: payload, Site: s.site(ctx),
		Settings: s.extSettings(ctx, st.slug), TS: now.Unix(), DeliveryID: deliveryID})
	if err != nil {
		return nil, err
	}
	if len(body) > maxEnvelopeSize {
		s.log.Warn("ext.deliver.too_large", "ext", st.slug, "hook", hook, "bytes", len(body))
		s.recordDelivery(ctx, st.slug, Delivery{ID: db.NewID(), DeliveryID: deliveryID, Hook: hook, Kind: kind, Status: "error",
			Error: errTooLarge.Error(), CreatedAt: db.FormatTime(now)})
		return nil, errTooLarge
	}
	start := time.Now()
	resp, code, err := s.post(ctx, st, ts, body, deliveryID, timeout)
	if err == nil && check != nil {
		err = check(resp)
	}
	d := Delivery{ID: db.NewID(), DeliveryID: deliveryID, Hook: hook, Kind: kind, Status: "ok", HTTPStatus: code,
		DurationMS: time.Since(start).Milliseconds(), CreatedAt: db.FormatTime(now)}
	if err != nil {
		d.Status, d.Error = "error", trimErr(err.Error())
		if errors.Is(err, context.DeadlineExceeded) {
			d.Status = "timeout"
		}
	}
	s.recordDelivery(ctx, st.slug, d)
	if err != nil {
		return nil, err // callers decide whether it counts as a failure
	}
	s.succeeded(ctx, st.slug)
	return resp, nil
}

func (s *service) post(ctx context.Context, st *extState, ts string, body []byte, deliveryID string, timeout time.Duration) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, st.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "GoRun-Extensions/1")
	req.Header.Set(HeaderTimestamp, ts)
	req.Header.Set(HeaderSignature, Sign(st.secret, ts, body))
	req.Header.Set(HeaderExtension, st.slug)
	req.Header.Set(HeaderDelivery, deliveryID)
	resp, err := s.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, fmt.Errorf("timeout after %s: %w", timeout, context.DeadlineExceeded)
		}
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, resp.StatusCode, fmt.Errorf("timeout after %s: %w", timeout, context.DeadlineExceeded)
		}
		return nil, resp.StatusCode, err
	}
	if len(data) > maxResponseBody {
		return nil, resp.StatusCode, errors.New("response body over 1 MB")
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, resp.StatusCode, fmt.Errorf("endpoint answered %d", resp.StatusCode)
	}
	return data, resp.StatusCode, nil
}

func (s *service) succeeded(ctx context.Context, slug string) {
	if _, err := s.db.ExecContext(context.WithoutCancel(ctx), `UPDATE extensions SET failures = 0 WHERE slug = ? AND failures <> 0`, slug); err != nil {
		s.log.Warn("ext: reset failures", "ext", slug, "err", err)
	}
}

// failed counts a failure; at errorThreshold consecutive failures the
// extension is switched to status error and delivery stops.
func (s *service) failed(ctx context.Context, st *extState, msg string) {
	ctx = context.WithoutCancel(ctx)
	var failures int
	var status string
	err := s.db.QueryRowContext(ctx, `UPDATE extensions SET failures = failures + 1, total_failures = total_failures + 1,
		last_error = ? WHERE slug = ? RETURNING failures, status`, msg, st.slug).Scan(&failures, &status)
	if err != nil {
		s.log.Warn("ext: count failure", "ext", st.slug, "err", err)
		return
	}
	if failures < errorThreshold || status != StatusActive {
		return
	}
	st.active.Store(false)
	res, err := s.db.ExecContext(ctx, `UPDATE extensions SET status = ? WHERE slug = ? AND status = ?`, StatusError, st.slug, StatusActive)
	if err != nil {
		s.log.Error("ext: set error status", "ext", st.slug, "err", err)
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		s.log.Error("ext: too many consecutive failures, delivery stopped", "ext", st.slug, "failures", failures, "last_error", msg)
		s.audit.Record(ctx, "ext.error", "extension", st.slug, map[string]any{"failures": failures, "last_error": msg})
	}
}
