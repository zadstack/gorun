package theme

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/zadstack/gorun/internal/db"
)

var ErrIdentityLogoConflict = errors.New("theme: identity changed since logo review")
var ErrIdentityLogoMedia = errors.New("theme: logo must reference an existing PNG")

// IdentityLogoStore is the bounded interface used by logo generation bridges.
// It deliberately exposes neither ApplyIdentity nor whole-snapshot saving.
type IdentityLogoStore interface {
	IdentityLogoRevision(context.Context, string) (string, error)
	SaveIdentityLogo(ctx context.Context, slug, revision, slot, mediaID string) error
}

type logoState struct {
	revision string
	draft    IdentityDraft
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
		if draft.Base != nil {
			for _, k := range SiteIdentityKeys {
				if live.Site[k] != draft.Base[k] {
					draft.Snapshot.Site[k] = live.Site[k]
				}
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
	return logoState{revision: hex.EncodeToString(hash[:]), draft: draft}, nil
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

// SaveIdentityLogo compares and merges within BEGIN IMMEDIATE. Unrelated draft
// values, links, title, tagline and other logos survive. Live keys never change.
func (s *service) SaveIdentityLogo(ctx context.Context, slug, revision, slot, mediaID string) error {
	if !logoKeys[slot] || revision == "" || mediaID == "" {
		return ErrIdentityLogoMedia
	}
	err := db.InTx(ctx, s.d.DB, func(tx *sql.Tx) error {
		state, err := s.logoStateTx(ctx, tx, slug)
		if err != nil {
			return err
		}
		if state.revision != revision {
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
