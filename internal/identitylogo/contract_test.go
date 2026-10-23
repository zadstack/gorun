package identitylogo_test

import (
	"bytes"
	"context"
	"errors"
	logo "github.com/zadstack/gorun/internal/identitylogo"
	"image"
	"image/color"
	"image/png"
	"sync/atomic"
	"testing"
)

type contractDraftStore struct{ writes int }

func (d *contractDraftStore) Revision(context.Context, string) (string, error) {
	return "independent-revision", nil
}
func (d *contractDraftStore) Review(context.Context, string) (string, map[string]string, error) {
	return "independent-revision", map[string]string{"site.logo": "logo-v1", "site.logo_light": "light-v1", "site.logo_dark": "dark-v1", "site.icon": "icon-v1"}, nil
}
func (d *contractDraftStore) SelectPNG(context.Context, string, string, string, []byte) error {
	d.writes++
	return nil
}
func TestPublicContractProtectsConsentAndPrivateCandidates(t *testing.T) {
	ctx := context.Background()
	im := image.NewNRGBA(image.Rect(0, 0, 256, 256))
	im.Set(0, 0, color.NRGBA{G: 220, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, im); err != nil {
		t.Fatal(err)
	}
	in := logo.Input{ConnectionID: "profile", Theme: "default", Prompt: "Deliberate synthetic request", Size: 256, BudgetMicros: 200, Currency: "USD"}
	payload := []byte(`{"model":"declared","prompt":"Deliberate synthetic request","n":1,"size":"256x256","output_format":"png"}`)
	p := logo.Prepared{Input: in, PromptVersion: logo.PromptVersion, Destination: logo.Destination{ConnectionID: "profile", Fingerprint: "profile-v1"}, Capability: logo.Capability{StaticPNG: true, Fingerprint: "adapter-v1", Evidence: "unverified explicit declaration"}, Quote: logo.Quote{Known: true, AmountMicros: 100, Currency: "USD", Source: "synthetic image estimate", Version: "v1", AsOf: "2026-10-23", Basis: "single PNG"}, Payload: payload}
	calls := 0
	var enabled atomic.Bool
	enabled.Store(true)
	d := &contractDraftStore{}
	s := logo.NewService(logo.Hooks{Prepare: func(context.Context, logo.Input) (logo.Prepared, error) {
		q := p
		q.Payload = append([]byte(nil), payload...)
		return q, nil
	}, GenerateBounded: func(_ context.Context, q logo.Prepared) ([]byte, error) {
		calls++
		if !bytes.Equal(q.Payload, payload) || q.Quote.AmountMicros != 100 || q.Destination.Fingerprint != "profile-v1" {
			t.Fatalf("review mutation escaped: %+v", q)
		}
		return buf.Bytes(), nil
	}}, d, logo.Options{Enabled: func(context.Context) bool { return enabled.Load() }})
	r, err := s.Preview(ctx, "owner", in)
	if err != nil {
		t.Fatal(err)
	}
	r.Prepared.Payload[0] = '['
	r.Prepared.Quote.AmountMicros = 1
	r.Prepared.Destination.Fingerprint = "tampered"
	c, err := s.Generate(ctx, "owner", r.Token, true)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
	_, data, err := s.Candidate(ctx, "owner", c.ID)
	if err != nil {
		t.Fatal(err)
	}
	data[0] = 0
	_, again, err := s.Candidate(ctx, "owner", c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = png.Decode(bytes.NewReader(again)); err != nil {
		t.Fatalf("returned image mutated private stage: %v", err)
	}
	enabled.Store(false)
	if _, err = s.Preview(ctx, "owner", in); !errors.Is(err, logo.ErrDisabled) {
		t.Fatalf("disabled preview error=%v", err)
	}
	if _, _, err = s.Candidate(ctx, "owner", c.ID); err == nil {
		t.Fatal("disabled module exposed candidate")
	}
	if err = s.Select(ctx, "owner", c.ID, "site.logo", c.Revision); err == nil || d.writes != 0 {
		t.Fatalf("disabled selection err=%v writes=%d", err, d.writes)
	}
	enabled.Store(true)
	if _, _, err = s.Candidate(ctx, "owner", c.ID); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("re-enable generated automatically")
	}
}
