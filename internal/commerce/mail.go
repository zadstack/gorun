package commerce

import (
	"bytes"
	htmlutil "html"
	"html/template"
	"net/url"
	"strings"

	"github.com/zadstack/gorun/internal/i18n"
)

// accessMail fills the customer emails (the mailer's own templates live in
// internal/mailer, which commerce does not own, so the bodies are built
// here from the commerce catalog).
type accessMail struct {
	Link, Product, Order, EndsAt string
}

var mailTmpl = template.Must(template.New("mail").Parse(`<!doctype html>
<html lang="{{.Locale}}" dir="{{.Dir}}"><body style="margin:0;background:#f3f5f2;font-family:system-ui,-apple-system,'Segoe UI',Tahoma,sans-serif;color:#141816">
<div style="max-width:560px;margin:0 auto;padding:24px">
<div style="background:#ffffff;border:1px solid #cbd3ce;border-radius:6px;padding:24px">
<h1 style="font-size:18px;margin:0 0 12px">{{.Title}}</h1>
{{range .Lines}}<p style="margin:0 0 12px;line-height:1.6">{{.}}</p>{{end}}
<p style="margin:20px 0"><a href="{{.Link}}" style="display:inline-block;background:#167252;color:#ffffff;text-decoration:none;padding:10px 18px;border-radius:6px;font-weight:600">{{.Button}}</a></p>
<p style="margin:0;color:#637069;font-size:13px;line-height:1.6">{{.Footer}}</p>
<p style="margin:8px 0 0;color:#637069;font-size:12px;word-break:break-all" dir="ltr">{{.Link}}</p>
</div></div></body></html>`))

type mailParts struct {
	Locale, Dir, Title, Button, Footer, Link string
	Lines                                    []string
}

func renderMail(b *i18n.Bundle, locale string, p mailParts) (html, text string) {
	p.Locale, p.Dir = locale, "ltr"
	if b != nil {
		p.Dir = b.Dir(locale)
	}
	var buf bytes.Buffer
	if err := mailTmpl.Execute(&buf, p); err != nil {
		return "", ""
	}
	var t strings.Builder
	t.WriteString(p.Title + "\n\n")
	for _, l := range p.Lines {
		t.WriteString(l + "\n\n")
	}
	t.WriteString(p.Button + ": " + p.Link + "\n\n" + p.Footer + "\n")
	return buf.String(), t.String()
}

func tr(b *i18n.Bundle, locale, key string, args ...any) string {
	if b == nil {
		return key
	}
	return b.T(locale, key, args...)
}

// accessEmail is sent once an entitlement is active: a magic link that
// signs the customer in to /account.
func accessEmail(b *i18n.Bundle, locale string, m accessMail) (subject, html, text string) {
	subject = tr(b, locale, "commerce.mail.access.subject", m.Product)
	html, text = renderMail(b, locale, mailParts{
		Title:  tr(b, locale, "commerce.mail.access.title"),
		Lines:  []string{tr(b, locale, "commerce.mail.access.body", m.Product, m.Order), tr(b, locale, "commerce.mail.access.ends", m.EndsAt)},
		Button: tr(b, locale, "commerce.mail.access.button"),
		Footer: tr(b, locale, "commerce.mail.footer"),
		Link:   m.Link,
	})
	return subject, html, text
}

// loginEmail answers a /account sign-in request.
func loginEmail(b *i18n.Bundle, locale, link string) (subject, html, text string) {
	subject = tr(b, locale, "commerce.mail.login.subject")
	html, text = renderMail(b, locale, mailParts{
		Title:  tr(b, locale, "commerce.mail.login.title"),
		Lines:  []string{tr(b, locale, "commerce.mail.login.body")},
		Button: tr(b, locale, "commerce.mail.login.button"),
		Footer: tr(b, locale, "commerce.mail.footer"),
		Link:   link,
	})
	return subject, html, text
}

// requestReceiptEmail confirms receipt only. It grants no login or entitlement.
func requestReceiptEmail(b *i18n.Bundle, locale, number, baseURL string) (subject, html, text string) {
	subject = tr(b, locale, "commerce.mail.request.subject", number)
	identity := ""
	if u, err := url.Parse(baseURL); err == nil && (u.Scheme == "https" || u.Scheme == "http") {
		identity = u.Hostname()
	}
	if identity != "" {
		subject = identity + " — " + subject
	}
	// Only the plaintext is kept: the HTML part is rebuilt from it below.
	_, text = renderMail(b, locale, mailParts{
		Title:  tr(b, locale, "commerce.mail.request.title"),
		Lines:  []string{tr(b, locale, "commerce.mail.request.number", number), tr(b, locale, "commerce.mail.request.review"), tr(b, locale, "commerce.mail.request.no_payment"), tr(b, locale, "commerce.mail.request.next")},
		Button: tr(b, locale, "commerce.mail.request.button"),
		Footer: tr(b, locale, "commerce.mail.request.footer"), Link: baseURL,
	})
	// Receipts are transactional notices: identify the site and keep the body
	// simple, without a promotional call-to-action. Retain the plaintext part.
	if identity != "" {
		text = identity + "\n\n" + text
	}
	var body strings.Builder
	dir := "ltr"
	if b != nil {
		dir = b.Dir(locale)
	}
	body.WriteString(`<html lang="` + htmlutil.EscapeString(locale) + `" dir="` + htmlutil.EscapeString(dir) + `"><body>`)
	for _, line := range strings.Split(strings.TrimSpace(text), "\n\n") {
		body.WriteString("<p>" + htmlutil.EscapeString(line) + "</p>")
	}
	body.WriteString("</body></html>")
	html = body.String()
	return
}
