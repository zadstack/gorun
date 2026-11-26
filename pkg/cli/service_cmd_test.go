package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/mailer"
	"github.com/zadstack/gorun/internal/services"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

const (
	testServiceMailSecret = "smtp-secret-value-0001"
	testServiceStoreKeys  = "AKIDSERVICECLI0001:storage-secret-value-cli"
)

func serviceRun(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := serviceCmd(args, strings.NewReader(stdin), &out)
	return out.String(), err
}

func serviceGet(t *testing.T, dir, name string) services.Status {
	t.Helper()
	out, err := serviceRun(t, "", "get", name, "--data", dir)
	if err != nil {
		t.Fatal(err)
	}
	var st services.Status
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return st
}

// A set with only some fields keeps the others and the stored secret: a
// person fixing a sender address must not lose the password.
func TestServiceSetPartialKeepsSecret(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GORUN_ENV", "dev")
	out, err := serviceRun(t, testServiceMailSecret+"\n", "set", "mail", "provider=smtp", "host=smtp.mx.cloudflare.net", "port=465", "tls=tls",
		"username=api_token", "from=a@example.test", "--secret-stdin", "--data", dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "service mail: configured" {
		t.Fatalf("set printed %q", out)
	}
	if _, err := serviceRun(t, "", "set", "mail", "from=b@example.test", "--data", dir); err != nil {
		t.Fatal(err)
	}
	st := serviceGet(t, dir, "mail")
	if st.State != services.StateConfigured || !st.SecretSet || st.Fields["host"] != "smtp.mx.cloudflare.net" || st.Fields["from"] != "b@example.test" || st.Fields["username"] != "api_token" {
		t.Fatalf("status after partial set: %+v", st)
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
	c, err := mailer.New(app.Deps.Settings, app.Deps.Log).WithKey(cfg.SecretKey).Config(ctx)
	if err != nil || c.Password != testServiceMailSecret || c.Port != 465 || c.From != "b@example.test" {
		t.Fatalf("%+v %v", c, err)
	}
}

// list and get show secret_set, never a secret.
func TestServiceListNoSecretInOutput(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GORUN_ENV", "dev")
	if _, err := serviceRun(t, testServiceMailSecret+"\n", "set", "mail", "provider=smtp", "host=smtp.example.test", "--secret-stdin", "--data", dir); err != nil {
		t.Fatal(err)
	}
	if _, err := serviceRun(t, testServiceStoreKeys+"\n", "set", "storage", "provider=r2", "account=0123456789abcdef0123456789abcdef",
		"bucket=site-media", "purpose=media", "--secret-stdin", "--data", dir); err != nil {
		t.Fatal(err)
	}
	out, err := serviceRun(t, "", "list", "--data", dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{testServiceMailSecret, "AKIDSERVICECLI0001", "storage-secret-value-cli"} {
		if strings.Contains(out, secret) {
			t.Fatalf("list printed a secret: %s", out)
		}
	}
	var got struct {
		Services []services.Status `json:"services"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	var names []string
	for _, st := range got.Services {
		names = append(names, st.Name)
		if (st.Name == "mail" || st.Name == "storage") && !st.SecretSet {
			t.Errorf("%s: secret_set false: %+v", st.Name, st)
		}
	}
	if strings.Join(names, ",") != "mail,storage,chat,analytics,backups" {
		t.Fatalf("services %v", names)
	}
}

// A secret never comes from the command line: as a k=v or a flag it is
// refused with nothing stored; without --secret-stdin stdin is not read;
// an empty first line is refused.
func TestServiceSetSecretOnlyFromStdin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GORUN_ENV", "dev")
	for _, args := range [][]string{
		{"set", "mail", "provider=smtp", "host=smtp.example.test", "password=x", "--data", dir},
		{"set", "mail", "provider=resend", "from=a@example.test", "api_key=x", "--data", dir},
		{"set", "storage", "provider=r2", "bucket=b", "purpose=media", "keys=A:B", "--data", dir},
	} {
		_, err := serviceRun(t, "", args...)
		secretName := strings.SplitN(args[4], "=", 2)[0]
		if args[1] == "storage" {
			secretName = "keys"
		}
		if err == nil || !strings.Contains(err.Error(), "service: invalid") || !strings.Contains(err.Error(), secretName) || strings.Contains(err.Error(), "=x") {
			t.Errorf("%v: %v", args, err)
		}
	}
	if _, err := serviceRun(t, "", "set", "mail", "provider=smtp", "host=smtp.example.test", "--password", "x", "--data", dir); err == nil {
		t.Error("--password was accepted")
	}
	if st := serviceGet(t, dir, "mail"); st.State != services.StateOff {
		t.Fatalf("a refused set stored something: %+v", st)
	}
	if _, err := serviceRun(t, testServiceMailSecret+"\n", "set", "mail", "provider=smtp", "host=smtp.example.test", "--data", dir); err != nil {
		t.Fatal(err)
	}
	if st := serviceGet(t, dir, "mail"); st.SecretSet {
		t.Fatalf("stdin read without --secret-stdin: %+v", st)
	}
	if _, err := serviceRun(t, "\n", "set", "mail", "--secret-stdin", "--data", dir); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("empty secret line: %v", err)
	}
}

func TestServiceFieldErrorsNameFields(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GORUN_ENV", "dev")
	_, err := serviceRun(t, "", "set", "mail", "provider=smtp", "host=smtp.example.test", "colour=red", "--data", dir)
	if err == nil || err.Error() != "service: invalid colour" {
		t.Fatalf("%v", err)
	}
	_, err = serviceRun(t, "", "set", "mail", "provider=smtp", "host=smtp.example.test", "port=99999", "from=not an address", "--data", dir)
	if err == nil || err.Error() != "service: invalid from, port" {
		t.Fatalf("%v", err)
	}
	if _, err := serviceRun(t, "", "get", "pigeon", "--data", dir); err == nil {
		t.Fatal("unknown service accepted")
	}
	if out, err := serviceRun(t, "", "off", "analytics", "--data", dir); err != nil || strings.TrimSpace(out) != "service analytics: off" {
		t.Fatalf("off: %q %v", out, err)
	}
	if st := serviceGet(t, dir, "analytics"); st.State != services.StateOff {
		t.Fatalf("analytics after off: %+v", st)
	}
}
