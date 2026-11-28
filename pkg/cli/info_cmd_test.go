package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestInfoCmd(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GORUN_ENV", "dev")
	data := []string{"--data", dir}
	// The password comes from stdin, never the command line.
	r, w, _ := os.Pipe()
	old := os.Stdin
	os.Stdin = r
	w.WriteString("a-long-test-password\n")
	w.Close()
	err := userCreate(append([]string{"--email", "owner@example.test", "--name", "Owner", "--admin", "--password-stdin"}, data...))
	os.Stdin = old
	if err != nil {
		t.Fatal(err)
	}
	if err := settingsCmd([]string{"set", "site.title", "زاد ستاك", "--data", dir}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := infoCmd(data, &out); err != nil {
		t.Fatal(err)
	}
	var empty siteInfo
	json.Unmarshal(out.Bytes(), &empty)
	if empty.MessagesNew != 0 || empty.CommentsPending != 0 {
		t.Fatalf("counts on an empty site: %+v", empty)
	}
	d, err := openDB(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO form_submissions (id, form, name, email, message, created_at, status, delivery) VALUES ('a', 'contact', 'n', 'e@x.test', 'm', '2026-10-11T00:00:00Z', 'new', 'failed')`,
		`INSERT INTO form_submissions (id, form, name, email, message, created_at, status, delivery) VALUES ('b', 'contact', 'n', 'e@x.test', 'm', '2026-10-11T00:00:00Z', 'read', 'sent')`,
		`INSERT INTO form_submissions (id, form, name, email, message, created_at, status, delivery) VALUES ('c', 'contact', 'n', 'e@x.test', 'm', '2026-10-11T00:00:00Z', 'spam', 'failed')`,
	} {
		if _, err := d.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	d.Close()
	out.Reset()
	if err := infoCmd(data, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "password") || strings.Contains(out.String(), "a-long-test-password") {
		t.Fatal("info leaks a password")
	}
	var info siteInfo
	if err := json.Unmarshal(out.Bytes(), &info); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	if info.MessagesNew != 1 || info.MessagesFailed != 1 || info.MessagesTotal != 2 {
		t.Fatalf("inbox counts: %+v", info)
	}
	if info.Title != "زاد ستاك" || len(info.Users) != 1 || info.Users[0].Email != "owner@example.test" || info.Users[0].Role != "admin" || info.DBBytes == 0 {
		t.Fatalf("info %+v", info)
	}
}

// gorun info reports migrations the database holds but this binary does not
// know (a newer binary migrated it), and applied files whose content changed.
func TestInfoCmdReportsSchemaAhead(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GORUN_ENV", "dev")
	data := []string{"--data", dir}
	var out bytes.Buffer
	if err := infoCmd(data, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"unknown_migrations": []`) || !strings.Contains(out.String(), `"changed_migrations": []`) {
		t.Fatalf("fresh database must report empty lists:\n%s", out.String())
	}
	d, err := openDB(data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO schema_migrations(module, name, applied_at) VALUES ('future', '0001_x.sql', '2030-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	d.Close()
	out.Reset()
	if err := infoCmd(data, &out); err != nil {
		t.Fatalf("schema ahead must not fail info: %v", err)
	}
	var info siteInfo
	if err := json.Unmarshal(out.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if len(info.UnknownMigrations) != 1 || info.UnknownMigrations[0] != "future/0001_x.sql" {
		t.Fatalf("unknown_migrations = %v", info.UnknownMigrations)
	}
}

// gorun info says how the site sends mail, with no secret in its output.
func TestInfoCmdReportsMail(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GORUN_ENV", "dev")
	data := []string{"--data", dir}
	read := func() infoMail {
		t.Helper()
		var out bytes.Buffer
		if err := infoCmd(data, &out); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "very-secret-value") {
			t.Fatal("info leaks a mail secret")
		}
		var info siteInfo
		if err := json.Unmarshal(out.Bytes(), &info); err != nil {
			t.Fatal(err)
		}
		return info.Mail
	}
	// Secrets are stored encrypted and cannot be set from the CLI; their stored
	// form is all info looks at, so a placeholder stands in for one.
	set := func(k, v string) {
		t.Helper()
		d, err := openDB(data)
		if err != nil {
			t.Fatal(err)
		}
		defer d.Close()
		raw, _ := json.Marshal(v)
		if _, err := d.Exec(`INSERT INTO settings (key, value_json, updated_at) VALUES (?, ?, '2026-11-26T00:00:00Z') ON CONFLICT(key) DO UPDATE SET value_json = excluded.value_json`, k, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	if m := read(); m.Provider != "smtp" || m.Configured || m.SecretSet {
		t.Fatalf("a site with no mail settings: %+v", m)
	}
	for k, v := range map[string]string{"mail.host": "smtp.example.test", "mail.from": "noreply@example.test", "mail.password": "very-secret-value"} {
		set(k, v)
	}
	if m := read(); m.Host != "smtp.example.test" || m.From != "noreply@example.test" || !m.Configured || !m.SecretSet {
		t.Fatalf("SMTP settings: %+v", m)
	}
	set("mail.provider", "resend")
	if m := read(); m.Provider != "resend" || m.Host != "" || m.Configured || m.SecretSet {
		t.Fatalf("Resend without a key: %+v", m)
	}
	set("mail.resend_api_key", "very-secret-value")
	if m := read(); !m.Configured || !m.SecretSet {
		t.Fatalf("Resend with a key and a sender: %+v", m)
	}
}

// gorun info lists the six services from the same registry as gorun
// service list, without any secret, and keeps its mail block.
func TestInfoReportsServices(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GORUN_ENV", "dev")
	data := []string{"--data", dir}
	in := strings.NewReader("very-secret-value\n")
	if err := serviceCmd([]string{"set", "mail", "provider=smtp", "host=smtp.example.com", "from=site@example.com", "--secret-stdin", "--data", dir}, in, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := infoCmd(data, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "very-secret-value") {
		t.Fatal("info leaks a service secret")
	}
	var info struct {
		Mail     map[string]any   `json:"mail"`
		Services []map[string]any `json:"services"`
	}
	if err := json.Unmarshal(out.Bytes(), &info); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	names := []string{}
	for _, s := range info.Services {
		names = append(names, s["name"].(string))
	}
	if strings.Join(names, ",") != "mail,storage,chat,analytics,backups,metricool" {
		t.Fatalf("services = %v", names)
	}
	if m := info.Services[0]; m["state"] != "configured" || m["secret_set"] != true {
		t.Fatalf("mail service = %v", m)
	}
	if info.Mail["provider"] != "smtp" || info.Mail["host"] != "smtp.example.com" || info.Mail["secret_set"] != true {
		t.Fatalf("mail block = %v", info.Mail)
	}
}
