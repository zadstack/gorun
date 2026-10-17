package cli

import (
	"bytes"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/zadstack/gorun/internal/db"
)

// Addendum B: `gorun settings` recovers from a bad setting without the
// admin: validated like the admin screens, audited as system.
func TestSettingsCLIRecovery(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GORUN_ENV", "dev")
	data := []string{"--data", dir}
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := settingsCmd(append(args, data...), &out)
		return strings.TrimSpace(out.String()), err
	}
	must := func(args ...string) string {
		t.Helper()
		out, err := run(args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return out
	}
	// The lockout scenario: an https base URL saved by mistake, then cleared.
	must("set", "site.base_url", "https://Example.com/")
	if got := must("get", "site.base_url"); got != `"https://Example.com"` {
		t.Fatalf("normalised base url: %s", got)
	}
	must("unset", "site.base_url")
	if _, err := run("get", "site.base_url"); err == nil {
		t.Fatal("get of an unset key must fail")
	}
	must("set", "site.title", "  ZAD  ")
	if got := must("get", "site.title"); got != `"ZAD"` {
		t.Fatalf("title: %s", got)
	}
	must("set", "site.posts_per_page", "25")
	if got := must("get", "site.posts_per_page"); got != "25" {
		t.Fatalf("posts_per_page must be a number: %s", got)
	}
	must("set", "forms.recipients", "a@example.com; b@example.com")
	if got := must("get", "forms.recipients"); got != `"a@example.com, b@example.com"` {
		t.Fatalf("recipients: %s", got)
	}
	must("set", "contact.attachments", "true")
	must("set", "contact.attachment_max_mb", "10")
	if got := must("get", "contact.attachment_max_mb"); got != "10" {
		t.Fatalf("attachment size must be a number: %s", got)
	}
	must("set", "theme.business.hero_title", `"مرحبا"`)
	must("set", "social.links", `{"x":"https://x.com/zad"}`)
	if got := must("get", "social.links"); got != `{"x":"https://x.com/zad"}` {
		t.Fatalf("json value: %s", got)
	}
	for _, bad := range [][]string{
		{"set", "site.base_url", "ftp://nope"}, {"set", "site.posts_per_page", "0"}, {"set", "contact.email", "x@"},
		{"set", "site.theme", "../evil"}, {"set", "forms.recipients", "nope"}, {"set", "contact.attachment_max_mb", "99"}, {"set", "forms.notify", "maybe"}, {"set", "mail.password", "hunter2"}, {"set", "Bad Key", "x"}, {"frob", "site.title"},
	} {
		if _, err := run(bad...); err == nil {
			t.Errorf("%v must be refused", bad)
		}
	}
	d, err := sql.Open("sqlite", filepath.Join(dir, "gorun.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec(`INSERT INTO settings (key, value_json, updated_at) VALUES ('mail.password', '"enc:abc"', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if got := must("get", "mail.password"); got != `"(set, hidden)"` {
		t.Fatalf("secret printed: %s", got)
	}
	var n int
	if err := d.QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'settings.update' AND actor_kind = 'system'
		AND json_extract(meta_json, '$.via') = 'cli'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 9 { // 8 sets + 1 unset
		t.Fatalf("system audit rows = %d", n)
	}
}
