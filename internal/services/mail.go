package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/mail"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/mailer"
)

// ErrMailNotConfigured is a mail test on a site whose mail is not set up.
var ErrMailNotConfigured = errors.New("services: mail is not configured")

// ErrMailTestAddress is a mail test without a valid address to send to.
var ErrMailTestAddress = errors.New("services: mail test needs an email address")

// mailFields are the mail service's settings, each stored as mail.<name>.
var mailFields = []Field{
	{Name: "provider", Type: "enum", Required: true, Values: []string{"smtp", "resend"}},
	{Name: "host", Type: "string", Providers: []string{"smtp"}},
	{Name: "port", Type: "int", Providers: []string{"smtp"}},
	{Name: "tls", Type: "enum", Providers: []string{"smtp"}, Values: []string{mailer.TLSStartTLS, mailer.TLSImplicit, mailer.TLSNone}},
	{Name: "username", Type: "string", Providers: []string{"smtp"}},
	{Name: "from", Type: "string", Providers: []string{"resend"}, Required: true},
	{Name: "from_name", Type: "string"},
	{Name: "reply_to", Type: "string"},
}

// mailSecrets is each provider's secret: its name in the service and its setting.
var mailSecrets = map[string]struct{ name, key string }{
	"smtp":   {"password", "mail.password"},
	"resend": {"api_key", "mail.resend_api_key"},
}

type mailService struct{ d *kernel.Deps }

// Mail is Settings → Mail as a service. It validates with the admin's own
// admin.MailConfig, so it stores nothing the form would refuse. Each
// provider keeps its own secret: switching provider never reuses the other
// one's.
func Mail(d *kernel.Deps) Service { return mailService{d: d} }

func (mailService) Name() string        { return "mail" }
func (mailService) Providers() []string { return []string{"smtp", "resend"} }
func (mailService) Fields() []Field     { return append([]Field(nil), mailFields...) }

// Secret is the provider's secret name; "" means the stored provider.
func (m mailService) Secret(provider string) string {
	if provider == "" {
		provider = m.d.Settings.GetString(context.Background(), "mail.provider", "smtp")
	}
	return mailSecrets[provider].name
}

func (m mailService) Status(ctx context.Context) (Status, error) {
	all, err := m.d.Settings.All(ctx, "mail.")
	if err != nil {
		return Status{}, err
	}
	stored := decodeMail(all)
	st := Status{Name: "mail", Provider: stored.Provider, State: StateOff, Missing: []string{}, Fields: map[string]any{}, Source: "settings"}
	if len(all) == 0 {
		return st, nil
	}
	for _, f := range mailFields {
		if f.Name == "provider" {
			continue
		}
		if v := mailField(stored, f.Name); v != "" {
			if f.Name == "port" {
				n, _ := strconv.Atoi(v)
				st.Fields[f.Name] = n
				continue
			}
			st.Fields[f.Name] = v
		}
	}
	sec, ok := mailSecrets[stored.Provider]
	if !ok {
		st.State, st.Missing = StateIncomplete, []string{"provider"}
		return st, nil
	}
	st.SecretSet = m.d.Settings.GetString(ctx, sec.key, "") != ""
	if stored.Provider == "smtp" && stored.Host == "" {
		st.Missing = append(st.Missing, "host")
	}
	if stored.Provider == "resend" && stored.From == "" {
		st.Missing = append(st.Missing, "from")
	}
	if !st.SecretSet {
		st.Missing = append(st.Missing, sec.name)
	}
	st.State = StateIncomplete
	if len(st.Missing) == 0 && mailer.New(m.d.Settings, m.d.Log).Configured(ctx) {
		st.State = StateConfigured
	}
	return st, nil
}

func (m mailService) Apply(ctx context.Context, tx *sql.Tx, values map[string]string, secret *string) (map[string]string, error) {
	stored, err := mailStoredTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	in := stored
	fields := map[string]string{}
	for k, v := range values {
		switch k {
		case "provider":
			in.Provider = v
		case "host":
			in.Host = v
		case "port":
			in.Port = v
		case "tls":
			in.TLS = v
		case "username":
			in.Username = v
		case "from":
			in.From = v
		case "from_name":
			in.FromName = v
		case "reply_to":
			in.ReplyTo = v
		case "password", "api_key":
			fields[k] = ErrKeySecretValue // a secret never comes as a value
		default:
			fields[k] = "services.error.unknown_field"
		}
	}
	if len(fields) > 0 {
		return fields, nil
	}
	in.Provider = strings.TrimSpace(in.Provider)
	if in.Provider == "" {
		in.Provider = "smtp"
	}
	if secret != nil {
		s := strings.TrimSpace(*secret)
		if in.Provider == "resend" {
			in.ResendAPIKey, in.ClearResendAPIKey = s, s == ""
		} else {
			in.Password, in.ClearPassword = s, s == ""
		}
	}
	vals, errs := admin.MailConfig(in, m.d.Config.SecretKey)
	for k, v := range errs {
		if k == "resend_api_key" {
			k = "api_key"
		}
		fields[k] = v
	}
	if len(fields) > 0 {
		return fields, nil
	}
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := m.d.Settings.SetTx(ctx, tx, k, vals[k]); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

// Test sends the admin's test message to actor, an email address.
func (m mailService) Test(ctx context.Context, actor string) (TestResult, error) {
	a, err := mail.ParseAddress(actor)
	if err != nil || a.Address != actor {
		return TestResult{}, ErrMailTestAddress
	}
	ml := mailer.New(m.d.Settings, m.d.Log).WithKey(m.d.Config.SecretKey).WithEnv(m.d.Config.Env)
	if !ml.Configured(ctx) {
		return TestResult{}, ErrMailNotConfigured
	}
	title := m.d.Settings.GetString(ctx, "site.title", "GoRun")
	subject, html, text, err := ml.Render("en", "test", map[string]any{"Name": actor, "SiteTitle": title})
	if err != nil {
		return TestResult{}, err
	}
	res := TestResult{At: time.Now().UTC()}
	if err := ml.SendRequired(ctx, actor, subject, html, text); err != nil {
		res.Message = err.Error()
		return res, nil
	}
	res.OK, res.Message = true, "sent to "+actor
	m.d.Audit.Record(ctx, "mail.test_sent", "service", "mail", map[string]any{"service": "mail"})
	return res, nil
}

// Off removes every mail.* setting, secrets included.
func (m mailService) Off(ctx context.Context, tx *sql.Tx) error {
	return m.d.Settings.DeletePrefixTx(ctx, tx, "mail.")
}

// mailStoredTx reads the stored mail.* settings on tx as the admin form would show them.
func mailStoredTx(ctx context.Context, tx *sql.Tx) (admin.MailInput, error) {
	rows, err := tx.QueryContext(ctx, `SELECT key, value_json FROM settings WHERE substr(key, 1, 5) = 'mail.'`)
	if err != nil {
		return admin.MailInput{}, err
	}
	defer rows.Close()
	all := map[string]json.RawMessage{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return admin.MailInput{}, err
		}
		all[k] = json.RawMessage(v)
	}
	return decodeMail(all), rows.Err()
}

// decodeMail is the stored non-secret mail.* settings, with the form's defaults.
func decodeMail(all map[string]json.RawMessage) admin.MailInput {
	str := func(k, def string) string {
		var v string
		if json.Unmarshal(all[k], &v) != nil || strings.TrimSpace(v) == "" {
			return def
		}
		return strings.TrimSpace(v)
	}
	port := str("mail.port", "")
	var n int
	if port == "" && json.Unmarshal(all["mail.port"], &n) == nil && n > 0 {
		port = strconv.Itoa(n)
	}
	if port == "" {
		port = "587"
	}
	return admin.MailInput{Provider: str("mail.provider", "smtp"), Host: str("mail.host", ""), Port: port,
		TLS: str("mail.tls", mailer.TLSStartTLS), Username: str("mail.username", ""), From: str("mail.from", ""),
		FromName: str("mail.from_name", ""), ReplyTo: str("mail.reply_to", "")}
}

func mailField(in admin.MailInput, name string) string {
	switch name {
	case "host":
		return in.Host
	case "port":
		return in.Port
	case "tls":
		return in.TLS
	case "username":
		return in.Username
	case "from":
		return in.From
	case "from_name":
		return in.FromName
	case "reply_to":
		return in.ReplyTo
	}
	return ""
}
