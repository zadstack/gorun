package theme

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

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
	return out
}

// LiveIdentity reads the applied identity of theme slug.
func (s *service) LiveIdentity(ctx context.Context, slug string) IdentitySnapshot {
	snap := IdentitySnapshot{Theme: slug, Values: map[string]string{}, Site: map[string]string{}}
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
	if t, b := snap.Values[idText], snap.Values[idBackground]; t != "" && b != "" && hexColor.MatchString(t) && hexColor.MatchString(b) && contrast(t, b) < minTextContrast {
		fields[idText] = "theme.identity.error_text_contrast"
	}
	if title := snap.Site["site.title"]; title == "" || len(title) > 200 {
		fields["site.title"] = "theme.identity.error_title"
	}
	if len(snap.Site["site.tagline"]) > 500 {
		fields["site.tagline"] = "theme.identity.error_tagline"
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
	return s.d.Settings.Set(ctx, identityDraftKey(snap.Theme), IdentityDraft{Snapshot: snap, SavedAt: db.Now(), Author: identityAuthor(ctx)})
}

// DiscardIdentityDraft removes the pending draft of slug.
func (s *service) DiscardIdentityDraft(ctx context.Context, slug string) error {
	err := s.d.Settings.Delete(ctx, identityDraftKey(slug))
	if errors.Is(err, settings.ErrNotFound) {
		return nil
	}
	return err
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
		hist = []IdentityRevision{{ID: db.NewID(), At: now, Author: author, Kind: "baseline", Snapshot: s.LiveIdentity(ctx, snap.Theme)}}
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
