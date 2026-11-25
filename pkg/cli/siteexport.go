package cli

// Site export (gorun export): the site as a folder the owner can keep in
// Git: the site's facts, the active theme's identity, every content item,
// the builder layouts, the media records and files, and the public pages
// as rendered (docs/BACKUPS.md#site-export). It is a readable record of
// the site, not a backup format: backup keeps the database and media for
// restore; this keeps what a person or a diff can read. It lives in the
// binary layer because it reads every module.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/builder"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/seo"
	"github.com/zadstack/gorun/internal/theme"
)

// ExportOptions select what to write. Dest is created when missing; files are
// overwritten, and files from an earlier export that no longer apply are
// left where they are (a Git diff shows them).
type ExportOptions struct {
	Dest  string
	Pages bool // render every public page into pages/
	Media bool // copy local media files into media/files/
}

// ExportReport counts what was written.
type ExportReport struct {
	Dest    string   `json:"dest"`
	Items   int      `json:"items"`
	Layouts int      `json:"layouts"`
	Media   int      `json:"media"`
	Files   int      `json:"media_files"`
	Pages   int      `json:"pages"`
	Skipped []string `json:"skipped,omitempty"`
}

// siteFacts is site.json.
type siteFacts struct {
	ExportedAt string `json:"exported_at"`
	Version    string `json:"gorun_version,omitempty"`
	BaseURL    string `json:"base_url"`
	Title      string `json:"title"`
	Tagline    string `json:"tagline"`
	Theme      string `json:"theme"`
	Locale     string `json:"locale"`
	Layout     string `json:"layout"`
}

// identityReader is the part of the theme service the export needs.
type identityReader interface {
	LiveIdentity(ctx context.Context, slug string) theme.IdentitySnapshot
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// exportSite writes the export. app is the booted application (its handler
// renders the pages); version labels site.json.
func exportSite(ctx context.Context, app *kernel.App, version string, opts ExportOptions) (ExportReport, error) {
	if opts.Dest == "" {
		return ExportReport{}, errors.New("export: a destination directory is required")
	}
	if err := os.MkdirAll(opts.Dest, 0o755); err != nil {
		return ExportReport{}, err
	}
	d := app.Deps
	rep := ExportReport{Dest: opts.Dest}
	base := kernel.BaseURL(ctx, d)

	// site.json and the identity.
	facts := siteFacts{ExportedAt: time.Now().UTC().Format(time.RFC3339), Version: version, BaseURL: base, Layout: "gorun-export/1"}
	if d.Settings != nil {
		facts.Title = d.Settings.GetString(ctx, "site.title", "")
		facts.Tagline = d.Settings.GetString(ctx, "site.tagline", "")
		facts.Locale = i18n.ContentLocale(ctx, d.Settings)
	}
	if ts, ok := kernel.Get[theme.Service](d, kernel.ServiceTheme); ok {
		info := ts.Active(ctx)
		facts.Theme = info.Slug
		if ir, ok := ts.(identityReader); ok && info.Slug != "" {
			if err := writeJSON(filepath.Join(opts.Dest, "identity", safe(info.Slug)+".json"), ir.LiveIdentity(ctx, info.Slug)); err != nil {
				return rep, err
			}
		}
	}
	if err := writeJSON(filepath.Join(opts.Dest, "site.json"), facts); err != nil {
		return rep, err
	}

	// Content: one JSON record per item next to its body as HTML (or the
	// body's own format), under content/<type>/.
	exp, ok := kernel.Get[content.Exporter](d, kernel.ServiceContent)
	if !ok {
		return rep, errors.New("export: content service missing")
	}
	var buf bytes.Buffer
	if _, err := exp.Export(ctx, &buf, content.ExportOptions{Format: "json", Limit: 0, BaseURL: base}); err != nil {
		return rep, fmt.Errorf("export: content: %w", err)
	}
	var envelope struct {
		Items []content.ExportItem `json:"items"`
	}
	if err := json.Unmarshal(buf.Bytes(), &envelope); err != nil {
		return rep, fmt.Errorf("export: decode content: %w", err)
	}
	items := envelope.Items
	slugByID := map[string]string{}
	for _, it := range items {
		name := safe(it.Slug)
		if name == "" {
			name = safe(it.ID)
		}
		// Items of the site's own locale keep the bare slug; translations
		// carry theirs.
		if it.Locale != "" && it.Locale != facts.Locale {
			name += "." + safe(it.Locale)
		}
		slugByID[it.ID] = safe(it.Type) + "/" + name
		dir := filepath.Join(opts.Dest, "content", safe(it.Type))
		body := it.Body
		it.Body = ""
		if err := writeJSON(filepath.Join(dir, name+".json"), it); err != nil {
			return rep, err
		}
		ext := ".html"
		if it.BodyFormat == "markdown" {
			ext = ".md"
		}
		if err := writeFile(filepath.Join(dir, name+ext), []byte(body)); err != nil {
			return rep, err
		}
		rep.Items++
	}

	// Builder layouts, named after their page.
	layouts, err := builder.ExportLayouts(ctx, d.DB)
	if err != nil {
		return rep, err
	}
	for _, l := range layouts {
		name := slugByID[l.ContentID]
		if name == "" {
			name = "unknown/" + safe(l.ContentID)
		}
		name = strings.TrimPrefix(name, "page/")
		if l.Published != nil {
			if err := writeJSON(filepath.Join(opts.Dest, "layouts", name+".json"), l.Published); err != nil {
				return rep, err
			}
		}
		if l.Draft != nil {
			if err := writeJSON(filepath.Join(opts.Dest, "layouts", name+".draft.json"), l.Draft); err != nil {
				return rep, err
			}
		}
		rep.Layouts++
	}

	// Media records, and the local files when asked.
	if ms, ok := kernel.Get[media.Service](d, kernel.ServiceMedia); ok {
		var all []media.Media
		for page := 1; ; page++ {
			list, total, err := ms.List(ctx, media.ListQuery{Page: page, PerPage: 200, Sort: "created_at", Order: "asc"})
			if err != nil {
				return rep, fmt.Errorf("export: media: %w", err)
			}
			all = append(all, list...)
			if len(list) == 0 || len(all) >= total {
				break
			}
		}
		if err := writeJSON(filepath.Join(opts.Dest, "media", "media.json"), all); err != nil {
			return rep, err
		}
		rep.Media = len(all)
		if opts.Media {
			for _, m := range all {
				src := filepath.Join(d.Config.DataDir, "media", filepath.FromSlash(m.StorageKey))
				if !strings.HasPrefix(src, filepath.Join(d.Config.DataDir, "media")+string(filepath.Separator)) {
					rep.Skipped = append(rep.Skipped, "media "+m.ID+": unsafe key")
					continue
				}
				if err := copyFile(src, filepath.Join(opts.Dest, "media", "files", filepath.FromSlash(m.StorageKey))); err != nil {
					if errors.Is(err, os.ErrNotExist) {
						rep.Skipped = append(rep.Skipped, "media "+m.ID+": not a local file ("+m.URL+")")
						continue
					}
					return rep, err
				}
				rep.Files++
			}
		}
	}

	// The public pages as the site renders them now, from the sitemap.
	if opts.Pages {
		ss, ok := kernel.Get[seo.Service](d, kernel.ServiceSEO)
		if !ok {
			return rep, errors.New("export: seo service missing")
		}
		entries, err := ss.Entries(ctx)
		if err != nil {
			return rep, fmt.Errorf("export: sitemap: %w", err)
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Loc < entries[j].Loc })
		for _, e := range entries {
			u, err := url.Parse(e.Loc)
			if err != nil {
				rep.Skipped = append(rep.Skipped, "page "+e.Loc+": bad URL")
				continue
			}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, u.Path, nil).WithContext(ctx)
			req.Host = u.Host
			app.Handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				rep.Skipped = append(rep.Skipped, fmt.Sprintf("page %s: HTTP %d", u.Path, rec.Code))
				continue
			}
			target := filepath.Join(opts.Dest, "pages", filepath.FromSlash(pagePath(u.Path)))
			if err := writeFile(target, rec.Body.Bytes()); err != nil {
				return rep, err
			}
			rep.Pages++
		}
	}
	if err := writeJSON(filepath.Join(opts.Dest, "export.json"), rep); err != nil {
		return rep, err
	}
	return rep, nil
}

// pagePath maps a URL path to pages/<path>/index.html.
func pagePath(p string) string {
	p = path.Clean("/" + p)
	parts := strings.Split(strings.Trim(p, "/"), "/")
	for i, part := range parts {
		parts[i] = safe(part)
	}
	if p == "/" {
		return "index.html"
	}
	return path.Join(append(parts, "index.html")...)
}

// safe keeps a name to letters, digits, dot, underscore and hyphen, with
// no leading dots, so a slug can never leave its folder.
func safe(name string) string {
	name = unsafeName.ReplaceAllString(name, "-")
	name = strings.TrimLeft(name, ".")
	if len(name) > 120 {
		name = name[:120]
	}
	return name
}

func writeJSON(p string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(p, append(raw, '\n'))
}

func writeFile(p string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
