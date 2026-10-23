package identitylogo

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image/png"
	"strings"
	"sync"
	"time"
)

const (
	reviewTTL      = 10 * time.Minute
	candidateTTL   = 30 * time.Minute
	MaxImageBytes  = 8 << 20
	maxPixels      = 2048 * 2048
	maxStagedBytes = 32 << 20
	maxReviews     = 64
	maxCandidates  = 16
	maxPrompt      = 8000
)

type pending struct {
	owner         string
	review        Review
	slotBaselines map[string]string
}
type staged struct {
	owner         string
	candidate     Candidate
	png           []byte
	selecting     bool
	review        Review
	slotBaselines map[string]string
}

// Service keeps bounded private staging in memory, outside the public media
// library. Restart discards pending consent and candidates without any replay.
type Service struct {
	hooks      Hooks
	drafts     DraftStore
	options    Options
	mu         sync.Mutex
	reviews    map[string]pending
	candidates map[string]*staged
	inFlight   int
}

func NewService(h Hooks, d DraftStore, o Options) *Service {
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Service{hooks: h, drafts: d, options: o, reviews: make(map[string]pending), candidates: make(map[string]*staged)}
}
func (s *Service) enabled(ctx context.Context) bool {
	return s.options.Enabled == nil || s.options.Enabled(ctx)
}
func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func token() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

var selectionSlots = []string{"site.logo", "site.logo_light", "site.logo_dark", "site.icon"}

func validSlotBaselines(baselines map[string]string) bool {
	for _, slot := range selectionSlots {
		if baselines[slot] == "" {
			return false
		}
	}
	return true
}
func copySlotBaselines(baselines map[string]string) map[string]string {
	out := make(map[string]string, len(selectionSlots))
	for _, slot := range selectionSlots {
		out[slot] = baselines[slot]
	}
	return out
}

func copyPrepared(p Prepared) Prepared {
	p.Payload = append(json.RawMessage(nil), p.Payload...)
	return p
}
func (s *Service) prune(now time.Time) {
	for k, r := range s.reviews {
		if !now.Before(r.review.Expires) {
			delete(s.reviews, k)
		}
	}
	for k, c := range s.candidates {
		if !c.selecting && !now.Before(c.candidate.Expires) {
			delete(s.candidates, k)
		}
	}
}

func validPrepared(p Prepared, in Input) bool {
	return p.PromptVersion == PromptVersion && p.Input == in && p.Destination.ConnectionID == in.ConnectionID && p.Destination.Fingerprint != "" &&
		p.Capability.StaticPNG && p.Capability.Fingerprint != "" && p.Capability.Evidence != "" &&
		p.Quote.Known && p.Quote.AmountMicros >= 0 && p.Quote.AmountMicros <= in.BudgetMicros &&
		p.Quote.Currency == in.Currency && p.Quote.Source != "" && p.Quote.Version != "" && p.Quote.AsOf != "" && p.Quote.Basis != "" &&
		len(p.Payload) > 0 && len(p.Payload) <= 32<<10 && json.Valid(p.Payload)
}

// Preview performs no provider I/O and returns every outbound byte for review.
func (s *Service) Preview(ctx context.Context, owner string, in Input) (Review, error) {
	if !s.enabled(ctx) {
		return Review{}, ErrDisabled
	}
	if owner == "" || in.Theme == "" || strings.TrimSpace(in.Prompt) == "" || len(in.Prompt) > maxPrompt ||
		(in.Size != 256 && in.Size != 512 && in.Size != 1024) || in.BudgetMicros <= 0 || len(in.Currency) != 3 ||
		s.hooks.Prepare == nil || s.hooks.GenerateBounded == nil || s.drafts == nil {
		return Review{}, ErrUnsupported
	}
	p, err := s.hooks.Prepare(ctx, in)
	if err != nil {
		return Review{}, ErrUnsupported
	}
	if !validPrepared(p, in) {
		return Review{}, ErrUnsupported
	}
	revision, baselines, err := s.drafts.Review(ctx, in.Theme)
	if err != nil {
		return Review{}, err
	}
	if revision == "" || !validSlotBaselines(baselines) {
		return Review{}, ErrChanged
	}
	t, err := token()
	if err != nil {
		return Review{}, err
	}
	r := Review{Token: t, Expires: s.options.Now().Add(reviewTTL), Revision: revision, PayloadHash: digest(p.Payload), Prepared: copyPrepared(p)}
	s.mu.Lock()
	s.prune(s.options.Now())
	// One pending review per owner avoids unbounded replacement/double-click state.
	for k, v := range s.reviews {
		if v.owner == owner {
			delete(s.reviews, k)
		}
	}
	if len(s.reviews) >= maxReviews {
		s.mu.Unlock()
		return Review{}, ErrLimit
	}
	saved := r
	saved.Prepared = copyPrepared(p)
	s.reviews[t] = pending{owner: owner, review: saved, slotBaselines: copySlotBaselines(baselines)}
	s.mu.Unlock()
	s.record(ctx, "review", "ready", owner, r, "")
	return r, nil
}

// Generate consumes consent before I/O, including failed/uncertain attempts.
// A retry always needs a new explicit review and approval.
func (s *Service) Generate(ctx context.Context, owner, approval string, approved bool) (result Candidate, resultErr error) {
	if !s.enabled(ctx) {
		return Candidate{}, ErrDisabled
	}
	if !approved || owner == "" {
		s.record(ctx, "generation", "approval_rejected", owner, Review{}, "")
		return Candidate{}, ErrApproval
	}
	s.mu.Lock()
	p, ok := s.reviews[approval]
	s.prune(s.options.Now())
	if !ok || p.owner != owner {
		s.mu.Unlock()
		s.record(ctx, "generation", "approval_rejected", owner, Review{}, "")
		return Candidate{}, ErrApproval
	}
	if !s.options.Now().Before(p.review.Expires) {
		s.mu.Unlock()
		s.record(ctx, "generation", "approval_expired", owner, p.review, "")
		return Candidate{}, ErrApproval
	}
	delete(s.reviews, approval)
	s.mu.Unlock()
	r := p.review
	defer func() {
		outcome := "failed"
		if errors.Is(resultErr, ErrChanged) {
			outcome = "review_changed"
		}
		if errors.Is(resultErr, ErrLimit) {
			outcome = "capacity_rejected"
		}
		if resultErr == nil {
			outcome = "staged"
		}
		s.record(ctx, "generation", outcome, owner, r, result.SHA256)
	}()
	revision, err := s.drafts.Revision(ctx, r.Prepared.Input.Theme)
	if err != nil {
		return Candidate{}, err
	}
	if revision != r.Revision {
		return Candidate{}, ErrChanged
	}
	// Prepare again checks local cost/capability/payload configuration. The bounded
	// bridge still must atomically bind the actual destination read used for I/O.
	current, err := s.hooks.Prepare(ctx, r.Prepared.Input)
	if err != nil {
		return Candidate{}, ErrChanged
	}
	a, _ := json.Marshal(current)
	b, _ := json.Marshal(r.Prepared)
	if !bytes.Equal(a, b) {
		return Candidate{}, ErrChanged
	}
	if !s.options.Now().Before(r.Expires) {
		return Candidate{}, ErrApproval
	}
	if !s.enabled(ctx) {
		return Candidate{}, ErrDisabled
	}
	s.mu.Lock()
	s.prune(s.options.Now())
	reserved := (s.inFlight + 1) * MaxImageBytes
	for _, c := range s.candidates {
		reserved += len(c.png)
	}
	if len(s.candidates)+s.inFlight >= maxCandidates || reserved > maxStagedBytes {
		s.mu.Unlock()
		return Candidate{}, ErrLimit
	}
	s.inFlight++
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.inFlight--; s.mu.Unlock() }()
	data, err := s.hooks.GenerateBounded(ctx, copyPrepared(r.Prepared))
	if err != nil {
		if errors.Is(err, ErrChanged) {
			return Candidate{}, ErrChanged
		}
		return Candidate{}, ErrProvider
	}
	if !s.enabled(ctx) {
		return Candidate{}, ErrDisabled
	}
	imageData, w, h, err := sanitizePNG(data)
	if err == nil && (w != r.Prepared.Input.Size || h != r.Prepared.Input.Size) {
		err = ErrImage
	}
	if err != nil {
		return Candidate{}, err
	}
	id, err := token()
	if err != nil {
		return Candidate{}, err
	}
	c := Candidate{ID: id, Theme: r.Prepared.Input.Theme, Revision: r.Revision, Expires: s.options.Now().Add(candidateTTL), SHA256: digest(imageData), Width: w, Height: h, Bytes: len(imageData), PayloadHash: r.PayloadHash}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune(s.options.Now())
	total := len(imageData)
	for _, v := range s.candidates {
		total += len(v.png)
	}
	if len(s.candidates) >= maxCandidates || total > maxStagedBytes {
		return Candidate{}, ErrLimit
	}
	s.candidates[id] = &staged{owner: owner, candidate: c, png: imageData, review: r, slotBaselines: copySlotBaselines(p.slotBaselines)}
	return c, nil
}

func sanitizePNG(data []byte) ([]byte, int, int, error) {
	if len(data) == 0 || len(data) > MaxImageBytes {
		return nil, 0, 0, ErrImage
	}
	// Reject APNG rather than silently treating animation as a static logo.
	for pos := 8; pos+12 <= len(data); {
		n := int(binary.BigEndian.Uint32(data[pos : pos+4]))
		if n > len(data)-pos-12 {
			return nil, 0, 0, ErrImage
		}
		kind := string(data[pos+4 : pos+8])
		if kind == "IEND" {
			break
		}
		if kind == "acTL" || kind == "fcTL" || kind == "fdAT" {
			return nil, 0, 0, ErrImage
		}
		pos += n + 12
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 2048 || cfg.Height > 2048 || cfg.Width*cfg.Height > maxPixels {
		return nil, 0, 0, ErrImage
	}
	im, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, ErrImage
	}
	var out bytes.Buffer
	if err := png.Encode(&out, im); err != nil || out.Len() > MaxImageBytes {
		return nil, 0, 0, ErrImage
	}
	return out.Bytes(), cfg.Width, cfg.Height, nil
}

// Candidate returns a defensive copy only to its owner. HTTP adapters must set
// image/png, nosniff and private/no-store and enforce both settings.manage and
// media.upload on every action (including candidate reads).
func (s *Service) Candidate(ctx context.Context, owner, id string) (Candidate, []byte, error) {
	if !s.enabled(ctx) {
		return Candidate{}, nil, ErrDisabled
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune(s.options.Now())
	c, ok := s.candidates[id]
	if !ok || c.owner != owner {
		return Candidate{}, nil, ErrCandidate
	}
	return c.candidate, append([]byte(nil), c.png...), nil
}

// Select imports only after a human explicitly chooses a slot. The original
// candidate revision binds the submitted form; the private target
// baseline prevents overwriting that slot while unrelated edits remain valid.
func (s *Service) Select(ctx context.Context, owner, id, slot, expectedRevision string) error {
	if !s.enabled(ctx) {
		return ErrDisabled
	}
	if slot != "site.logo" && slot != "site.logo_light" && slot != "site.logo_dark" && slot != "site.icon" {
		return ErrCandidate
	}
	s.mu.Lock()
	s.prune(s.options.Now())
	c, ok := s.candidates[id]
	if !ok || c.owner != owner || c.selecting {
		s.mu.Unlock()
		return ErrCandidate
	}
	if c.candidate.Revision != expectedRevision || c.slotBaselines[slot] == "" {
		s.mu.Unlock()
		return ErrChanged
	}
	c.selecting = true
	data := append([]byte(nil), c.png...)
	meta := c.candidate
	baseline := c.slotBaselines[slot]
	s.mu.Unlock()
	err := s.drafts.SelectPNG(ctx, meta.Theme, baseline, slot, data)
	outcome := "failed"
	if err == nil {
		outcome = "draft_saved"
	}
	s.record(ctx, "select", outcome, owner, c.review, meta.SHA256)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		delete(s.candidates, id)
	} else {
		c.selecting = false
	}
	return err
}

func (s *Service) record(ctx context.Context, action, outcome, owner string, r Review, candidateHash string) {
	if s.hooks.Record == nil {
		return
	}
	s.hooks.Record(ctx, AuditEvent{Action: action, Outcome: outcome, Owner: owner, Theme: r.Prepared.Input.Theme, ConnectionID: r.Prepared.Destination.ConnectionID, Model: r.Prepared.Destination.Model, PayloadHash: r.PayloadHash, PromptVersion: r.Prepared.PromptVersion, EstimateMicros: r.Prepared.Quote.AmountMicros, Currency: r.Prepared.Quote.Currency, CandidateHash: candidateHash})
}
