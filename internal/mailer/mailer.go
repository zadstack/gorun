// Package mailer sends transactional email over SMTP or Resend using mail.*
// settings. When mail.host is unset it logs the message instead: in
// development (WithEnv("dev")) including the text body, so magic links
// still work; otherwise only the recipient and subject, with a warning.
package mailer

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/settings"
)

// ServiceName is the kernel service name the admin module registers the
// *Mailer under (kernel.Get[*mailer.Mailer](d, mailer.ServiceName)).
const ServiceName = "mailer"

// TLS modes for mail.tls.
const (
	TLSStartTLS = "starttls"
	TLSImplicit = "tls"
	TLSNone     = "none"
)

const (
	dialTimeout = 10 * time.Second
	ioTimeout   = 30 * time.Second
	defaultPort = 587
)

// Config is the selected provider configuration; only its secret is decrypted.
type Config struct {
	Host, Username, Password, From, FromName, TLS string
	Port                                          int
	Provider, ResendAPIKey                        string
}

// Mailer sends email. It is safe for concurrent use.
type Mailer struct {
	resendTransport http.RoundTripper
	settings        *settings.Store
	log             *slog.Logger
	key             []byte
	env             string
	transport       func(ctx context.Context, c Config, from, to string, msg []byte) error
}

// New returns a mailer reading SMTP settings from s. Call WithKey to enable
// decrypting mail.password.
func New(s *settings.Store, log *slog.Logger) *Mailer {
	return &Mailer{settings: s, log: log, transport: smtpSend}
}

// WithKey returns a copy of m that decrypts mail.password with key
// (cfg.SecretKey).
func (m *Mailer) WithKey(key []byte) *Mailer {
	c := *m
	c.key = append([]byte(nil), key...)
	return &c
}

// WithEnv returns a copy of m for the runtime environment env (cfg.Env).
// Only "dev" logs message bodies when SMTP is not configured.
func (m *Mailer) WithEnv(env string) *Mailer {
	c := *m
	c.env = env
	return &c
}

// Configured reports local settings presence, not provider reachability or delivery.
func (m *Mailer) Configured(ctx context.Context) bool {
	if m.settings == nil {
		return false
	}
	if m.settings.GetString(ctx, "mail.provider", "smtp") == "resend" {
		return m.settings.GetString(ctx, "mail.resend_api_key", "") != "" && strings.TrimSpace(m.settings.GetString(ctx, "mail.from", "")) != ""
	}
	return (m.settings.GetString(ctx, "mail.provider", "smtp") == "smtp" || m.settings.GetString(ctx, "mail.provider", "") == "") && strings.TrimSpace(m.settings.GetString(ctx, "mail.host", "")) != ""
}

// Key returns the secret key used for mail.password (for callers that
// store an encrypted password).
func (m *Mailer) Key() []byte { return append([]byte(nil), m.key...) }

// Config reads mail.* settings and decrypts only the selected provider secret.
func (m *Mailer) Config(ctx context.Context) (Config, error) {
	if m.settings == nil {
		return Config{}, errors.New("mailer: no settings store")
	}
	var readErr error
	str := func(k string) string {
		var value string
		if err := m.settings.Get(ctx, k, &value); err != nil && !errors.Is(err, settings.ErrNotFound) {
			readErr = errors.New("mailer: cannot read mail configuration")
		}
		return strings.TrimSpace(value)
	}
	c := Config{Host: str("mail.host"), Username: str("mail.username"), From: str("mail.from"),
		FromName: str("mail.from_name"), TLS: str("mail.tls"), Port: m.port(ctx)}
	if c.TLS == "" {
		c.TLS = TLSStartTLS
	}
	c.Provider = str("mail.provider")
	if readErr != nil {
		return c, readErr
	}
	if c.Provider == "" {
		c.Provider = "smtp"
	}
	if c.Provider != "smtp" && c.Provider != "resend" {
		return c, errors.New("mailer: unsupported provider")
	}
	secretName := "mail.password"
	if c.Provider == "resend" {
		secretName = "mail.resend_api_key"
	}
	if enc := str(secretName); enc != "" {
		pw, err := DecryptSecret(m.key, enc)
		if err != nil {
			return c, err
		}
		if c.Provider == "resend" {
			c.ResendAPIKey = pw
		} else {
			c.Password = pw
		}
	}
	if readErr != nil {
		return c, readErr
	}
	return c, nil
}

// port accepts a JSON number or numeric string; default 587.
func (m *Mailer) port(ctx context.Context) int {
	var raw json.RawMessage
	if err := m.settings.Get(ctx, "mail.port", &raw); err != nil {
		return defaultPort
	}
	var n int
	if json.Unmarshal(raw, &n) == nil && n > 0 {
		return n
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n > 0 {
			return n
		}
	}
	return defaultPort
}

// Message is one email for SendMessage. ReplyTo is optional (an address,
// optionally with a display name).
type Message struct {
	To, ReplyTo, Subject, HTML, Text string
	Attachments                      []Attachment // optional; makes the message multipart/mixed
}

// Attachment is a file sent with a Message. ContentType defaults to
// application/octet-stream; Name is encoded per RFC 2231 when needed.
type Attachment struct {
	Name, ContentType string
	Data              []byte
}

// Send delivers one message to a single recipient. Without mail.host it
// returns nil after logging the message: in dev the text body too (e.g. a
// magic link) at INFO; in any other env only recipient and subject, at
// WARN, because a body can carry a sign-in link.
func (m *Mailer) Send(ctx context.Context, to, subject, html, text string) error {
	return m.SendMessage(ctx, Message{To: to, Subject: subject, HTML: html, Text: text})
}

// SendRequired uses one configuration snapshot and never logs a message as a delivery fallback.
func (m *Mailer) SendRequired(ctx context.Context, to, subject, html, text string) error {
	return m.sendMessage(ctx, Message{To: to, Subject: subject, HTML: html, Text: text}, true)
}

// SendMessage is Send with the optional headers of msg (Reply-To).
func (m *Mailer) SendMessage(ctx context.Context, msg Message) error {
	return m.sendMessage(ctx, msg, false)
}

func (m *Mailer) sendMessage(ctx context.Context, msg Message, required bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	subject := oneLine(msg.Subject)
	if m.settings == nil {
		return errors.New("mailer: no settings store")
	}
	c, err := m.Config(ctx)
	if err != nil {
		return err
	}
	if c.Provider == "resend" {
		return m.sendResend(ctx, c, msg)
	}
	if c.Host == "" {
		if required {
			return errors.New("mailer: configured delivery provider required")
		}
		if m.env == "dev" {
			m.log.Info("mail not configured; link/body logged", "to", msg.To, "subject", subject, "body", msg.Text)
		} else {
			m.log.Warn("mail not configured; message not sent (set the mail settings)", "to", msg.To, "subject", subject)
		}
		return nil
	}
	rcpt, err := mail.ParseAddress(oneLine(msg.To))
	if err != nil {
		return fmt.Errorf("mailer: invalid recipient: %w", err)
	}
	var replyTo *mail.Address
	if strings.TrimSpace(msg.ReplyTo) != "" {
		if replyTo, err = mail.ParseAddress(oneLine(msg.ReplyTo)); err != nil {
			return fmt.Errorf("mailer: invalid reply-to: %w", err)
		}
	}
	fromAddr := c.From
	if fromAddr == "" {
		fromAddr = c.Username
	}
	from, err := mail.ParseAddress(oneLine(fromAddr))
	if err != nil {
		return fmt.Errorf("mailer: invalid mail.from: %w", err)
	}
	from.Name = oneLine(c.FromName)
	raw, err := buildMessage(from, rcpt, replyTo, subject, msg.HTML, msg.Text, c.Host, msg.Attachments)
	if err != nil {
		return err
	}
	if err := m.transport(ctx, c, from.Address, rcpt.Address, raw); err != nil {
		return safeDeliveryError(err)
	}
	return nil
}

func safeDeliveryError(err error) error {
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("mailer: send: %w", context.Canceled)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("mailer: send: %w", context.DeadlineExceeded)
	}
	var response *textproto.Error
	if errors.As(err, &response) {
		return fmt.Errorf("mailer: send: SMTP response %d", response.Code)
	}
	var network net.Error
	if errors.As(err, &network) && network.Timeout() {
		return errors.New("mailer: send: transport timeout")
	}
	// Provider response text may repeat credentials, recipients or message data.
	return errors.New("mailer: send: transport failed")
}

func oneLine(s string) string {
	return strings.TrimSpace(strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ").Replace(s))
}

func buildMessage(from, to, replyTo *mail.Address, subject, html, text, host string, atts []Attachment) ([]byte, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	hdr := func(k, v string) { fmt.Fprintf(&buf, "%s: %s\r\n", k, v) }
	hdr("From", from.String())
	hdr("To", to.String())
	if replyTo != nil {
		hdr("Reply-To", replyTo.String())
	}
	hdr("Subject", mime.QEncoding.Encode("utf-8", subject))
	hdr("Date", time.Now().UTC().Format(time.RFC1123Z))
	hdr("Message-ID", "<"+db.NewID()+"@"+host+">")
	hdr("MIME-Version", "1.0")
	if len(atts) == 0 {
		hdr("Content-Type", `multipart/alternative; boundary="`+mw.Boundary()+`"`)
		buf.WriteString("\r\n")
		if err := writeAlternatives(mw, text, html); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}
	// With attachments: multipart/mixed whose first part is the alternatives.
	var alt bytes.Buffer
	inner := multipart.NewWriter(&alt)
	if err := writeAlternatives(inner, text, html); err != nil {
		return nil, err
	}
	hdr("Content-Type", `multipart/mixed; boundary="`+mw.Boundary()+`"`)
	buf.WriteString("\r\n")
	w, err := mw.CreatePart(textproto.MIMEHeader{"Content-Type": {`multipart/alternative; boundary="` + inner.Boundary() + `"`}})
	if err == nil {
		_, err = w.Write(alt.Bytes())
	}
	if err != nil {
		return nil, fmt.Errorf("mailer: build: %w", err)
	}
	for _, a := range atts {
		if err := writeAttachment(mw, a); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("mailer: build: %w", err)
	}
	return buf.Bytes(), nil
}

// writeAlternatives writes the text and HTML parts and closes mw.
func writeAlternatives(mw *multipart.Writer, text, html string) error {
	for _, part := range []struct{ ctype, body string }{{"text/plain", text}, {"text/html", html}} {
		w, err := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {part.ctype + "; charset=utf-8"},
			"Content-Transfer-Encoding": {"quoted-printable"},
		})
		if err != nil {
			return fmt.Errorf("mailer: build: %w", err)
		}
		qp := quotedprintable.NewWriter(w)
		if _, err := qp.Write([]byte(part.body)); err != nil {
			return fmt.Errorf("mailer: build: %w", err)
		}
		if err := qp.Close(); err != nil {
			return fmt.Errorf("mailer: build: %w", err)
		}
	}
	return mw.Close()
}

var crlf = []byte("\r\n")

// writeAttachment writes a as a base64 part with 76-character lines.
func writeAttachment(mw *multipart.Writer, a Attachment) error {
	ctype := "application/octet-stream"
	if mt, _, err := mime.ParseMediaType(a.ContentType); err == nil && !strings.ContainsAny(mt, "\r\n") {
		ctype = mt
	}
	name := oneLine(a.Name)
	if name == "" {
		name = "attachment"
	}
	w, err := mw.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {mime.FormatMediaType(ctype, map[string]string{"name": name})},
		"Content-Disposition":       {mime.FormatMediaType("attachment", map[string]string{"filename": name})},
		"Content-Transfer-Encoding": {"base64"},
	})
	if err != nil {
		return fmt.Errorf("mailer: build: %w", err)
	}
	enc := make([]byte, base64.StdEncoding.EncodedLen(len(a.Data)))
	base64.StdEncoding.Encode(enc, a.Data)
	for len(enc) > 0 {
		n := min(len(enc), 76)
		if _, err := w.Write(enc[:n]); err != nil {
			return fmt.Errorf("mailer: build: %w", err)
		}
		if _, err := w.Write(crlf); err != nil {
			return fmt.Errorf("mailer: build: %w", err)
		}
		enc = enc[n:]
	}
	return nil
}

// smtpSend delivers msg with net/smtp, honouring c.TLS, with dial and I/O
// timeouts (net/smtp has none of its own).
func smtpSend(ctx context.Context, c Config, from, to string, msg []byte) (resultErr error) {
	addr := net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	dialer := &net.Dialer{Timeout: dialTimeout}
	tlsCfg := &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12}
	var conn net.Conn
	var err error
	if c.TLS == TLSImplicit {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: tlsCfg}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	defer conn.Close()
	defer func() {
		if resultErr != nil {
			if cause := ctx.Err(); cause != nil {
				resultErr = cause
			} else if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
				resultErr = context.DeadlineExceeded
			}
		}
	}()
	deadline := time.Now().Add(ioTimeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	cl, err := smtp.NewClient(conn, c.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer cl.Close()
	if c.TLS == TLSStartTLS {
		if ok, _ := cl.Extension("STARTTLS"); !ok {
			return errors.New("server does not support STARTTLS")
		}
		if err := cl.StartTLS(tlsCfg); err != nil {
			return err
		}
	}
	if c.Username != "" {
		// PlainAuth refuses to send credentials over an unencrypted
		// connection to a non-localhost server.
		if err := cl.Auth(smtp.PlainAuth("", c.Username, c.Password, c.Host)); err != nil {
			return err
		}
	}
	if err := cl.Mail(from); err != nil {
		return err
	}
	if err := cl.Rcpt(to); err != nil {
		return err
	}
	w, err := cl.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return cl.Quit()
}
