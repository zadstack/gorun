// Package identitylogoadmin exposes the optional static-logo workflow.
package identitylogoadmin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"image"
	"image/png"
	"io/fs"

	"golang.org/x/image/draw"

	coreadmin "github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/identitylogo"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/theme"
)

// The renderer resolves module extensions below its admin/templates prefix.
//
//go:embed admin/templates/pages/identitylogo/*.html catalog/*.json
var files embed.FS

const pagePath = "/appearance/identity/logo"

type Module struct{}

func New() *Module                { return &Module{} }
func (*Module) Name() string      { return "identitylogo" }
func (*Module) Migrations() fs.FS { return nil }
func (*Module) Catalogs() fs.FS   { return files }
func (*Module) Enabled(ctx context.Context, d *sql.DB) (bool, error) {
	return kernel.AddonEnabled(ctx, d, "identitylogo")
}
func (*Module) Register(d *kernel.Deps) error {
	tpl, ok := kernel.Get[*httpx.Templates](d, kernel.ServiceAdminTemplates)
	if !ok {
		return errors.New("identitylogo: admin templates unavailable")
	}
	if e := tpl.RegisterExtension(files); e != nil {
		return e
	}
	h := &handlers{d: d, tpl: tpl}
	h.service = identitylogo.NewService(bridgeHooks(d), &draftStore{d}, identitylogo.Options{Enabled: func(ctx context.Context) bool {
		on, e := kernel.AddonEnabled(ctx, d.DB, "identitylogo")
		return e == nil && on && d.Config.Addons["identitylogo"] != kernel.AddonModeDisabled
	}})
	d.Services.Set("identitylogo", h.service)
	d.Admin.AddNav(kernel.NavItem{ID: "appearance.identity_logo", LabelKey: "identitylogo.nav", Icon: "image", Href: "/admin" + pagePath, Permission: "settings.manage", Group: "appearance", Position: 3})
	r := d.Router.Admin.With(auth.RequirePerm("settings.manage"))
	r.Get(pagePath, h.page)
	r.With(auth.RequirePerm("media.upload")).Post(pagePath, h.submit)
	r.With(auth.RequirePerm("media.upload")).Get(pagePath+"/candidate/{id}.png", h.image)
	return nil
}

var _ kernel.OptionalModule = (*Module)(nil)

func bridgeInput(in identitylogo.Input) coreadmin.LogoImageInput {
	return coreadmin.LogoImageInput{ConnectionID: in.ConnectionID, Theme: in.Theme, Prompt: in.Prompt, Size: in.Size, BudgetMicros: in.BudgetMicros, Currency: in.Currency}
}
func fromBridge(p coreadmin.LogoImagePrepared, in identitylogo.Input) identitylogo.Prepared {
	return identitylogo.Prepared{Input: in, PromptVersion: p.PromptVersion, Destination: identitylogo.Destination{ConnectionID: in.ConnectionID, Name: p.Name, Provider: p.Provider, Model: p.Model, Host: p.Host, Fingerprint: p.Fingerprint}, Capability: identitylogo.Capability{StaticPNG: p.StaticPNG, Fingerprint: p.CapabilityFingerprint, Evidence: p.Evidence}, Quote: identitylogo.Quote{Known: p.Quote.Known, AmountMicros: p.Quote.AmountMicros, Currency: p.Quote.Currency, Source: p.Quote.Source, Version: p.Quote.Version, AsOf: p.Quote.AsOf, Basis: p.Quote.Basis}, Payload: p.Payload}
}
func toBridge(p identitylogo.Prepared) coreadmin.LogoImagePrepared {
	return coreadmin.LogoImagePrepared{Input: bridgeInput(p.Input), Name: p.Destination.Name, Provider: p.Destination.Provider, Model: p.Destination.Model, Host: p.Destination.Host, Fingerprint: p.Destination.Fingerprint, CapabilityFingerprint: p.Capability.Fingerprint, Evidence: p.Capability.Evidence, StaticPNG: p.Capability.StaticPNG, PromptVersion: p.PromptVersion, Quote: coreadmin.LogoImageQuote{Known: p.Quote.Known, AmountMicros: p.Quote.AmountMicros, Currency: p.Quote.Currency, Source: p.Quote.Source, Version: p.Quote.Version, AsOf: p.Quote.AsOf, Basis: p.Quote.Basis}, Payload: p.Payload}
}
func bridgeHooks(d *kernel.Deps) identitylogo.Hooks {
	return identitylogo.Hooks{
		Prepare: func(ctx context.Context, in identitylogo.Input) (identitylogo.Prepared, error) {
			p, e := coreadmin.PrepareIdentityLogo(ctx, d, bridgeInput(in), identitylogo.PromptVersion)
			return fromBridge(p, in), e
		},
		GenerateBounded: func(ctx context.Context, p identitylogo.Prepared) ([]byte, error) {
			b, e := coreadmin.GenerateIdentityLogoBounded(ctx, d, toBridge(p))
			if errors.Is(e, coreadmin.ErrLogoChanged) {
				return nil, identitylogo.ErrChanged
			}
			return b, e
		},
		Record: func(ctx context.Context, e identitylogo.AuditEvent) {
			if d.Audit == nil {
				return
			}
			d.Audit.Record(ctx, "identitylogo."+e.Action, "theme", e.Theme, map[string]any{
				"outcome":                 e.Outcome,
				"owner":                   e.Owner,
				"connection":              e.ConnectionID,
				"model":                   e.Model,
				"payload_hash":            e.PayloadHash,
				"approval_hash":           e.ApprovalHash,
				"destination_fingerprint": e.DestinationFingerprint,
				"capability_fingerprint":  e.CapabilityFingerprint,
				"quote_hash":              e.QuoteHash,
				"quote_version":           e.QuoteVersion,
				"draft_revision_hash":     e.DraftRevisionHash,
				"prompt_version":          e.PromptVersion,
				"estimate_micros":         e.EstimateMicros,
				"currency":                e.Currency,
				"candidate_hash":          e.CandidateHash,
			})
		},
	}
}

type draftStore struct{ d *kernel.Deps }

func (s *draftStore) store() (theme.IdentityLogoStore, error) {
	st, ok := kernel.Get[theme.IdentityLogoStore](s.d, kernel.ServiceTheme)
	if !ok {
		return nil, identitylogo.ErrUnsupported
	}
	return st, nil
}
func (s *draftStore) Revision(ctx context.Context, slug string) (string, error) {
	st, e := s.store()
	if e != nil {
		return "", e
	}
	return st.IdentityLogoRevision(ctx, slug)
}

func (s *draftStore) selectionStore() (theme.IdentityLogoSelectionStore, error) {
	st, ok := kernel.Get[theme.IdentityLogoSelectionStore](s.d, kernel.ServiceTheme)
	if !ok {
		return nil, identitylogo.ErrUnsupported
	}
	return st, nil
}
func (s *draftStore) Review(ctx context.Context, slug string) (string, map[string]string, error) {
	st, err := s.selectionStore()
	if err != nil {
		return "", nil, err
	}
	return st.IdentityLogoReview(ctx, slug)
}

type importedConflict struct {
	MediaID string
	Cause   error
}

func (e *importedConflict) Error() string {
	return "logo imported but draft selection did not complete; draft was not modified"
}
func (e *importedConflict) Unwrap() error { return e.Cause }

func (s *draftStore) SelectPNG(ctx context.Context, slug, baseline, slot string, png []byte) error {
	if !auth.Can(ctx, "settings.manage") || !auth.Can(ctx, "media.upload") {
		return identitylogo.ErrUnsupported
	}
	st, e := s.selectionStore()
	if e != nil {
		return e
	}
	_, current, e := st.IdentityLogoReview(ctx, slug)
	if e != nil || baseline == "" || current[slot] != baseline {
		return identitylogo.ErrChanged
	}
	library, ok := kernel.Get[media.Service](s.d, kernel.ServiceMedia)
	if !ok {
		return identitylogo.ErrUnsupported
	}
	selected, e := selectedLogoPNG(png, slot)
	if e != nil {
		return identitylogo.ErrImage
	}
	on, e := kernel.AddonEnabled(ctx, s.d.DB, "identitylogo")
	if e != nil || !on || s.d.Config.Addons["identitylogo"] == kernel.AddonModeDisabled {
		return identitylogo.ErrDisabled
	}
	asset, e := library.Upload(ctx, "identity-logo-"+db.NewID()+".png", bytes.NewReader(selected), media.UploadMeta{AuthorID: owner(ctx), Title: "Identity logo proposal"})
	if e != nil {
		return e
	}
	if s.d.Audit != nil {
		sourceHash, derivedHash := sha256.Sum256(png), sha256.Sum256(selected)
		s.d.Audit.Record(ctx, "identitylogo.import", "media", asset.ID, map[string]any{"theme": slug, "slot": slot, "candidate_sha256": hex.EncodeToString(sourceHash[:]), "derived_input_sha256": hex.EncodeToString(derivedHash[:]), "derived_sha256": asset.Checksum, "width": asset.Width, "height": asset.Height, "bytes": asset.Size})
	}
	if e = st.SaveIdentityLogoSlot(ctx, slug, baseline, slot, asset.ID); e != nil {
		// Once imported, media can acquire references independently. Never delete it
		// on a draft conflict; report the new asset for explicit human review.
		if s.d.Audit != nil {
			s.d.Audit.Record(ctx, "identitylogo.import_unselected", "media", asset.ID, map[string]any{"theme": slug})
		}
		cause := e
		if errors.Is(e, theme.ErrIdentityLogoConflict) {
			cause = identitylogo.ErrChanged
		}
		if errors.Is(e, theme.ErrIdentityLogoDisabled) {
			cause = identitylogo.ErrDisabled
		}
		return &importedConflict{MediaID: asset.ID, Cause: cause}
	}
	return nil
}
func owner(ctx context.Context) string {
	if u := auth.UserFromContext(ctx); u != nil {
		return u.ID
	}
	return ""
}

// Selection is a local derivative, never another generation or a recoloring.
func selectedLogoPNG(raw []byte, slot string) ([]byte, error) {
	source, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	size := 512
	if slot == "site.icon" {
		size = 64
	}
	target := image.NewNRGBA(image.Rect(0, 0, size, size))
	draw.CatmullRom.Scale(target, target.Bounds(), source, source.Bounds(), draw.Src, nil)
	var encoded bytes.Buffer
	err = png.Encode(&encoded, target)
	return encoded.Bytes(), err
}
