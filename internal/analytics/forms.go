package analytics

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/zadstack/gorun/internal/db"
)

// hookFormSubmitted is the event site emits for every accepted form
// submission (site.HookFormSubmitted). The module subscribes by name and
// does not import site.
const hookFormSubmitted = "site.form.submitted"

// seenSubmissions is how many recent submission ids are remembered, so a
// retried hooks.event job does not count a submission twice.
const seenSubmissions = 4096

// maxForm caps the stored form slug, in bytes.
const maxForm = 128

// formPayload is the part of site's FormSubmission analytics reads. The
// payload also carries name, email, message, ip and fields; none of them
// is decoded, so none can be stored.
type formPayload struct {
	ID          string `json:"id"`
	Form        string `json:"form"`
	Page        string `json:"page"`
	SubmittedAt string `json:"submitted_at"`
}

// decodeFormPayload accepts the payload as Emit delivers it
// (json.RawMessage), as raw JSON bytes, or as a value (a struct or a map
// with the FormSubmission JSON keys) passed to Hooks.Do.
func decodeFormPayload(p any) (formPayload, error) {
	var raw []byte
	switch v := p.(type) {
	case json.RawMessage:
		raw = v
	case []byte:
		raw = v
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return formPayload{}, err
		}
		raw = b
	}
	var out formPayload
	err := json.Unmarshal(raw, &out)
	return out, err
}

// formSubmitted is the site.form.submitted action: it queues a submit
// event with the form slug and the submission's page path (query and
// fragment dropped, canonical and capped like a view's path, so a decoded
// Arabic form source meets the encoded views of the same page). The visitor is unknown,
// so it stays empty. The event keeps the submission's own time.
//
// It never returns an error: a failing action makes the hooks.event job
// retry every action of the event, which would send site's notification
// mail again. Problems are logged and the submission is not counted.
func (m *Module) formSubmitted(ctx context.Context, p any) error {
	in, err := decodeFormPayload(p)
	if err != nil {
		m.log.Warn("analytics: undecodable form submission", "err", err)
		return nil
	}
	in.Form = truncate(in.Form, maxForm)
	if in.Form == "" {
		return nil
	}
	path, _ := cleanPath(in.Page)
	on, exclude := m.tracking(ctx)
	if !on || (path != "" && excluded(path, exclude)) {
		return nil
	}
	if !m.seen.add(in.ID) {
		return nil // a retried delivery of a submission already counted
	}
	ts := time.Now().UTC()
	if at, err := db.ParseTime(in.SubmittedAt); err == nil && !at.After(ts) {
		ts = at
	}
	if !m.buf.Add(Event{TS: ts, Kind: KindSubmit, Path: path, Form: in.Form}) {
		m.log.Warn("analytics: form submission not recorded (buffer stopped or full)", "form", in.Form)
	}
	return nil
}

// seenIDs remembers the last n ids added, oldest evicted first.
type seenIDs struct {
	mu   sync.Mutex
	set  map[string]struct{}
	ring []string
	next int
}

func newSeenIDs(n int) *seenIDs {
	return &seenIDs{set: make(map[string]struct{}, n), ring: make([]string, n)}
}

// add records id and reports whether it was new. An empty id is always new
// (there is nothing to deduplicate by).
func (s *seenIDs) add(id string) bool {
	if id == "" {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.set[id]; ok {
		return false
	}
	if old := s.ring[s.next]; old != "" {
		delete(s.set, old)
	}
	s.ring[s.next] = id
	s.set[id] = struct{}{}
	s.next = (s.next + 1) % len(s.ring)
	return true
}
