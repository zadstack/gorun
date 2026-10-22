package admin

import (
	"context"
	"errors"
	"net/mail"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/observe"
)

// Mail section of the Integrations hub: the resolved SMTP/Resend values
// (never secrets) and an explicit test send that reports provider
// acceptance separately from inbox delivery, which nothing here verifies.

type mailStatus struct {
	Provider     string           `json:"provider" enum:"smtp,resend"`
	Source       string           `json:"source" doc:"Always settings: mail is configured in the database, never from ENV or config"`
	Configured   bool             `json:"configured"`
	Host         string           `json:"host"`
	Port         string           `json:"port"`
	TLS          string           `json:"tls"`
	From         string           `json:"from"`
	FromName     string           `json:"from_name"`
	ReplyTo      string           `json:"reply_to"`
	Username     string           `json:"username"`
	PasswordSet  bool             `json:"password_set"`
	APIKeySet    bool             `json:"api_key_set"`
	EditToken    string           `json:"-"`
	Operation    *CapabilityCheck `json:"operation" doc:"Newest saved test send, or null"`
	Reason       string           `json:"reason"`
	Remediation  string           `json:"remediation"`
	OperationKey string           `json:"-"`
}

func (h *handlers) mailStatus(ctx context.Context) (mailStatus, error) {
	snap, err := mailSnapshot(ctx, h.d.DB)
	if err != nil {
		return mailStatus{}, err
	}
	form := mailFromSnapshot(snap)
	cap := h.mailCapability(ctx)
	st := mailStatus{Provider: form.Provider, Source: "settings", Configured: h.mail.Configured(ctx), Host: form.Host, Port: form.Port, TLS: form.TLS, From: form.From, FromName: form.FromName,
		ReplyTo: form.ReplyTo, Username: form.Username, EditToken: form.EditToken, Operation: cap.Operation, Reason: cap.Reason, Remediation: cap.Remediation,
		PasswordSet: snap["mail.password"] != "" && snap["mail.password"] != `""`, APIKeySet: snap["mail.resend_api_key"] != "" && snap["mail.resend_api_key"] != `""`}
	return st, nil
}

// mailTestInput is the explicit test send. To defaults to the requesting
// operator's own address; any other address is a deliberate choice.
type mailTestInput struct {
	To string `form:"to" json:"to"`
}

// mailTestOutcome separates what was observed (the provider accepted the
// message) from what was not (delivery to an inbox).
type mailTestOutcome struct {
	Accepted  bool   `json:"accepted" doc:"The provider or SMTP server accepted the message"`
	Delivery  string `json:"delivery" enum:"not_verified" doc:"Inbox delivery is never verified here"`
	Recipient string `json:"recipient" doc:"Masked address the message was addressed to"`
	Own       bool   `json:"own_address" doc:"True when sent to the requesting operator's own address"`
	Provider  string `json:"provider"`
	Error     string `json:"error,omitempty" doc:"Sanitized reason when not accepted"`
	At        string `json:"at"`
}

var errMailRecipient = errors.New("integrations: invalid test recipient")

func maskEmail(addr string) string {
	at := strings.LastIndex(addr, "@")
	if at <= 0 {
		return "***"
	}
	local := addr[:at]
	if len(local) > 2 {
		local = local[:2] + "***"
	} else {
		local = local[:1] + "***"
	}
	return local + addr[at:]
}

// sendIntegrationsTestMail renders the standard test template and sends
// it once. Provider acceptance is recorded as operation evidence for the
// requesting operator; the recipient is audited masked.
func (h *handlers) sendIntegrationsTestMail(ctx context.Context, u *auth.User, in mailTestInput, locale, siteTitle string) (mailTestOutcome, error) {
	if u == nil {
		return mailTestOutcome{}, errMailRecipient
	}
	to := strings.TrimSpace(in.To)
	if to == "" {
		to = u.Email
	}
	if a, err := mail.ParseAddress(to); err != nil || a.Address != to || len(to) > 254 {
		return mailTestOutcome{}, errMailRecipient
	}
	own := strings.EqualFold(to, u.Email)
	if !h.mail.Configured(ctx) {
		return mailTestOutcome{}, observe.ErrDisabled
	}
	provider := h.d.Settings.GetString(ctx, "mail.provider", "smtp")
	if provider == "" {
		provider = "smtp"
	}
	name := u.Name
	if name == "" {
		name = u.Email
	}
	out := mailTestOutcome{Delivery: "not_verified", Recipient: maskEmail(to), Own: own, Provider: provider, At: time.Now().UTC().Format(time.RFC3339)}
	started := time.Now()
	subject, html, text, err := h.mail.Render(locale, "test", map[string]any{"Name": name, "SiteTitle": siteTitle})
	if err != nil {
		return out, errors.New("test message could not be rendered")
	}
	sendErr := h.mail.SendRequired(ctx, to, subject, html, text)
	out.Accepted = sendErr == nil
	var runErr error
	if sendErr != nil {
		out.Error = "message was not accepted by the mail provider"
		runErr = errors.New(out.Error)
		h.d.Log.Warn("integrations: test mail", "provider", provider, "err", sendErr)
	}
	h.d.Audit.Record(ctx, "mail.test_sent", "user", u.ID, map[string]any{"integrations": true, "recipient": out.Recipient, "own_address": own, "accepted": out.Accepted})
	key := mailOperationKey(u)
	if rev, rerr := h.mailRevision(ctx, key); rerr == nil && rev != "" {
		recipient := "requesting_operator"
		if !own {
			recipient = "chosen_address"
		}
		if rerr := h.observe.Record(ctx, kindOperationMail, key, rev, observe.Result{Scope: "operation:send", Data: map[string]any{"recipient": recipient, "delivery": "not_verified"}}, runErr, time.Since(started)); rerr != nil {
			h.d.Log.Warn("integrations: record mail observation", "err", rerr)
		}
	}
	return out, nil
}
