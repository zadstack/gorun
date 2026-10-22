package theme

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/settings"
)

// Identity draft and revisions. A draft is one pending IdentitySnapshot per
// theme (theme.<slug>.identity_draft); applying writes the live identity and
// site keys in one transaction and appends a revision to the bounded list in
// theme.<slug>.identity_history, so a site can go back to an earlier identity
// without touching content or any other theme setting.

const (
	identityHistoryKeep = 10
	identityDraftSuffix = "identity_draft"
	identityHistSuffix  = "identity_history"

	// minTextContrast is WCAG AA for body text against its background.
	minTextContrast = 4.5
)

// IdentityRevision is one applied identity state.
type IdentityRevision struct {
	ID       string           `json:"id"`
	At       string           `json:"at"`
	Author   string           `json:"author"`
	Kind     string           `json:"kind"` // baseline, apply, revert, import
	Snapshot IdentitySnapshot `json:"snapshot"`
}

// IdentityDraft is the pending, unapplied identity of a theme.
type IdentityDraft struct {
	Snapshot IdentitySnapshot `json:"snapshot"`
	SavedAt  string           `json:"saved_at"`
	Author   string           `json:"author"`
	// Base holds the live site values when the draft was saved, so a draft
	// never restores a title or logo that another apply changed meanwhile.
	Base map[string]string `json:"base,omitempty"`
	// Rebased lists the site keys replaced by their live value on load.
	Rebased []string `json:"-"`
}

// identityLinkKeys are the header link settings a theme may declare.
var identityLinkKeys = []string{"cta_label", "cta_url"}

// safeLinkURL accepts an internal path or an http(s) URL.
func safeLinkURL(v string) bool {
	if v == "" || strings.HasPrefix(v, "/") && !strings.HasPrefix(v, "//") {
		return true
	}
	low := strings.ToLower(v)
	return strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://")
}

// IdentityValidationError lists invalid fields (identity key or site key →
// i18n message key).
type IdentityValidationError struct{ Fields map[string]string }

func (e *IdentityValidationError) Error() string {
	keys := make([]string, 0, len(e.Fields))
	for k := range e.Fields {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return "theme: invalid identity: " + strings.Join(keys, ", ")
}

func identityDraftKey(slug string) string   { return SettingKey(slug, identityDraftSuffix) }
func identityHistoryKey(slug string) string { return SettingKey(slug, identityHistSuffix) }

// normalize trims every value and drops site keys the studio does not own.
func (s IdentitySnapshot) normalize() IdentitySnapshot {
	out := IdentitySnapshot{Theme: s.Theme, Values: map[string]string{}, Site: map[string]string{}}
	for _, k := range IdentityKeys {
		if v := strings.TrimSpace(s.Values[k]); v != "" {
			out.Values[k] = v
		}
	}
	for _, k := range SiteIdentityKeys {
		if v := strings.TrimSpace(s.Site[k]); v != "" {
			out.Site[k] = v
		}
	}
	if s.Links != nil {
		out.Links = map[string]string{}
		for _, k := range identityLinkKeys {
			if v, ok := s.Links[k]; ok {
				out.Links[k] = strings.TrimSpace(v)
			}
		}
	}
	return out
}

// LiveIdentity reads the applied identity of theme slug, header links
// included, so a baseline revision can restore everything the page edits.
func (s *service) LiveIdentity(ctx context.Context, slug string) IdentitySnapshot {
	snap := IdentitySnapshot{Theme: slug, Values: map[string]string{}, Site: map[string]string{}, Links: map[string]string{}}
	for _, k := range identityLinkKeys {
		snap.Links[k] = s.d.Settings.GetString(ctx, SettingKey(slug, k), "")
	}
	for _, k := range IdentityKeys {
		if v := s.d.Settings.GetString(ctx, SettingKey(slug, k), ""); v != "" {
			snap.Values[k] = v
		}
	}
	for _, k := range SiteIdentityKeys {
		if v := s.d.Settings.GetString(ctx, k, ""); v != "" {
			snap.Site[k] = v
		}
	}
	return snap
}

// IdentityDraft returns the pending draft of slug; false when none is stored
// or the stored value cannot be decoded.
func (s *service) IdentityDraft(ctx context.Context, slug string) (IdentityDraft, bool) {
	var d IdentityDraft
	if err := s.d.Settings.Get(ctx, identityDraftKey(slug), &d); err != nil || d.Snapshot.Theme != slug {
		return IdentityDraft{}, false
	}
	if d.Snapshot.Site == nil {
		d.Snapshot.Site = map[string]string{}
	}
	if d.Base != nil {
		live := s.LiveIdentity(ctx, slug).Site
		for _, k := range SiteIdentityKeys {
			if live[k] != d.Base[k] {
				d.Snapshot.Site[k] = live[k]
				d.Rebased = append(d.Rebased, k)
			}
		}
	}
	return d, true
}

// validateSnapshot checks identity values and the site keys; media ids must
// exist in the media library.
func (s *service) validateSnapshot(ctx context.Context, snap IdentitySnapshot) error {
	vals := map[string]any{}
	for k, v := range snap.Values {
		vals[k] = v
	}
	fields := validateIdentity(vals)
	if t, b := s.effectiveTextBackground(ctx, snap); t != "" && b != "" && contrast(t, b) < minTextContrast && contrast(t, b) > 0 {
		fields[idText] = "theme.identity.error_text_contrast"
	}
	if n := utf8.RuneCountInString(snap.Site["site.title"]); n == 0 || n > 200 {
		fields["site.title"] = "theme.identity.error_title"
	}
	if utf8.RuneCountInString(snap.Site["site.tagline"]) > 500 {
		fields["site.tagline"] = "theme.identity.error_tagline"
	}
	if utf8.RuneCountInString(snap.Links["cta_label"]) > 200 {
		fields["cta_label"] = "theme.identity.error_title"
	}
	if !safeLinkURL(snap.Links["cta_url"]) {
		fields["cta_url"] = "theme.identity.error_url"
	}
	ms, hasMedia := kernel.Get[media.Service](s.d, kernel.ServiceMedia)
	for _, k := range []string{"site.logo", "site.logo_light", "site.logo_dark", "site.icon"} {
		id := snap.Site[k]
		if id == "" {
			continue
		}
		if !hasMedia {
			fields[k] = "theme.identity.error_logo"
			continue
		}
		if _, err := ms.Get(ctx, id); err != nil {
			fields[k] = "theme.identity.error_logo"
		}
	}
	if len(fields) > 0 {
		return &IdentityValidationError{Fields: fields}
	}
	return nil
}

// effectiveTextBackground returns the snapshot's text and background colours
// with the theme's declared colours as fallback (only when the snapshot sets
// at least one of them, so an untouched theme is never re-validated).
func (s *service) effectiveTextBackground(ctx context.Context, snap IdentitySnapshot) (text, back string) {
	text, back = snap.Values[idText], snap.Values[idBackground]
	if text == "" && back == "" {
		return "", ""
	}
	if text == "" || back == "" {
		rep := s.Appearance(ctx)
		declared := func(role string) string {
			if rep.Declared == nil {
				return ""
			}
			if v := rep.Declared.Colors[role]; v.Available && v.Value != nil {
				return *v.Value
			}
			return ""
		}
		if text == "" {
			text = declared("text")
		}
		if back == "" {
			back = declared("background")
		}
	}
	if !hexColor.MatchString(text) || !hexColor.MatchString(back) {
		return "", ""
	}
	return text, back
}

func identityAuthor(ctx context.Context) string {
	if u := auth.UserFromContext(ctx); u != nil {
		return u.ID
	}
	return ""
}

// SaveIdentityDraft validates and stores snap as the pending draft of its
// theme. Nothing live changes.
func (s *service) SaveIdentityDraft(ctx context.Context, snap IdentitySnapshot) error {
	snap = snap.normalize()
	if err := s.validateSnapshot(ctx, snap); err != nil {
		return err
	}
	return s.d.Settings.Set(ctx, identityDraftKey(snap.Theme), IdentityDraft{Snapshot: snap, SavedAt: db.Now(), Author: identityAuthor(ctx), Base: s.LiveIdentity(ctx, snap.Theme).Site})
}

// DiscardIdentityDraft removes the pending draft of slug.
func (s *service) DiscardIdentityDraft(ctx context.Context, slug string) error {
	return s.d.Settings.Delete(ctx, identityDraftKey(slug)) // deleting a missing row is not an error
}

// IdentityHistory lists the revisions of slug, newest first. ok is false
// when the stored list cannot be decoded (it is then replaced on the next
// apply).
func (s *service) IdentityHistory(ctx context.Context, slug string) ([]IdentityRevision, bool) {
	var raw json.RawMessage
	if err := s.d.Settings.Get(ctx, identityHistoryKey(slug), &raw); err != nil {
		return nil, errors.Is(err, settings.ErrNotFound)
	}
	var hist []IdentityRevision
	if err := json.Unmarshal(raw, &hist); err != nil {
		return nil, false
	}
	return hist, true
}

// ApplyIdentity writes snap live for its theme and records a revision. The
// first apply of a theme first records the previous live state as the
// "baseline" revision so it can be restored.
func (s *service) ApplyIdentity(ctx context.Context, snap IdentitySnapshot) (IdentityRevision, error) {
	return s.applyIdentity(ctx, snap, "apply")
}

// RevertIdentity re-applies the revision revisionID of slug.
func (s *service) RevertIdentity(ctx context.Context, slug, revisionID string) (IdentityRevision, error) {
	hist, _ := s.IdentityHistory(ctx, slug)
	i := slices.IndexFunc(hist, func(r IdentityRevision) bool { return r.ID == revisionID })
	if i < 0 {
		return IdentityRevision{}, fmt.Errorf("%w: identity revision %q", ErrNotFound, revisionID)
	}
	snap := hist[i].Snapshot
	snap.Theme = slug
	return s.applyIdentity(ctx, snap, "revert")
}

func (s *service) applyIdentity(ctx context.Context, snap IdentitySnapshot, kind string) (IdentityRevision, error) {
	snap = snap.normalize()
	if err := s.validateSnapshot(ctx, snap); err != nil {
		return IdentityRevision{}, err
	}
	author, now := identityAuthor(ctx), db.Now()
	rev := IdentityRevision{ID: db.NewID(), At: now, Author: author, Kind: kind, Snapshot: snap}
	hist, _ := s.IdentityHistory(ctx, snap.Theme)
	if len(hist) == 0 {
		base := s.LiveIdentity(ctx, snap.Theme)
		if base.Site["site.title"] == "" {
			base.Site["site.title"] = s.siteInfo(ctx).Title // a site that never stored a title still gets a restorable baseline
		}
		hist = []IdentityRevision{{ID: db.NewID(), At: now, Author: author, Kind: "baseline", Snapshot: base}}
	}
	hist = append([]IdentityRevision{rev}, hist...)
	if len(hist) > identityHistoryKeep {
		hist = hist[:identityHistoryKeep]
	}
	err := db.InTx(ctx, s.d.DB, func(tx *sql.Tx) error {
		for _, k := range IdentityKeys {
			if err := s.d.Settings.SetTx(ctx, tx, SettingKey(snap.Theme, k), snap.Values[k]); err != nil {
				return err
			}
		}
		for _, k := range SiteIdentityKeys {
			if err := s.d.Settings.SetTx(ctx, tx, k, snap.Site[k]); err != nil {
				return err
			}
		}
		for k, v := range snap.Links {
			if err := s.d.Settings.SetTx(ctx, tx, SettingKey(snap.Theme, k), v); err != nil {
				return err
			}
		}
		if err := s.d.Settings.SetTx(ctx, tx, identityHistoryKey(snap.Theme), hist); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, identityDraftKey(snap.Theme)); err != nil {
			return fmt.Errorf("theme: clear identity draft: %w", err)
		}
		if s.d.Audit != nil {
			s.d.Audit.RecordTx(ctx, tx, "theme.identity."+kind, "theme", snap.Theme, map[string]any{"revision": rev.ID})
		}
		return nil
	})
	if err != nil {
		return IdentityRevision{}, err
	}
	s.d.Settings.Invalidate()
	return rev, nil
}
