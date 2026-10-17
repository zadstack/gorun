package theme

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Overrides live in <DataDir>/overrides/<active theme>/ mirroring the theme
// layout; drafts in its _drafts/ subdirectory, which only preview renders
// (?gp_preview_override=1 with themes.manage) read.

func (s *service) overridePaths(ctx context.Context, name string) (published, draft string, err error) {
	rel, err := relPath(name)
	if err != nil {
		return "", "", err
	}
	od, err := overridesDir(s.d.Config, s.activeSlug(ctx))
	if err != nil {
		return "", "", err
	}
	return filepath.Join(od, rel), filepath.Join(od, draftsSubdir, rel), nil
}

// checkTemplate rejects syntax errors, unknown funcs and html/template
// escaper errors (e.g. an unterminated attribute), which only surface on
// the first Execute. The template is executed into a writer that fails on
// the first byte: escaping happens before any output, so escaper errors
// (*template.Error) are caught while funcs barely run. Runtime errors from
// the empty context (nil fields, index out of range) are not template
// errors and are ignored.
func (s *service) checkTemplate(ctx context.Context, name, body string) error {
	st := &reqState{ctx: ctx, tc: &Context{Extra: map[string]any{}, Query: map[string]string{}}, set: &compiled{}}
	t, err := template.New(name).Funcs(s.funcMap(st)).Parse(body)
	if err != nil {
		return &templateError{detail: err.Error()}
	}
	var tErr *template.Error
	if err := t.Execute(stopWriter{}, st.tc); errors.As(err, &tErr) {
		return &templateError{detail: err.Error()}
	}
	return nil
}

// templateError is an ErrInvalidTemplate carrying the parser/escaper
// diagnostic about the submitted source.
type templateError struct{ detail string }

func (e *templateError) Error() string { return ErrInvalidTemplate.Error() + ": " + e.detail }
func (e *templateError) Unwrap() error { return ErrInvalidTemplate }

// stopWriter fails every write so checkTemplate's dry run ends at the first
// output.
type stopWriter struct{}

var errStopWrite = errors.New("theme: dry run")

func (stopWriter) Write([]byte) (int, error) { return 0, errStopWrite }

func writeFile(p, body string) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("theme: create override dir: %w", err)
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".override-*.tmp")
	if err != nil {
		return fmt.Errorf("theme: write override: %w", err)
	}
	tmp := f.Name()
	_, werr := f.WriteString(body)
	cerr := f.Close()
	if err := errors.Join(werr, cerr, os.Chmod(tmp, 0o644)); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("theme: write override: %w", err)
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("theme: write override: %w", err)
	}
	return nil
}

// SaveOverride writes body as the draft (draft=true) or the published
// override of name; publishing directly also discards any draft.
func (s *service) SaveOverride(ctx context.Context, name, body string, draft bool) error {
	pub, dr, err := s.overridePaths(ctx, name)
	if err != nil {
		return err
	}
	if err := s.checkTemplate(ctx, name, body); err != nil {
		return err
	}
	target := pub
	if draft {
		target = dr
	}
	if err := writeFile(target, body); err != nil {
		return err
	}
	if !draft {
		if err := os.Remove(dr); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("theme: remove draft: %w", err)
		}
	}
	s.invalidate()
	s.d.Audit.Record(ctx, "theme.override.save", "template", name, map[string]any{"draft": draft, "theme": s.activeSlug(ctx)})
	return nil
}

// readDraft returns the draft override of name, if any.
func (s *service) readDraft(ctx context.Context, name string) (string, bool) {
	_, dr, err := s.overridePaths(ctx, name)
	if err != nil {
		return "", false
	}
	b, err := os.ReadFile(dr)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// hasOverride reports whether a published override of name exists.
func (s *service) hasOverride(ctx context.Context, name string) bool {
	pub, _, err := s.overridePaths(ctx, name)
	if err != nil {
		return false
	}
	_, err = os.Stat(pub)
	return err == nil
}

// Overrides lists the active theme's published and draft overrides.
func (s *service) Overrides(ctx context.Context) ([]Override, error) {
	od, err := overridesDir(s.d.Config, s.activeSlug(ctx))
	if err != nil {
		return nil, err
	}
	var out []Override
	for _, l := range []struct {
		dir   string
		draft bool
	}{{od, false}, {filepath.Join(od, draftsSubdir), true}} {
		for name, f := range collect([]layer{{dir: l.dir}}) {
			st, err := os.Stat(f.path)
			if err != nil {
				continue
			}
			out = append(out, Override{Name: name, Draft: l.draft, UpdatedAt: st.ModTime().UTC().Truncate(time.Second).Format(time.RFC3339)})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return !out[i].Draft
	})
	return out, nil
}

// PublishOverride promotes the draft of name to the published override.
func (s *service) PublishOverride(ctx context.Context, name string) error {
	pub, dr, err := s.overridePaths(ctx, name)
	if err != nil {
		return err
	}
	body, err := os.ReadFile(dr)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: no draft for %q", ErrNotFound, name)
	}
	if err != nil {
		return fmt.Errorf("theme: read draft: %w", err)
	}
	if err := s.checkTemplate(ctx, name, string(body)); err != nil {
		return err
	}
	if err := writeFile(pub, string(body)); err != nil {
		return err
	}
	if err := os.Remove(dr); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("theme: remove draft: %w", err)
	}
	s.invalidate()
	s.d.Audit.Record(ctx, "theme.override.publish", "template", name, map[string]any{"theme": s.activeSlug(ctx)})
	return nil
}

// DeleteOverride removes the published override and draft of name.
func (s *service) DeleteOverride(ctx context.Context, name string) error {
	pub, dr, err := s.overridePaths(ctx, name)
	if err != nil {
		return err
	}
	removed := false
	for _, p := range []string{pub, dr} {
		err := os.Remove(p)
		switch {
		case err == nil:
			removed = true
		case !errors.Is(err, fs.ErrNotExist):
			return fmt.Errorf("theme: delete override: %w", err)
		}
	}
	if !removed {
		return fmt.Errorf("%w: no override for %q", ErrNotFound, name)
	}
	s.invalidate()
	s.d.Audit.Record(ctx, "theme.override.delete", "template", name, map[string]any{"theme": s.activeSlug(ctx)})
	return nil
}
