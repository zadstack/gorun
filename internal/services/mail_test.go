package services

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/mailer"
)

func strp(s string) *string { return &s }

// setMail sets the mail service through the registry and fails on any error.
func setMail(t *testing.T, r *Registry, values map[string]string, secret *string) Status {
	t.Helper()
	st, fields, err := r.Set(context.Background(), "mail", values, secret, "cli")
	if err != nil || len(fields) > 0 {
		t.Fatalf("set mail %v: fields %v, err %v", values, fields, err)
	}
	return st
}

func smtpValues() map[string]string {
	return map[string]string{"provider": "smtp", "host": "smtp.example.com", "port": "2525", "tls": "tls", "username": "mailer", "from": "site@example.com", "from_name": "Site", "reply_to": "help@example.com"}
}

func TestMailPartialSetKeepsOtherFieldsAndSecret(t *testing.T) {
	d := testDeps(t)
	ctx := context.Background()
	r := NewRegistry(d, Mail(d))
	setMail(t, r, smtpValues(), strp("s3cret-pass"))

	st := setMail(t, r, map[string]string{"from": "news@example.com"}, nil)
	if st.State != StateConfigured || !st.SecretSet || st.Provider != "smtp" || st.Source != "settings" {
		t.Fatalf("status after partial set = %+v", st)
	}
	c, err := mailer.New(d.Settings, d.Log).WithKey(d.Config.SecretKey).Config(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := mailer.Config{Provider: "smtp", Host: "smtp.example.com", Port: 2525, TLS: "tls", Username: "mailer", Password: "s3cret-pass", From: "news@example.com", FromName: "Site", ReplyTo: "help@example.com"}
	if c != want {
		t.Fatalf("stored config = %+v, want %+v", c, want)
	}
}

func TestMailSwitchToResendWithoutKeyIsIncomplete(t *testing.T) {
	d := testDeps(t)
	ctx := context.Background()
	r := NewRegistry(d, Mail(d))
	setMail(t, r, smtpValues(), strp("s3cret-pass"))

	st := setMail(t, r, map[string]string{"provider": "resend"}, nil)
	if st.State != StateIncomplete || st.Provider != "resend" || st.SecretSet || !slices.Contains(st.Missing, "api_key") {
		t.Fatalf("status after switch = %+v", st)
	}
	if v := d.Settings.GetString(ctx, "mail.resend_api_key", ""); v != "" {
		t.Fatalf("the SMTP password was copied as the Resend key: %q", v)
	}
	if d.Settings.GetString(ctx, "mail.password", "") == "" {
		t.Fatal("switching provider dropped the SMTP password")
	}

	st = setMail(t, r, nil, strp("re_key_123"))
	if st.State != StateConfigured || !st.SecretSet || len(st.Missing) != 0 {
		t.Fatalf("status after the Resend key = %+v", st)
	}
}

func TestMailStatusHasNoSecret(t *testing.T) {
	d := testDeps(t)
	ctx := context.Background()
	r := NewRegistry(d, Mail(d))
	setMail(t, r, smtpValues(), strp("s3cret-pass"))
	enc := d.Settings.GetString(ctx, "mail.password", "")
	if enc == "" {
		t.Fatal("password not stored")
	}
	st, err := Mail(d).Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "s3cret-pass") || strings.Contains(string(b), enc) {
		t.Fatalf("status carries the secret: %s", b)
	}
	if !st.SecretSet {
		t.Fatalf("secret_set = false: %s", b)
	}
}

func TestMailRefusesWhatTheAdminRefuses(t *testing.T) {
	d := testDeps(t)
	ctx := context.Background()
	r := NewRegistry(d, Mail(d))
	vals := smtpValues()
	vals["port"] = "99999"
	_, fields, err := r.Set(ctx, "mail", vals, strp("s3cret-pass"), "cli")
	if err != nil {
		t.Fatal(err)
	}
	if fields["port"] == "" {
		t.Fatalf("fields = %v, want a port error", fields)
	}
	if v := d.Settings.GetString(ctx, "mail.host", ""); v != "" {
		t.Fatalf("a refused set stored mail.host = %q", v)
	}
	st, err := Mail(d).Status(ctx)
	if err != nil || st.State != StateOff {
		t.Fatalf("status after a refused set = %+v, %v", st, err)
	}
}
