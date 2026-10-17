package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
)

// siteInfo is what `gorun info` prints (JSON): enough for a manager to
// show the site's identity, its staff and how much content it holds.
type siteInfo struct {
	Version  string                    `json:"version"`
	Title    string                    `json:"title"`
	Tagline  string                    `json:"tagline"`
	Theme    string                    `json:"theme"`
	BaseURL  string                    `json:"base_url"`
	Locale   string                    `json:"locale"`
	Users    []infoUser                `json:"users"`
	Content  map[string]map[string]int `json:"content"` // type → status → count (trash left out)
	Media    int                       `json:"media"`
	DBBytes  int64                     `json:"db_bytes"`
	AIWriter string                    `json:"ai_writer"` // provider, "" when off
	// Inbox: form messages not opened yet, mail delivery failures, all
	// messages (spam left out); comments waiting for approval.
	MessagesNew     int `json:"messages_new"`
	MessagesFailed  int `json:"messages_failed"`
	MessagesTotal   int `json:"messages_total"`
	CommentsPending int `json:"comments_pending"`
	// Settings a manager may change (non-secret).
	Timezone         string `json:"timezone"`
	ContactEmail     string `json:"contact_email"`
	PostsPerPage     int    `json:"posts_per_page"`
	FormsNotify      bool   `json:"forms_notify"`
	FormsRecipients  string `json:"forms_recipients"`
	Attachments      bool   `json:"attachments"`
	AttachmentMaxMB  int    `json:"attachment_max_mb"`
	Maintenance      bool   `json:"maintenance"`
	HiddenFromSearch bool   `json:"hidden_from_search"` // robots.txt disallows everything
	// Schema guard: applied migrations this binary does not know (the
	// database was migrated by a newer binary) and applied files whose
	// content changed since they ran ("module/name"). Empty when healthy.
	UnknownMigrations []string `json:"unknown_migrations"`
	ChangedMigrations []string `json:"changed_migrations"`
}

type infoUser struct {
	Email     string `json:"email"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	Status    string `json:"status"`
	LastLogin string `json:"last_login"`
}

// infoCmd implements `gorun info [--data dir]`: read-only facts about the
// site as JSON (no secrets: no password hashes, keys or tokens).
func infoCmd(args []string, out io.Writer) error {
	cfg, err := config.Load(args)
	if err != nil {
		return err
	}
	d, err := openDB(args)
	if err != nil {
		return err
	}
	defer d.Close()
	ctx := context.Background()
	setting := func(key, def string) string {
		var raw string
		if err := d.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key = ?`, key).Scan(&raw); err != nil {
			return def
		}
		var v string
		if json.Unmarshal([]byte(raw), &v) != nil || v == "" {
			return def
		}
		return v
	}
	info := siteInfo{
		Version: version, Title: setting("site.title", ""), Tagline: setting("site.tagline", ""),
		Theme: setting("site.theme", cfg.Theme), BaseURL: setting("site.base_url", cfg.BaseURL), Locale: setting("site.locale", ""),
		AIWriter: setting("ai.provider", ""), Users: []infoUser{}, Content: map[string]map[string]int{},
	}
	raw := func(key string) (json.RawMessage, bool) {
		var v string
		if err := d.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key = ?`, key).Scan(&v); err != nil {
			return nil, false
		}
		return json.RawMessage(v), true
	}
	boolOf := func(key string, def bool) bool {
		v, ok := raw(key)
		if !ok {
			return def
		}
		var b bool
		if json.Unmarshal(v, &b) == nil {
			return b
		}
		var str string
		if json.Unmarshal(v, &str) == nil {
			return str == "true" || str == "1" || str == "on"
		}
		return def
	}
	intOf := func(key string, def int) int {
		v, ok := raw(key)
		if !ok {
			return def
		}
		var n int
		if json.Unmarshal(v, &n) == nil {
			return n
		}
		return def
	}
	info.Timezone = setting("site.timezone", "UTC")
	info.ContactEmail = setting("contact.email", "")
	info.PostsPerPage = intOf("site.posts_per_page", 10)
	info.FormsNotify = boolOf("forms.notify", true)
	info.FormsRecipients = setting("forms.recipients", "")
	info.Attachments = boolOf("contact.attachments", false)
	info.AttachmentMaxMB = intOf("contact.attachment_max_mb", 5)
	info.Maintenance = boolOf("site.maintenance", false)
	info.HiddenFromSearch = strings.Contains(setting("seo.robots_txt", ""), "Disallow: /\n") || strings.HasSuffix(strings.TrimSpace(setting("seo.robots_txt", "")), "Disallow: /")
	rows, err := d.QueryContext(ctx, `SELECT email, name, role, status, last_login_at FROM users ORDER BY created_at`)
	if err != nil {
		return fmt.Errorf("info: users: %w", err)
	}
	for rows.Next() {
		var u infoUser
		if err := rows.Scan(&u.Email, &u.Name, &u.Role, &u.Status, &u.LastLogin); err != nil {
			rows.Close()
			return err
		}
		info.Users = append(info.Users, u)
	}
	rows.Close()
	rows, err = d.QueryContext(ctx, `SELECT type, status, COUNT(*) FROM content WHERE status != 'trash' GROUP BY type, status`)
	if err != nil {
		return fmt.Errorf("info: content: %w", err)
	}
	for rows.Next() {
		var typ, status string
		var n int
		if err := rows.Scan(&typ, &status, &n); err != nil {
			rows.Close()
			return err
		}
		if info.Content[typ] == nil {
			info.Content[typ] = map[string]int{}
		}
		info.Content[typ][status] = n
	}
	rows.Close()
	if err := d.QueryRowContext(ctx, `SELECT COUNT(*) FROM media`).Scan(&info.Media); err != nil && err != sql.ErrNoRows {
		info.Media = 0
	}
	// Older databases may lack these tables: a missing count stays 0.
	for q, dst := range map[string]*int{
		`SELECT COUNT(*) FROM form_submissions WHERE status = 'new'`:                           &info.MessagesNew,
		`SELECT COUNT(*) FROM form_submissions WHERE delivery = 'failed' AND status != 'spam'`: &info.MessagesFailed,
		`SELECT COUNT(*) FROM form_submissions WHERE status != 'spam'`:                         &info.MessagesTotal,
		`SELECT COUNT(*) FROM comments WHERE status = 'pending'`:                               &info.CommentsPending,
	} {
		if err := d.QueryRowContext(ctx, q).Scan(dst); err != nil {
			*dst = 0
		}
	}
	// Like the counts above, a failed inspection degrades to empty lists.
	info.UnknownMigrations, info.ChangedMigrations = []string{}, []string{}
	if schema, err := kernel.InspectSchema(ctx, d, Modules(cfg)); err == nil {
		info.UnknownMigrations, info.ChangedMigrations = schema.Unknown, schema.Mismatched
	} else {
		fmt.Fprintln(os.Stderr, "info: schema inspection:", err)
	}
	if st, err := os.Stat(cfg.DBPath()); err == nil {
		info.DBBytes = st.Size()
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(info)
}
