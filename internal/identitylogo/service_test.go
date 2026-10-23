package identitylogo

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type memoryDraft struct {
	mu       sync.Mutex
	revision string
	writes   int
	slot     string
	data     []byte
}

func (d *memoryDraft) Revision(context.Context, string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.revision, nil
}
func (d *memoryDraft) SelectPNG(_ context.Context, _ string, rev, slot string, data []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if rev != d.revision {
		return ErrChanged
	}
	d.writes++
	d.slot = slot
	d.data = append([]byte(nil), data...)
	d.revision = "new"
	return nil
}
func smallPNG(t *testing.T) []byte {
	t.Helper()
	im := image.NewNRGBA(image.Rect(0, 0, 256, 256))
	im.Set(0, 0, color.NRGBA{R: 255, A: 128})
	var out bytes.Buffer
	if err := png.Encode(&out, im); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func input() Input {
	return Input{ConnectionID: "profile", Theme: "default", Prompt: "Synthetic bilingual mark", Size: 256, BudgetMicros: 500000, Currency: "USD"}
}
func prepared(in Input) Prepared {
	return Prepared{PromptVersion: PromptVersion, Input: in, Destination: Destination{ConnectionID: in.ConnectionID, Model: "declared-model", Host: "synthetic.invalid", Fingerprint: "credential-and-config-v1"}, Capability: Capability{StaticPNG: true, Fingerprint: "explicit-png-protocol-v1", Evidence: "configured/unverified"}, Quote: Quote{Known: true, AmountMicros: 100000, Currency: "USD", Source: "operator image estimate", Version: "1", AsOf: "2026-10-23", Basis: "one static PNG"}, Payload: []byte(`{"model":"declared-model","prompt":"Synthetic bilingual mark","n":1,"size":"256x256","output_format":"png"}`)}
}
func newTestService(t *testing.T) (*Service, *memoryDraft, *atomic.Int32) {
	t.Helper()
	d := &memoryDraft{revision: "live+draft-v1"}
	calls := &atomic.Int32{}
	data := smallPNG(t)
	s := NewService(Hooks{Prepare: func(_ context.Context, in Input) (Prepared, error) { return prepared(in), nil }, GenerateBounded: func(context.Context, Prepared) ([]byte, error) { calls.Add(1); return data, nil }}, d, Options{})
	return s, d, calls
}
func preview(t *testing.T, s *Service, owner string) Review {
	t.Helper()
	r, err := s.Preview(context.Background(), owner, input())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestConsentChangesSendNothing(t *testing.T) {
	cases := map[string]func(*Prepared){
		"destination":    func(p *Prepared) { p.Destination.Fingerprint = "rotated-key" },
		"model":          func(p *Prepared) { p.Destination.Model = "other" },
		"payload":        func(p *Prepared) { p.Payload = []byte(`{"n":2}`) },
		"price":          func(p *Prepared) { p.Quote.AmountMicros++ },
		"price_basis":    func(p *Prepared) { p.Quote.Basis = "different" },
		"capability":     func(p *Prepared) { p.Capability.Fingerprint = "schema2" },
		"prompt_version": func(p *Prepared) { p.PromptVersion = "future" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			s, d, calls := newTestService(t)
			r := preview(t, s, "owner")
			s.hooks.Prepare = func(_ context.Context, in Input) (Prepared, error) { p := prepared(in); change(&p); return p, nil }
			_, err := s.Generate(context.Background(), "owner", r.Token, true)
			if !errors.Is(err, ErrChanged) || calls.Load() != 0 || d.writes != 0 {
				t.Fatalf("err=%v calls=%d writes=%d", err, calls.Load(), d.writes)
			}
		})
	}
}

func TestApprovalOwnerExpiryAndDraftBinding(t *testing.T) {
	for _, scenario := range []string{"owner", "expiry", "draft", "no_approval", "disabled"} {
		t.Run(scenario, func(t *testing.T) {
			s, d, calls := newTestService(t)
			now := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
			s.options.Now = func() time.Time { return now }
			r := preview(t, s, "owner")
			owner, approved := "owner", true
			switch scenario {
			case "owner":
				owner = "other"
			case "expiry":
				now = now.Add(reviewTTL)
			case "draft":
				d.revision = "human-edit"
			case "no_approval":
				approved = false
			case "disabled":
				s.options.Enabled = func(context.Context) bool { return false }
			}
			_, err := s.Generate(context.Background(), owner, r.Token, approved)
			if err == nil || calls.Load() != 0 || d.writes != 0 {
				t.Fatalf("err=%v calls=%d", err, calls.Load())
			}
		})
	}
}

func TestUnknownAndIncompleteImageQuotesFailClosed(t *testing.T) {
	for _, field := range []string{"known", "basis", "date", "source", "budget", "capability"} {
		t.Run(field, func(t *testing.T) {
			s, _, calls := newTestService(t)
			s.hooks.Prepare = func(_ context.Context, in Input) (Prepared, error) {
				p := prepared(in)
				switch field {
				case "known":
					p.Quote.Known = false
				case "basis":
					p.Quote.Basis = ""
				case "date":
					p.Quote.AsOf = ""
				case "source":
					p.Quote.Source = ""
				case "budget":
					p.Quote.AmountMicros = in.BudgetMicros + 1
				case "capability":
					p.Capability.StaticPNG = false
				}
				return p, nil
			}
			if _, err := s.Preview(context.Background(), "owner", input()); !errors.Is(err, ErrUnsupported) || calls.Load() != 0 {
				t.Fatalf("err=%v calls=%d", err, calls.Load())
			}
		})
	}
}

func TestPrivateCandidateAndAtomicSelectionContract(t *testing.T) {
	s, d, calls := newTestService(t)
	ctx := context.Background()
	r := preview(t, s, "owner")
	r.Prepared.Payload[0] = '!'
	c, err := s.Generate(ctx, "owner", r.Token, true)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || d.writes != 0 || c.SHA256 == "" || c.Width != 256 {
		t.Fatalf("candidate=%+v calls=%d writes=%d", c, calls.Load(), d.writes)
	}
	if _, _, err := s.Candidate(ctx, "other", c.ID); !errors.Is(err, ErrCandidate) {
		t.Fatal("candidate readable by other owner")
	}
	_, data, err := s.Candidate(ctx, "owner", c.ID)
	if err != nil {
		t.Fatal(err)
	}
	data[0] = 0
	_, again, _ := s.Candidate(ctx, "owner", c.ID)
	if again[0] == 0 {
		t.Fatal("private bytes alias caller memory")
	}
	if _, err := s.Generate(ctx, "owner", r.Token, true); !errors.Is(err, ErrApproval) || calls.Load() != 1 {
		t.Fatal("approval replayed")
	}
	if err := s.Select(ctx, "owner", c.ID, "site.logo", c.Revision); err != nil {
		t.Fatal(err)
	}
	if d.writes != 1 || d.slot != "site.logo" || !bytes.Equal(d.data, again) {
		t.Fatal("selection did not preserve image")
	}
	if _, _, err := s.Candidate(ctx, "owner", c.ID); !errors.Is(err, ErrCandidate) {
		t.Fatal("selected candidate reused")
	}
}

func TestConcurrentDraftChangeKeepsCandidateRecoverable(t *testing.T) {
	s, d, _ := newTestService(t)
	ctx := context.Background()
	r := preview(t, s, "owner")
	c, err := s.Generate(ctx, "owner", r.Token, true)
	if err != nil {
		t.Fatal(err)
	}
	d.revision = "human-edit"
	if err := s.Select(ctx, "owner", c.ID, "site.logo", c.Revision); !errors.Is(err, ErrChanged) || d.writes != 0 {
		t.Fatalf("err=%v writes=%d", err, d.writes)
	}
	if _, _, err := s.Candidate(ctx, "owner", c.ID); err != nil {
		t.Fatal("conflict discarded private candidate")
	}
}

func TestProviderFailureConsumesConsentAndSanitizesError(t *testing.T) {
	s, _, calls := newTestService(t)
	s.hooks.GenerateBounded = func(context.Context, Prepared) ([]byte, error) {
		calls.Add(1)
		return nil, errors.New("secret-key at https://private.invalid")
	}
	r := preview(t, s, "owner")
	_, err := s.Generate(context.Background(), "owner", r.Token, true)
	if !errors.Is(err, ErrProvider) || strings.Contains(err.Error(), "secret-key") {
		t.Fatal(err)
	}
	_, err = s.Generate(context.Background(), "owner", r.Token, true)
	if !errors.Is(err, ErrApproval) || calls.Load() != 1 {
		t.Fatal("failed paid operation retried")
	}
}

func TestBoundedInflightReservationsBeforeProvider(t *testing.T) {
	s, _, calls := newTestService(t)
	data := smallPNG(t)
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	s.hooks.GenerateBounded = func(context.Context, Prepared) ([]byte, error) {
		calls.Add(1)
		entered <- struct{}{}
		<-release
		return data, nil
	}
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for _, owner := range []string{"a", "b", "c", "d"} {
		r := preview(t, s, owner)
		wg.Add(1)
		go func(owner string, r Review) {
			defer wg.Done()
			_, err := s.Generate(context.Background(), owner, r.Token, true)
			errs <- err
		}(owner, r)
		<-entered
	}
	r := preview(t, s, "fifth")
	if _, err := s.Generate(context.Background(), "fifth", r.Token, true); !errors.Is(err, ErrLimit) || calls.Load() != 4 {
		t.Fatalf("err=%v calls=%d", err, calls.Load())
	}
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	r = preview(t, s, "after-release")
	if _, err := s.Generate(context.Background(), "after-release", r.Token, true); err != nil {
		t.Fatal("reservation leaked:", err)
	}
}

func TestRejectNonStaticAndUnboundedImages(t *testing.T) {
	valid := smallPNG(t)
	animated := append([]byte(nil), valid[:len(valid)-12]...)
	chunk := make([]byte, 20)
	binary.BigEndian.PutUint32(chunk[:4], 8)
	copy(chunk[4:8], "acTL")
	animated = append(animated, chunk...)
	animated = append(animated, valid[len(valid)-12:]...)
	for name, data := range map[string][]byte{"html": []byte("<html>"), "svg": []byte("<svg/>"), "gif": []byte("GIF89a"), "oversized": make([]byte, MaxImageBytes+1), "animation": animated, "corrupt": valid[:30]} {
		t.Run(name, func(t *testing.T) {
			if _, _, _, err := sanitizePNG(data); !errors.Is(err, ErrImage) {
				t.Fatal(err)
			}
		})
	}
	// Trailing executable-like bytes are stripped by decode+re-encode.
	out, _, _, err := sanitizePNG(append(valid, []byte("<script>bad</script>")...))
	if err != nil || bytes.Contains(out, []byte("script")) {
		t.Fatal("metadata/trailing content retained", err)
	}
}

func TestAuditHasVersionHashesAndNoRawPrompt(t *testing.T) {
	s, _, _ := newTestService(t)
	var events []AuditEvent
	s.hooks.Record = func(_ context.Context, e AuditEvent) { events = append(events, e) }
	r := preview(t, s, "owner")
	c, err := s.Generate(context.Background(), "owner", r.Token, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Select(context.Background(), "owner", c.ID, "site.icon", c.Revision); err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatal(events)
	}
	for _, e := range events {
		if e.PromptVersion != PromptVersion || e.PayloadHash != r.PayloadHash || e.Owner != "owner" || e.EstimateMicros != 100000 {
			t.Fatalf("audit missing binding: %+v", e)
		}
	}
}

func TestStagingCapacityAndExpiryBeforeProvider(t *testing.T) {
	s, _, calls := newTestService(t)
	now := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	s.options.Now = func() time.Time { return now }
	var first Candidate
	for i := 0; i < maxCandidates; i++ {
		owner := string(rune('a' + i))
		r := preview(t, s, owner)
		c, err := s.Generate(context.Background(), owner, r.Token, true)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = c
		}
	}
	r := preview(t, s, "overflow")
	if _, err := s.Generate(context.Background(), "overflow", r.Token, true); !errors.Is(err, ErrLimit) || calls.Load() != maxCandidates {
		t.Fatalf("full staging called provider: err=%v calls=%d", err, calls.Load())
	}
	now = now.Add(candidateTTL)
	if _, _, err := s.Candidate(context.Background(), "a", first.ID); !errors.Is(err, ErrCandidate) {
		t.Fatal("expired candidate readable")
	}
	r = preview(t, s, "after-expiry")
	if _, err := s.Generate(context.Background(), "after-expiry", r.Token, true); err != nil {
		t.Fatal("expired staging not reclaimed", err)
	}
}

func TestDoubleSubmitSingleProviderRequest(t *testing.T) {
	s, _, calls := newTestService(t)
	data := smallPNG(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	s.hooks.GenerateBounded = func(context.Context, Prepared) ([]byte, error) {
		calls.Add(1)
		close(entered)
		<-release
		return data, nil
	}
	r := preview(t, s, "owner")
	first := make(chan error, 1)
	go func() { _, err := s.Generate(context.Background(), "owner", r.Token, true); first <- err }()
	<-entered
	if _, err := s.Generate(context.Background(), "owner", r.Token, true); !errors.Is(err, ErrApproval) || calls.Load() != 1 {
		t.Fatalf("double submit err=%v calls=%d", err, calls.Load())
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
}

func TestProviderDimensionMismatchNeverStages(t *testing.T) {
	s, d, calls := newTestService(t)
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	s.hooks.GenerateBounded = func(context.Context, Prepared) ([]byte, error) { calls.Add(1); return b.Bytes(), nil }
	r := preview(t, s, "owner")
	if _, err := s.Generate(context.Background(), "owner", r.Token, true); !errors.Is(err, ErrImage) || calls.Load() != 1 || d.writes != 0 {
		t.Fatalf("err=%v calls=%d writes=%d", err, calls.Load(), d.writes)
	}
}

func TestExpiryDuringLocalRevalidationSendsNothing(t *testing.T) {
	s, _, calls := newTestService(t)
	now := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	s.options.Now = func() time.Time { return now }
	r := preview(t, s, "owner")
	s.hooks.Prepare = func(_ context.Context, in Input) (Prepared, error) {
		now = now.Add(reviewTTL)
		return prepared(in), nil
	}
	if _, err := s.Generate(context.Background(), "owner", r.Token, true); !errors.Is(err, ErrApproval) || calls.Load() != 0 {
		t.Fatalf("expired review sent: err=%v calls=%d", err, calls.Load())
	}
}
