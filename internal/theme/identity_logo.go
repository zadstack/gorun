package theme

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
)

var ErrIdentityLogoConflict = errors.New("theme: identity changed since logo review")
var ErrIdentityLogoDisabled = errors.New("theme: logo module disabled before draft selection")
var ErrIdentityLogoMedia = errors.New("theme: logo must reference an existing PNG")

// IdentityLogoStore is the bounded interface used by logo generation bridges.
// It deliberately exposes neither ApplyIdentity nor whole-snapshot saving.
type IdentityLogoStore interface {
	IdentityLogoRevision(context.Context, string) (string, error)
	SaveIdentityLogo(ctx context.Context, slug, revision, slot, mediaID string) error
}

// IdentityLogoSelectionStore captures all target baselines with the full review
// revision, then compares one target inside the final draft transaction.
type IdentityLogoSelectionStore interface {
	IdentityLogoReview(context.Context, string) (revision string, slotBaselines map[string]string, err error)
	SaveIdentityLogoSlot(ctx context.Context, slug, baseline, slot, mediaID string) error
}

type logoState struct {
	revision       string
	slotBaselines  map[string]string
	draft          IdentityDraft
	selectionDraft IdentityDraft
	liveSite       map[string]string
}

// logoStateTx reads only identity-owned settings through the same transaction.
// Revision includes active theme, live keys and raw pending draft, even when no
// draft exists. Neither stale settings cache nor a second pool read is involved.
func (s *service) logoStateTx(ctx context.Context, tx *sql.Tx, slug string) (logoState, error) {
	keys := []string{SettingTheme, identityDraftKey(slug)}
	for _, k := range IdentityKeys {
		keys = append(keys, SettingKey(slug, k))
	}
	for _, k := range identityLinkKeys {
		keys = append(keys, SettingKey(slug, k))
	}
	keys = append(keys, SiteIdentityKeys...)
	raw := map[string]string{}
	for _, k := range keys {
		var value string
		err := tx.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key = ?`, k).Scan(&value)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return logoState{}, err
		}
		raw[k] = value
	}
	active := s.d.Config.Theme
	if active == "" {
		active = defaultTheme
	}
	if value, ok := raw[SettingTheme]; ok {
		if err := json.Unmarshal([]byte(value), &active); err != nil {
			return logoState{}, err
		}
	}
	if active != slug {
		return logoState{}, ErrIdentityLogoConflict
	}
	live := IdentitySnapshot{Theme: slug, Values: map[string]string{}, Site: map[string]string{}, Links: map[string]string{}}
	read := func(key string) (string, error) {
		var v string
		if raw[key] == "" {
			return "", nil
		}
		err := json.Unmarshal([]byte(raw[key]), &v)
		return v, err
	}
	for _, k := range SiteIdentityKeys {
		v, err := read(k)
		if err != nil {
			return logoState{}, err
		}
		live.Site[k] = v
	}
	for _, k := range IdentityKeys {
		v, err := read(SettingKey(slug, k))
		if err != nil {
			return logoState{}, err
		}
		live.Values[k] = v
	}
	for _, k := range identityLinkKeys {
		v, err := read(SettingKey(slug, k))
		if err != nil {
			return logoState{}, err
		}
		live.Links[k] = v
	}
	draft := IdentityDraft{Snapshot: live, Base: live.Site}
	if value, ok := raw[identityDraftKey(slug)]; ok {
		draft = IdentityDraft{}
		if err := json.Unmarshal([]byte(value), &draft); err != nil {
			return logoState{}, err
		}
		if draft.Snapshot.Theme != slug {
			return logoState{}, ErrIdentityLogoConflict
		}
		if draft.Snapshot.Site == nil {
			draft.Snapshot.Site = map[string]string{}
		}
	}
	selectionDraft := draft
	selectionDraft.Snapshot.Site = maps.Clone(draft.Snapshot.Site)
	selectionDraft.Snapshot.Values = maps.Clone(draft.Snapshot.Values)
	selectionDraft.Snapshot.Links = maps.Clone(draft.Snapshot.Links)
	selectionDraft.Base = maps.Clone(draft.Base)
	if draft.Base != nil {
		for _, k := range SiteIdentityKeys {
			if live.Site[k] != draft.Base[k] {
				draft.Snapshot.Site[k] = live.Site[k]
			}
		}
	}
	encoded, err := json.Marshal(struct {
		Theme  string
		Values map[string]string
	}{active, raw})
	if err != nil {
		return logoState{}, err
	}
	hash := sha256.Sum256(encoded)
	// Base is freshly live, matching the rebased effective draft; clone to avoid
	// aliasing a newly selected logo into the live baseline.
	draft.Base = make(map[string]string, len(live.Site))
	for k, v := range live.Site {
		draft.Base[k] = v
	}
	baselines := make(map[string]string, len(logoKeys))
	for slot := range logoKeys {
		// Values only: draft existence, metadata and unrelated identity fields do
		// not invalidate paid work. Include live even when a pending value masks it.
		value, err := json.Marshal(struct{ Theme, Live, Pending string }{active, live.Site[slot], draft.Snapshot.Site[slot]})
		if err != nil {
			return logoState{}, err
		}
		sum := sha256.Sum256(value)
		baselines[slot] = hex.EncodeToString(sum[:])
	}
	return logoState{revision: hex.EncodeToString(hash[:]), slotBaselines: baselines, draft: draft, selectionDraft: selectionDraft, liveSite: live.Site}, nil
}

func (s *service) IdentityLogoRevision(ctx context.Context, slug string) (string, error) {
	var revision string
	err := db.InTx(ctx, s.d.DB, func(tx *sql.Tx) error {
		state, err := s.logoStateTx(ctx, tx, slug)
		if err == nil {
			revision = state.revision
		}
		return err
	})
	return revision, err
}

// IdentityLogoReview captures the full revision and all target baselines from
// the same transaction. No selection baseline is read from a later snapshot.
func (s *service) IdentityLogoReview(ctx context.Context, slug string) (revision string, baselines map[string]string, err error) {
	err = db.InTx(ctx, s.d.DB, func(tx *sql.Tx) error {
		state, e := s.logoStateTx(ctx, tx, slug)
		if e == nil {
			revision, baselines = state.revision, state.slotBaselines
		}
		return e
	})
	return
}

// SaveIdentityLogo retains the existing full-revision CAS contract.
func (s *service) SaveIdentityLogo(ctx context.Context, slug, revision, slot, mediaID string) error {
	return s.saveIdentityLogo(ctx, slug, revision, slot, mediaID, false)
}

// SaveIdentityLogoSlot compares only target values and merges into the latest
// draft under BEGIN IMMEDIATE; all unrelated latest fields survive.
func (s *service) SaveIdentityLogoSlot(ctx context.Context, slug, baseline, slot, mediaID string) error {
	return s.saveIdentityLogo(ctx, slug, baseline, slot, mediaID, true)
}

func (s *service) saveIdentityLogo(ctx context.Context, slug, revision, slot, mediaID string, slotScoped bool) error {
	if !logoKeys[slot] || revision == "" || mediaID == "" {
		return ErrIdentityLogoMedia
	}
	err := db.InTx(ctx, s.d.DB, func(tx *sql.Tx) error {
		if slotScoped {
			if s.d.Config.Addons["identitylogo"] == kernel.AddonModeDisabled {
				return ErrIdentityLogoDisabled
			}
			enabled, err := kernel.AddonEnabledTx(ctx, tx, "identitylogo")
			if err != nil {
				return err
			}
			if !enabled {
				return ErrIdentityLogoDisabled
			}
		}
		state, err := s.logoStateTx(ctx, tx, slug)
		if err != nil {
			return err
		}
		current := state.revision
		if slotScoped {
			current = state.slotBaselines[slot]
		}
		if current != revision {
			return ErrIdentityLogoConflict
		}
		var mime string
		if err := tx.QueryRowContext(ctx, `SELECT mime FROM media WHERE id = ?`, mediaID).Scan(&mime); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrIdentityLogoMedia
			}
			return err
		}
		if mime != "image/png" {
			return ErrIdentityLogoMedia
		}
		if slotScoped {
			// Preserve the latest raw unrelated pending fields and baselines. Only
			// this target needs a fresh Base to keep its new value effective.
			state.draft = state.selectionDraft
			if state.draft.Base != nil {
				state.draft.Base[slot] = state.liveSite[slot]
			}
		}
		state.draft.Snapshot.Site[slot] = mediaID
		state.draft.SavedAt = db.Now()
		state.draft.Author = identityAuthor(ctx)
		if err := s.d.Settings.SetTx(ctx, tx, identityDraftKey(slug), state.draft); err != nil {
			return err
		}
		if s.d.Audit != nil {
			s.d.Audit.RecordTx(ctx, tx, "theme.identity.logo_draft", "theme", slug, map[string]any{"slot": slot, "media": mediaID})
		}
		return nil
	})
	if err == nil {
		s.d.Settings.Invalidate()
	}
	return err
}

var _ IdentityLogoStore = (*service)(nil)
var _ IdentityLogoSelectionStore = (*service)(nil)
