package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/mailer"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

// gorun mail set stores Settings → Mail from the shell: the same validation
// as the admin, the secret from stdin only, stored encrypted, never printed.
func TestMailSetStoresSMTPWithAnEncryptedSecret(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GORUN_ENV", "dev")
	var out bytes.Buffer
	err := mailCmd([]string{"set", "--provider", "smtp", "--host", "smtp.mx.cloudflare.net", "--port", "465", "--tls", "tls",
		"--username", "api_token", "--from", "noreply@example.test", "--secret-stdin", "--data", dir}, strings.NewReader("very-secret-token\n"), &out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "very-secret-token") {
		t.Fatal("mail set printed the secret")
	}
	cfg, err := config.Load([]string{"--data", dir})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	app, err := kernel.BootWithOptions(ctx, cfg, []kernel.Module{settingsmod.New()}, kernel.BootOptions{StartJobs: false})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	st := app.Deps.Settings
	if stored := st.GetString(ctx, "mail.password", ""); stored == "" || strings.Contains(stored, "very-secret-token") {
		t.Fatalf("the password is not stored encrypted: %q", stored)
	}
	m := mailer.New(st, app.Deps.Log).WithKey(cfg.SecretKey)
	c, err := m.Config(ctx)
	if err != nil || c.Host != "smtp.mx.cloudflare.net" || c.Port != 465 || c.TLS != "tls" || c.Username != "api_token" || c.From != "noreply@example.test" || c.Password != "very-secret-token" {
		t.Fatalf("%+v %v", c, err)
	}
	if !m.Configured(ctx) {
		t.Fatal("the mailer does not count it as configured")
	}

}

// A set without --secret-stdin keeps the secret already stored.
func TestMailSetKeepsTheStoredSecret(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GORUN_ENV", "dev")
	base := []string{"set", "--provider", "smtp", "--host", "smtp.mx.cloudflare.net", "--port", "465", "--tls", "tls", "--username", "api_token", "--data", dir}
	if err := mailCmd(append(base, "--from", "a@example.test", "--secret-stdin"), strings.NewReader("very-secret-token\n"), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := mailCmd(append(base, "--from", "b@example.test"), strings.NewReader(""), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load([]string{"--data", dir})
	ctx := context.Background()
	app, err := kernel.BootWithOptions(ctx, cfg, []kernel.Module{settingsmod.New()}, kernel.BootOptions{StartJobs: false})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	c, err := mailer.New(app.Deps.Settings, app.Deps.Log).WithKey(cfg.SecretKey).Config(ctx)
	if err != nil || c.From != "b@example.test" || c.Password != "very-secret-token" {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestMailSetRefusesWhatTheAdminRefuses(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GORUN_ENV", "dev")
	for _, args := range [][]string{
		{"set", "--provider", "pigeon", "--data", dir},
		{"set", "--provider", "smtp", "--host", "smtp.example.test", "--port", "99999", "--data", dir},
		{"set", "--provider", "smtp", "--host", "smtp.example.test", "--from", "not an address", "--data", dir},
		{"set", "--provider", "resend", "--data", dir},
		{"set", "--provider", "smtp", "--host", "smtp.example.test", "--password", "x", "--data", dir},
	} {
		if err := mailCmd(args, strings.NewReader(""), &bytes.Buffer{}); err == nil {
			t.Errorf("%v was accepted", args)
		}
	}
}
