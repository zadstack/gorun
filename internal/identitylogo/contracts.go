// Package identitylogo stages static PNG proposals privately. Generation never
// publishes media or applies a live identity. Adapters must declare capabilities;
// a configured text connection alone is not evidence of image support.
package identitylogo

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

const PromptVersion = "identitylogo-static-v1"

var (
	ErrApproval    = errors.New("identitylogo: approval missing, expired or consumed")
	ErrChanged     = errors.New("identitylogo: reviewed configuration or identity changed")
	ErrUnsupported = errors.New("identitylogo: static PNG generation or known image pricing unavailable")
	ErrProvider    = errors.New("identitylogo: generation failed; outcome may be uncertain, no automatic retry")
	ErrImage       = errors.New("identitylogo: invalid or oversized static PNG")
	ErrCandidate   = errors.New("identitylogo: candidate unavailable")
	ErrDisabled    = errors.New("identitylogo: module disabled")
	ErrLimit       = errors.New("identitylogo: private staging capacity reached")
)

// Input contains only deliberately supplied data; reference images and animation
// are unsupported. BudgetMicros is in the quote's currency (one unit = 1e6).
type Input struct {
	ConnectionID string `json:"connection_id"`
	Theme        string `json:"theme"`
	Prompt       string `json:"prompt"`
	Size         int    `json:"size"`
	BudgetMicros int64  `json:"budget_micros"`
	Currency     string `json:"currency"`
}

type Destination struct {
	ConnectionID string `json:"connection_id"`
	Name         string `json:"name"`
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	Host         string `json:"host"`
	Fingerprint  string `json:"fingerprint"`
}

// Capability is an explicit adapter declaration, never inferred from model name.
// Evidence states configured/unverified or verified; fake tests never verify a
// production capability. Fingerprint includes adapter/protocol/schema/version.
type Capability struct {
	StaticPNG   bool   `json:"static_png"`
	Fingerprint string `json:"fingerprint"`
	Evidence    string `json:"evidence"`
}

// Quote is an image request estimate, not a text-token estimate or a promise of
// a provider-enforced spending cap. Unknown prices fail closed in this version.
type Quote struct {
	Known        bool   `json:"known"`
	AmountMicros int64  `json:"amount_micros"`
	Currency     string `json:"currency"`
	Source       string `json:"source"`
	Version      string `json:"version"`
	AsOf         string `json:"as_of"`
	Basis        string `json:"basis"`
}

type Prepared struct {
	PromptVersion string      `json:"prompt_version"`
	Input         Input       `json:"input"`
	Destination   Destination `json:"destination"`
	Capability    Capability  `json:"capability"`
	Quote         Quote       `json:"quote"`
	// Payload is the exact reviewed provider JSON. It must describe one PNG and
	// contain no credential, hidden site context or reference image.
	Payload json.RawMessage `json:"payload"`
}

type AuditEvent struct {
	Action                 string `json:"action"`
	Outcome                string `json:"outcome"`
	Owner                  string `json:"owner"`
	Theme                  string `json:"theme"`
	ConnectionID           string `json:"connection_id"`
	Model                  string `json:"model"`
	PayloadHash            string `json:"payload_hash"`
	ApprovalHash           string `json:"approval_hash"`
	DestinationFingerprint string `json:"destination_fingerprint"`
	CapabilityFingerprint  string `json:"capability_fingerprint"`
	QuoteHash              string `json:"quote_hash"`
	QuoteVersion           string `json:"quote_version"`
	DraftRevisionHash      string `json:"draft_revision_hash"`
	PromptVersion          string `json:"prompt_version"`
	EstimateMicros         int64  `json:"estimate_micros"`
	Currency               string `json:"currency"`
	CandidateHash          string `json:"candidate_hash,omitempty"`
}

type Hooks struct {
	// Record receives metadata only; never raw prompts, payloads or secrets.
	Record func(context.Context, AuditEvent)
	// Prepare is local/read-only and must not call a provider. It returns a
	// credential-free payload, explicit capability and image-specific pricing.
	Prepare func(context.Context, Input) (Prepared, error)
	// GenerateBounded re-resolves one configuration snapshot, compares every
	// approved destination/capability/price value and sends exactly Payload using
	// credentials from THAT SAME snapshot. Changed config returns ErrChanged
	// before I/O. No retries, URL downloads, hidden prompts or extra images.
	GenerateBounded func(context.Context, Prepared) ([]byte, error)
}

// DraftStore captures the full generation revision and all four selection
// baselines atomically. Generation still checks the full revision; selection
// compares only its captured target baseline and merges into the latest draft.
// On conflict it must never overwrite concurrent edits or delete shared media.
type DraftStore interface {
	Revision(context.Context, string) (string, error)
	Review(context.Context, string) (revision string, slotBaselines map[string]string, err error)
	SelectPNG(ctx context.Context, theme, slotBaseline, slot string, png []byte) error
}

type Options struct {
	Enabled func(context.Context) bool
	Now     func() time.Time
}

type Review struct {
	// ApprovalHash binds the owner, draft revision and complete reviewed contract.
	// It excludes the one-use token and expiry and never contains raw credentials.
	ApprovalHash string    `json:"approval_hash"`
	Token        string    `json:"token"`
	Expires      time.Time `json:"expires"`
	Revision     string    `json:"revision"`
	PayloadHash  string    `json:"payload_hash"`
	Prepared     Prepared  `json:"prepared"`
}

type Candidate struct {
	ID          string    `json:"id"`
	Theme       string    `json:"theme"`
	Revision    string    `json:"revision"`
	Expires     time.Time `json:"expires"`
	SHA256      string    `json:"sha256"`
	Width       int       `json:"width"`
	Height      int       `json:"height"`
	Bytes       int       `json:"bytes"`
	PayloadHash string    `json:"payload_hash"`
}
