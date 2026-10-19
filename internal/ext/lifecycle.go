package ext

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/settings"
)

// Service manages extensions: install (from a directory or a zip),
// activate, deactivate, uninstall, and their delivery log.
type Service interface {
	// InstallDir installs the package in dir (manifest.json + optional
	// assets/). The package is copied to <DataDir>/extensions/<slug>/
	// unless dir already is that directory.
	InstallDir(ctx context.Context, dir string) (*InstallResult, error)
	// InstallZip installs a zipped package (manifest.json at the root or
	// inside one top-level folder), at most MaxZipSize bytes.
	InstallZip(ctx context.Context, r io.ReaderAt, size int64) (*InstallResult, error)
	Activate(ctx context.Context, slug string) error
	Deactivate(ctx context.Context, slug string) error
	// Uninstall removes the extension and revokes its API key. Declared
	// content types, their content, its settings and its package directory
	// are deleted only when purge is true.
	Uninstall(ctx context.Context, slug string, purge bool) error
	List(ctx context.Context) ([]Extension, error)
	Status(ctx context.Context, slug string) (*Extension, error)
	Deliveries(ctx context.Context, slug string, limit int) ([]Delivery, error)
	// Available lists packages present under <DataDir>/extensions/ that are
	// not installed yet.
	Available(ctx context.Context) ([]Manifest, error)
}

func (s *service) extDir(slug string) string {
	return filepath.Join(s.cfg.DataDir, "extensions", slug)
}

func (s *service) List(ctx context.Context) ([]Extension, error) { return s.list(ctx, "") }

func (s *service) Status(ctx context.Context, slug string) (*Extension, error) {
	return s.get(ctx, slug)
}

func (s *service) Available(ctx context.Context) ([]Manifest, error) {
	entries, err := os.ReadDir(filepath.Join(s.cfg.DataDir, "extensions"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ext: scan packages: %w", err)
	}
	var out []Manifest
	for _, e := range entries {
		if !e.IsDir() || !slugRe.MatchString(e.Name()) {
			continue
		}
		m, err := readManifest(filepath.Join(s.cfg.DataDir, "extensions", e.Name()))
		if err != nil || m.Slug != e.Name() {
			continue
		}
		if _, err := s.get(ctx, m.Slug); errors.Is(err, ErrNotFound) {
			out = append(out, *m)
		}
	}
	return out, nil
}

// stage returns a fresh staging directory under <DataDir>/extensions.
func (s *service) stage() (string, error) {
	base := filepath.Join(s.cfg.DataDir, "extensions")
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", fmt.Errorf("ext: create extensions dir: %w", err)
	}
	return os.MkdirTemp(base, stagingDirPrefix)
}

func (s *service) InstallDir(ctx context.Context, dir string) (*InstallResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := readManifest(dir)
	if err != nil {
		return nil, err
	}
	if err := s.check(ctx, m); err != nil {
		return nil, err
	}
	src, _ := filepath.Abs(dir)
	dst, _ := filepath.Abs(s.extDir(m.Slug))
	placed := src != dst
	if placed {
		staging, err := s.stage()
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(staging)
		if err := copyPackage(dir, staging); err != nil {
			return nil, err
		}
		if err := s.place(staging, m.Slug); err != nil {
			return nil, err
		}
	}
	return s.install(ctx, m, placed, InstallSourceDirectory)
}

func (s *service) InstallZip(ctx context.Context, r io.ReaderAt, size int64) (*InstallResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	staging, err := s.stage()
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)
	if err := extractZip(r, size, staging); err != nil {
		return nil, err
	}
	m, err := readManifest(staging)
	if err != nil {
		return nil, err
	}
	if err := s.check(ctx, m); err != nil {
		return nil, err
	}
	if err := s.place(staging, m.Slug); err != nil {
		return nil, err
	}
	return s.install(ctx, m, true, InstallSourceUpload)
}

// place moves a staged package to <DataDir>/extensions/<slug>, replacing a
// leftover directory of a not-installed extension.
func (s *service) place(staging, slug string) error {
	dst := s.extDir(slug)
	if err := os.RemoveAll(dst); err != nil {
		return fmt.Errorf("ext: replace package dir: %w", err)
	}
	if err := os.Rename(staging, dst); err != nil {
		return fmt.Errorf("ext: place package: %w", err)
	}
	return nil
}

// ownTypes returns the content types slug declared in earlier installs.
func (s *service) ownTypes(ctx context.Context, slug string) []string {
	var types []string
	if err := s.settings.Get(ctx, "ext."+slug+".types", &types); err != nil && !errors.Is(err, settings.ErrNotFound) {
		s.log.Warn("ext: read types bookkeeping", "ext", slug, "err", err)
	}
	return types
}

// check validates m against the running system and refuses installed slugs.
func (s *service) check(ctx context.Context, m *Manifest) error {
	env := validateEnv{perms: map[string]bool{}, takenTypes: map[string]bool{}, ownTypes: map[string]bool{}, allowPrivate: s.cfg.AllowPrivateNet}
	for _, p := range s.perms.All() {
		env.perms[p.Name] = true
	}
	if cs := s.content(); cs != nil {
		types, err := cs.Types(ctx)
		if err != nil {
			return err
		}
		for _, t := range types {
			env.takenTypes[t.Slug] = true
		}
	} else if len(m.ContentTypes) > 0 {
		return errors.New("ext: content service unavailable; cannot declare content types")
	}
	if slugRe.MatchString(m.Slug) {
		for _, t := range s.ownTypes(ctx, m.Slug) {
			env.ownTypes[t] = true
		}
	}
	errs := m.validate(env)
	if _, bad := errs["slug"]; !bad {
		if _, err := s.get(ctx, m.Slug); err == nil {
			errs["slug"] = "ext.validation.slug_taken"
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	if len(errs) > 0 {
		return &ValidationError{Fields: errs}
	}
	return nil
}

func randomSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("ext: crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// owner returns the user who owns the extension's API key: the acting user
// when there is one, else the oldest active admin.
func (s *service) owner(ctx context.Context) (string, error) {
	if u := auth.UserFromContext(ctx); u != nil && u.Status == auth.StatusActive {
		return u.ID, nil
	}
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM users WHERE role = 'admin' AND status = 'active' ORDER BY created_at, id LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNoOwner
	}
	if err != nil {
		return "", fmt.Errorf("ext: find key owner: %w", err)
	}
	return id, nil
}

// install stores a validated, placed package: row + subscriptions, content
// types, setting defaults, the scoped API key. On failure every step done
// so far is undone; placed reports whether this install put the package
// directory in place (and may therefore remove it).
func (s *service) install(ctx context.Context, m *Manifest, placed bool, source string) (*InstallResult, error) {
	if source != InstallSourceDirectory && source != InstallSourceUpload {
		return nil, errors.New("ext: invalid installation method")
	}
	var undo []func(context.Context)
	fail := func(err error) (*InstallResult, error) {
		uctx := context.WithoutCancel(ctx)
		for i := len(undo) - 1; i >= 0; i-- {
			undo[i](uctx)
		}
		return nil, err
	}
	if placed {
		undo = append(undo, func(context.Context) {
			if err := os.RemoveAll(s.extDir(m.Slug)); err != nil {
				s.log.Error("ext: roll back package dir", "ext", m.Slug, "err", err)
			}
		})
	}
	ownerID := ""
	if len(m.Permissions) > 0 {
		id, err := s.owner(ctx)
		if err != nil {
			return fail(err)
		}
		ownerID = id
	}
	secret := randomSecret()
	enc, err := db.Encrypt(s.cfg.SecretKey, []byte(secret), "ext:"+m.Slug)
	if err != nil {
		return fail(fmt.Errorf("ext: encrypt secret: %w", err))
	}
	raw, _ := json.Marshal(m)
	now := db.FormatTime(s.now())
	err = db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO extensions (slug, name, version, manifest_json, status, endpoint_url, secret_enc, installed_at, install_source)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, m.Slug, m.DisplayName("en"), m.Version, string(raw), StatusInstalled, m.Endpoint.URL, enc, now, source); err != nil {
			return fmt.Errorf("ext: insert: %w", err)
		}
		for _, h := range m.Hooks {
			if _, err := tx.ExecContext(ctx, `INSERT INTO extension_subscriptions (extension_slug, hook, kind, timeout_ms) VALUES (?, ?, ?, ?)`,
				m.Slug, h.Name, h.Kind, h.TimeoutMS); err != nil {
				return fmt.Errorf("ext: insert subscription: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return fail(err)
	}
	undo = append(undo, func(uctx context.Context) {
		if _, err := s.db.ExecContext(uctx, `DELETE FROM extensions WHERE slug = ?`, m.Slug); err != nil {
			s.log.Error("ext: roll back install row", "ext", m.Slug, "err", err)
		}
	})
	res, err := s.provision(ctx, m, ownerID, &undo)
	if err != nil {
		return fail(err)
	}
	res.Secret = secret
	s.audit.Record(ctx, "ext.install", "extension", m.Slug, map[string]any{"version": m.Version, "permissions": m.Permissions,
		"hooks": len(m.Hooks), "content_types": m.typeSlugs()})
	return res, nil
}

// provision registers content types and settings and issues the API key,
// pushing an undo step for everything it creates.
func (s *service) provision(ctx context.Context, m *Manifest, ownerID string, undo *[]func(context.Context)) (*InstallResult, error) {
	created, err := s.ensureTypes(ctx, m)
	*undo = append(*undo, func(uctx context.Context) {
		cs := s.content()
		for _, t := range created {
			if err := cs.DeleteType(uctx, t); err != nil {
				s.log.Error("ext: roll back content type", "ext", m.Slug, "type", t, "err", err)
			}
		}
	})
	if err != nil {
		return nil, err
	}
	typesKey := "ext." + m.Slug + ".types"
	var prevTypes json.RawMessage
	hadTypes := s.settings.Get(ctx, typesKey, &prevTypes) == nil
	types := s.ownTypes(ctx, m.Slug)
	for _, t := range m.typeSlugs() {
		if !slices.Contains(types, t) {
			types = append(types, t)
		}
	}
	if err := s.settings.Set(ctx, typesKey, types); err != nil {
		return nil, err
	}
	*undo = append(*undo, func(uctx context.Context) {
		if hadTypes {
			_ = s.settings.Set(uctx, typesKey, prevTypes)
		} else {
			s.deleteSetting(uctx, typesKey)
		}
	})
	for _, f := range m.Settings {
		key := settingKey(m.Slug, f.Key)
		var cur json.RawMessage
		if err := s.settings.Get(ctx, key, &cur); err == nil || f.Default == nil {
			continue // keep a value from an earlier install
		}
		if err := s.settings.Set(ctx, key, f.Default); err != nil {
			return nil, err
		}
		*undo = append(*undo, func(uctx context.Context) { s.deleteSetting(uctx, key) })
	}
	res := &InstallResult{}
	if len(m.Permissions) > 0 {
		plain, k, err := s.keys.Create(ctx, "ext:"+m.Slug, ownerID, m.Permissions, 0, "")
		if err != nil {
			return nil, fmt.Errorf("ext: issue api key: %w", err)
		}
		*undo = append(*undo, func(uctx context.Context) { _ = s.keys.Revoke(uctx, k.ID) })
		if _, err := s.db.ExecContext(ctx, `UPDATE extensions SET api_key_id = ? WHERE slug = ?`, k.ID, m.Slug); err != nil {
			return nil, fmt.Errorf("ext: store api key id: %w", err)
		}
		res.APIKey = plain
	}
	x, err := s.get(ctx, m.Slug)
	if err != nil {
		return nil, err
	}
	res.Extension = x
	return res, nil
}

// deleteSetting removes one settings key, logging failures.
func (s *service) deleteSetting(ctx context.Context, key string) {
	if err := s.settings.Delete(ctx, key); err != nil {
		s.log.Error("ext: delete setting", "key", key, "err", err)
	}
}

func settingKey(slug, key string) string { return "ext." + slug + ".settings." + key }

// ensureTypes creates m's content types that do not exist yet and returns
// the slugs it created. Existing types (kept from an earlier install,
// possibly edited by an admin) are never overwritten.
func (s *service) ensureTypes(ctx context.Context, m *Manifest) ([]string, error) {
	if len(m.ContentTypes) == 0 {
		return nil, nil
	}
	cs := s.content()
	if cs == nil {
		return nil, errors.New("ext: content service unavailable")
	}
	var created []string
	for _, t := range m.ContentTypes {
		if _, err := cs.GetType(ctx, t.Slug); err == nil {
			continue
		} else if !errors.Is(err, content.ErrNotFound) {
			return created, err
		}
		t.System = false
		if err := cs.SaveType(ctx, t); err != nil {
			return created, fmt.Errorf("ext: register content type %s: %w", t.Slug, err)
		}
		created = append(created, t.Slug)
	}
	return created, nil
}

func (s *service) Activate(ctx context.Context, slug string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	x, err := s.get(ctx, slug)
	if err != nil {
		return err
	}
	if x.Status == StatusActive {
		return nil
	}
	if _, err := s.ensureTypes(ctx, &x.Manifest); err != nil {
		return err
	}
	secret, err := db.Decrypt(s.cfg.SecretKey, x.secretEnc, "ext:"+slug)
	if err != nil {
		return fmt.Errorf("ext: decrypt secret of %s: %w", slug, err)
	}
	if err := s.setStatus(ctx, slug, StatusActive, `, failures = 0, last_error = '', activated_at = ?`, db.FormatTime(s.now())); err != nil {
		return err
	}
	s.rt.activate(s, x, secret)
	s.audit.Record(ctx, "ext.activate", "extension", slug, map[string]any{"version": x.Version, "from": x.Status})
	return nil
}

func (s *service) Deactivate(ctx context.Context, slug string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	x, err := s.get(ctx, slug)
	if err != nil {
		return err
	}
	s.rt.deactivate(s, slug)
	if x.Status == StatusDisabled || x.Status == StatusInstalled {
		return nil
	}
	if err := s.setStatus(ctx, slug, StatusDisabled, ""); err != nil {
		return err
	}
	s.audit.Record(ctx, "ext.deactivate", "extension", slug, map[string]any{"from": x.Status})
	return nil
}

func (s *service) Uninstall(ctx context.Context, slug string, purge bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	x, err := s.get(ctx, slug)
	if err != nil {
		return err
	}
	// Stop delivery first. With purge, content, settings and files are
	// removed while the extension row still exists, so any failure leaves
	// it installed (disabled) and the uninstall can simply be retried.
	s.rt.deactivate(s, slug)
	if x.Status == StatusActive {
		if err := s.setStatus(ctx, slug, StatusDisabled, ""); err != nil {
			return err
		}
	}
	meta := map[string]any{"version": x.Version, "purge": purge}
	if purge {
		n, err := s.purgeContent(ctx, x)
		meta["deleted_items"] = n
		if err != nil {
			return err
		}
		if err := s.purgeFiles(ctx, slug); err != nil {
			return err
		}
	}
	if x.APIKeyID != "" {
		if err := s.keys.Revoke(ctx, x.APIKeyID); err != nil && !errors.Is(err, auth.ErrNotFound) {
			return fmt.Errorf("ext: revoke api key: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM extensions WHERE slug = ?`, slug); err != nil {
		return fmt.Errorf("ext: delete %s: %w", slug, err)
	}
	s.rt.remove(s, slug)
	s.audit.Record(ctx, "ext.uninstall", "extension", slug, meta)
	return nil
}

// purgeContent hard-deletes the content of every type the extension
// declared (in this or earlier installs) and the types themselves.
func (s *service) purgeContent(ctx context.Context, x *Extension) (int, error) {
	types := s.ownTypes(ctx, x.Slug)
	for _, t := range x.Manifest.typeSlugs() {
		if !slices.Contains(types, t) {
			types = append(types, t)
		}
	}
	deleted := 0
	if cs := s.content(); cs != nil {
		for _, t := range types {
			if _, err := cs.GetType(ctx, t); errors.Is(err, content.ErrNotFound) {
				s.log.Info("ext: purge skips missing content type", "ext", x.Slug, "type", t)
				continue
			} else if err != nil {
				return deleted, err
			}
			n, err := purgeType(ctx, cs, t)
			deleted += n
			if err != nil {
				return deleted, err
			}
		}
	}
	return deleted, nil
}

// purgeFiles removes the extension's settings and package directory.
func (s *service) purgeFiles(ctx context.Context, slug string) error {
	if err := s.settings.DeletePrefix(ctx, "ext."+slug+"."); err != nil {
		return fmt.Errorf("ext: purge settings: %w", err)
	}
	if err := os.RemoveAll(s.extDir(slug)); err != nil {
		return fmt.Errorf("ext: remove package dir: %w", err)
	}
	return nil
}

func purgeType(ctx context.Context, cs content.Service, typ string) (int, error) {
	n := 0
	for {
		page, err := cs.Query(ctx, content.Query{Type: typ, IncludeTrash: true, Page: 1, PerPage: 100})
		if err != nil {
			return n, fmt.Errorf("ext: purge %s: %w", typ, err)
		}
		if len(page.Items) == 0 {
			break
		}
		for _, it := range page.Items {
			if err := cs.Delete(ctx, it.ID, true); err != nil && !errors.Is(err, content.ErrNotFound) {
				return n, fmt.Errorf("ext: purge item %s: %w", it.ID, err)
			}
			n++
		}
	}
	if err := cs.DeleteType(ctx, typ); err != nil && !errors.Is(err, content.ErrNotFound) {
		return n, fmt.Errorf("ext: purge type %s: %w", typ, err)
	}
	return n, nil
}

// trimErr shortens an error message for storage and display.
func trimErr(msg string) string {
	msg = strings.TrimSpace(msg)
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	return msg
}
