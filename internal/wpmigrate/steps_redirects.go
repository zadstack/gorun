package wpmigrate

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/zadstack/gorun/internal/redirects"
)

type redirectPayload struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// generateRedirects adds one redirect item per imported content item whose
// permalink changed (WordPress /2026/08/31/slug/ → /post/slug, suffixed
// duplicate slugs, …). The front page and unchanged paths get none.
func (s *service) generateRedirects(ctx context.Context, m *migrationRow) error {
	list, err := s.items(ctx, m.ID, "content", ItemDone, ItemPreview)
	if err != nil {
		return err
	}
	hosts := s.siteHosts(ctx, m)
	n := 0
	for _, it := range list {
		if it.WPURL == "" || it.LocalURL == "" || !hosts[hostOf(it.WPURL)] {
			continue
		}
		from := pathOf(it.WPURL)
		if from == "/" || from == "" || from == it.LocalURL {
			continue
		}
		if other := s.pathOwner(ctx, from); other != "" && other != it.LocalID {
			// A local item (not this one) already lives at the old path: a
			// redirect would hide it.
			s.logEntry(ctx, m.ID, "warn", it.ID, "redirect_skipped_path_in_use", map[string]any{"from": from, "to": it.LocalURL})
			continue
		}
		b, _ := json.Marshal(redirectPayload{From: from, To: it.LocalURL})
		row := itemRow{Kind: "redirect", WPType: it.WPType, WPID: it.WPID, WPSlug: it.WPSlug, WPURL: it.WPURL,
			Checksum: shortHash(from + "→" + it.LocalURL), Payload: b}
		if _, err := s.upsertItem(ctx, m.ID, row, m.DryRun); err != nil {
			return err
		}
		n++
	}
	s.logEntry(ctx, m.ID, "info", "", "step_items", map[string]any{"step": "redirects", "items": n})
	return nil
}

// importRedirect saves a 301 (enabled) from the old WordPress path.
func (s *service) importRedirect(ctx context.Context, m *migrationRow, it *itemRow) error {
	var p redirectPayload
	if err := json.Unmarshal(it.Payload, &p); err != nil {
		return err
	}
	if redirects.Reserved(redirects.NormalizePath(p.From)) {
		// e.g. WooCommerce's /checkout/ or /account/: GoRun serves these.
		it.Status, it.Error = ItemSkipped, "reserved_path"
		it.Would = map[string]any{"action": "skip", "from": p.From, "reason": "reserved_path"}
		return nil
	}
	it.Would = map[string]any{"action": "create", "from": p.From, "to": p.To}
	if m.DryRun {
		return nil
	}
	r := redirects.Redirect{FromPath: p.From, ToURL: p.To, Status: 301, Enabled: true}
	if existing := s.findRedirect(ctx, p.From); existing != nil {
		r.ID = existing.ID
		it.Would["action"] = "update"
	}
	saved, err := s.redirects.Save(ctx, r)
	if err != nil {
		var ve *redirects.ValidationError
		if errors.As(err, &ve) {
			return errors.New("redirect: " + joinFields(ve.Fields))
		}
		return err
	}
	it.LocalKind, it.LocalID, it.LocalURL = "redirect", saved.ID, p.To
	return nil
}

// findRedirect looks up an existing redirect by its (normalized) path.
func (s *service) findRedirect(ctx context.Context, from string) *redirects.Redirect {
	key := redirects.NormalizePath(from)
	list, _, err := s.redirects.List(ctx, redirects.ListQuery{Search: key, PerPage: 100})
	if err != nil {
		return nil
	}
	for i := range list {
		if list[i].FromPath == key {
			return &list[i]
		}
	}
	return nil
}

func joinFields(f map[string]string) string {
	var parts []string
	for _, k := range sortedKeys(f) {
		parts = append(parts, k+"="+f[k])
	}
	return strings.Join(parts, ", ")
}

// siteHosts are the hosts that count as "the WordPress site" for link
// rewriting (source URL, site URL and home, with and without www).
func (s *service) siteHosts(ctx context.Context, m *migrationRow) map[string]bool {
	hosts := map[string]bool{}
	if src, err := s.getSource(ctx, m.SourceID); err == nil && src.URL != "" {
		hosts[hostOf(src.URL)] = true
	}
	var site SiteInfo
	if raw, ok := m.Stats["site"]; ok {
		_ = json.Unmarshal(raw, &site)
	}
	for _, u := range []string{site.URL, site.Home} {
		if h := hostOf(u); h != "" {
			hosts[h] = true
		}
	}
	return hosts
}

// pathOwner returns the id of the content item served at path ("" when
// none): /{type}/{slug} for non-page types, otherwise the page whose
// permalink equals path.
func (s *service) pathOwner(ctx context.Context, path string) string {
	segs := strings.FieldsFunc(path, func(r rune) bool { return r == '/' })
	if len(segs) == 0 {
		return ""
	}
	locale := s.locale(ctx)
	if len(segs) == 2 && segs[0] != "page" {
		if it, err := s.content.GetBySlug(ctx, segs[0], segs[1], locale); err == nil {
			return it.ID
		}
	}
	if it, err := s.content.GetBySlug(ctx, "page", segs[len(segs)-1], locale); err == nil && s.linker.Item(ctx, it) == path {
		return it.ID
	}
	return ""
}
