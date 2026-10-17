// Package hooks provides WordPress-style actions, filters and events.
//
// Contracts:
//   - Priority: lower prio runs first; equal prio runs in registration order.
//   - Do passes the payload to actions as-is (typed) and joins their errors.
//   - Apply threads v through filters; on the first error it stops and
//     returns the last good value with the error.
//   - Emit marshals the payload to JSON and delivers it to the actions
//     registered under the event name as a json.RawMessage, both when it
//     runs async (via the "hooks.event" job when a queue is attached) and
//     when it falls back to sync delivery. Emit never returns an error.
package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sort"
	"sync"

	"github.com/zadstack/gorun/internal/jobs"
)

// EventJobKind is the job kind used to deliver Emit events asynchronously.
const EventJobKind = "hooks.event"

const eventMaxAttempts = 5

// ActionFunc handles an action or event.
type ActionFunc func(ctx context.Context, payload any) error

// FilterFunc transforms a value in a filter chain.
type FilterFunc func(ctx context.Context, v any) (any, error)

type entry[F any] struct {
	id   uint64
	prio int
	fn   F
}

// Token identifies one Action or Filter registration; pass it to Remove on
// the registry that issued it (ids are per registry, so another registry
// may hold an unrelated registration with the same token). The zero Token
// identifies nothing.
type Token struct{ id uint64 }

// IsZero reports whether t is the zero Token.
func (t Token) IsZero() bool { return t.id == 0 }

// Registry holds actions and filters by name. It is safe for concurrent use.
type Registry struct {
	mu      sync.RWMutex
	actions map[string][]entry[ActionFunc]
	filters map[string][]entry[FilterFunc]
	nextID  uint64
	queue   *jobs.Queue
}

// New returns an empty registry.
func New() *Registry {
	return &Registry{
		actions: map[string][]entry[ActionFunc]{},
		filters: map[string][]entry[FilterFunc]{},
	}
}

// Action registers fn under name with priority prio and returns a token
// for Remove.
func (r *Registry) Action(name string, prio int, fn ActionFunc) Token {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	r.actions[name] = insert(r.actions[name], entry[ActionFunc]{id: r.nextID, prio: prio, fn: fn})
	return Token{id: r.nextID}
}

// Filter registers fn under name with priority prio and returns a token
// for Remove.
func (r *Registry) Filter(name string, prio int, fn FilterFunc) Token {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	r.filters[name] = insert(r.filters[name], entry[FilterFunc]{id: r.nextID, prio: prio, fn: fn})
	return Token{id: r.nextID}
}

// Remove unregisters the action or filter identified by t and reports
// whether it was registered. In-flight Do/Apply calls that already copied
// the chain still run it; later calls do not.
func (r *Registry) Remove(t Token) bool {
	if t.IsZero() {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return without(r.actions, t.id) || without(r.filters, t.id)
}

// without drops the entry with id from m (copy-on-write, so chains handed
// to running Do/Apply calls stay intact) and reports whether it existed.
func without[F any](m map[string][]entry[F], id uint64) bool {
	for name, list := range m {
		for i, e := range list {
			if e.id != id {
				continue
			}
			if len(list) == 1 {
				delete(m, name)
				return true
			}
			out := make([]entry[F], 0, len(list)-1)
			m[name] = append(append(out, list[:i]...), list[i+1:]...)
			return true
		}
	}
	return false
}

func insert[F any](list []entry[F], e entry[F]) []entry[F] {
	out := append(append([]entry[F](nil), list...), e)
	sort.SliceStable(out, func(i, j int) bool { return out[i].prio < out[j].prio })
	return out
}

// Do runs every action registered under name synchronously and returns
// their errors joined with errors.Join (nil when all succeed).
func (r *Registry) Do(ctx context.Context, name string, payload any) error {
	r.mu.RLock()
	list := r.actions[name]
	r.mu.RUnlock()
	var errs []error
	for _, e := range list {
		if err := e.fn(ctx, payload); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Apply runs the filter chain registered under name over v.
func (r *Registry) Apply(ctx context.Context, name string, v any) (any, error) {
	r.mu.RLock()
	list := r.filters[name]
	r.mu.RUnlock()
	for _, e := range list {
		next, err := e.fn(ctx, v)
		if err != nil {
			return v, err
		}
		v = next
	}
	return v, nil
}

type eventPayload struct {
	Name    string          `json:"name"`
	Payload json.RawMessage `json:"payload"`
}

// Emit publishes an event. With a queue attached it is persisted as a
// "hooks.event" job and delivered by a worker (retried on error); otherwise,
// or if enqueueing fails, it is delivered synchronously. Errors are logged.
//
// Emit enqueues on its own pool connection: call it after a db.InTx
// transaction commits, never inside one (it would stall on the lock).
func (r *Registry) Emit(ctx context.Context, name string, payload any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		slog.Default().Error("hooks: emit marshal", "event", name, "err", err)
		return
	}
	r.mu.RLock()
	q := r.queue
	r.mu.RUnlock()
	if q != nil {
		_, err := q.Enqueue(ctx, EventJobKind, eventPayload{Name: name, Payload: raw})
		if err == nil {
			return
		}
		slog.Default().Error("hooks: emit enqueue, delivering sync", "event", name, "err", err)
	}
	if err := r.Do(ctx, name, json.RawMessage(raw)); err != nil {
		slog.Default().Error("hooks: emit", "event", name, "err", err)
	}
}

// AttachQueue makes Emit asynchronous and registers the "hooks.event" job
// handler on q.
func (r *Registry) AttachQueue(q *jobs.Queue) {
	q.Register(EventJobKind, func(ctx context.Context, p json.RawMessage) error {
		var ev eventPayload
		if err := json.Unmarshal(p, &ev); err != nil {
			return err
		}
		return r.Do(ctx, ev.Name, ev.Payload)
	}, eventMaxAttempts)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.queue = q
}

// Names returns every action and filter name, sorted and de-duplicated.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	seen := map[string]bool{}
	for n := range r.actions {
		seen[n] = true
	}
	for n := range r.filters {
		seen[n] = true
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
