package site

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/mail"
	"os"
	"strings"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/mailer"
)

// SettingContactEmail is the recipient of contact form messages; when it is
// unset or invalid the first active admin's email is used.
const SettingContactEmail = "contact.email"

// formSender is what delivery needs from the mailer service (*mailer.Mailer
// implements it; tests register a fake under mailer.ServiceName).
type formSender interface {
	SendMessage(ctx context.Context, msg mailer.Message) error
	Configured(ctx context.Context) bool
}

var _ formSender = (*mailer.Mailer)(nil)

// deliverForm is the HookFormSubmitted action: unless forms.notify is off
// it mails the submission to every recipient (Reply-To = the visitor, the
// attachment attached when small enough, else linked) and records the
// outcome on the row and in the audit log (site.form.delivered or
// site.form.failed). It never returns an error, so a failure does not make
// the event job re-run every other subscriber.
func (h *handlers) deliverForm(ctx context.Context, payload any) error {
	sub, err := decodeSubmission(payload)
	if err != nil {
		h.d.Log.Error("site: form delivery: bad payload", "err", err)
		return nil
	}
	result, claimErr := h.d.DB.ExecContext(ctx, `INSERT INTO form_event_deliveries (submission_id) VALUES (?) ON CONFLICT(submission_id) DO NOTHING`, sub.ID)
	if claimErr != nil {
		h.d.Log.Error("site: claim form notification", "err", claimErr)
		return nil
	}
	count, claimErr := result.RowsAffected()
	if claimErr != nil || count != 1 {
		return nil
	}
	_ = h.deliver(ctx, sub)
	h.deliverCustomerReceipt(ctx, sub)
	return nil
}

// deliver mails sub and returns the stored delivery state ("sent",
// "failed" or "off"); the admin's resend action calls it directly.
func (h *handlers) deliver(ctx context.Context, sub FormSubmission) string {
	fail := func(reason string, err error) string {
		meta := map[string]any{"page": sub.Page, "reason": reason}
		detail := reason
		if err != nil {
			meta["error"] = err.Error()
			detail = reason + ": " + err.Error()
			h.d.Log.Error("site: form delivery failed", "form", sub.Form, "reason", reason, "err", err)
		}
		h.setDelivery(ctx, sub.ID, deliveryFailed, detail)
		h.d.Audit.Record(ctx, "site.form.failed", "form", sub.Form, meta)
		return deliveryFailed
	}
	cfg := h.formConfig(ctx)
	if !cfg.Notify {
		h.setDelivery(ctx, sub.ID, deliveryOff, "")
		return deliveryOff
	}
	sender, ok := kernel.Get[formSender](h.d, mailer.ServiceName)
	if !ok {
		return fail("no_mailer", nil)
	}
	to := cfg.Recipients
	if len(to) == 0 {
		if r := h.contactRecipient(ctx); r != "" {
			to = []string{r}
		}
	}
	if len(to) == 0 {
		return fail("no_recipient", nil)
	}
	configured := sender.Configured(ctx)
	msg := h.formMessage(ctx, sub) // built once: the attachment is read from disk once
	var errs []error
	for _, addr := range to {
		msg.To = addr
		if err := sender.SendMessage(ctx, msg); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", addr, err))
		}
	}
	if len(errs) > 0 {
		return fail("send", errors.Join(errs...))
	}
	if !configured {
		// The mailer only logged it (the body too in dev): not delivered.
		h.d.Log.Warn("site: contact message not delivered: mail is not configured", "form", sub.Form)
		return fail("mail_not_configured", nil)
	}
	h.setDelivery(ctx, sub.ID, deliverySent, "")
	h.d.Audit.Record(ctx, "site.form.delivered", "form", sub.Form, map[string]any{"page": sub.Page, "to": strings.Join(to, ", ")})
	return deliverySent
}

func decodeSubmission(payload any) (FormSubmission, error) {
	switch v := payload.(type) {
	case FormSubmission:
		return v, nil
	case *FormSubmission:
		if v != nil {
			return *v, nil
		}
	case json.RawMessage:
		var s FormSubmission
		err := json.Unmarshal(v, &s)
		return s, err
	case []byte:
		var s FormSubmission
		err := json.Unmarshal(v, &s)
		return s, err
	}
	return FormSubmission{}, fmt.Errorf("unexpected payload %T", payload)
}

// contactRecipient returns setting contact.email when it is a valid
// address, else the email of the oldest active admin, else "".
func (h *handlers) contactRecipient(ctx context.Context) string {
	if v := strings.TrimSpace(h.d.Settings.GetString(ctx, SettingContactEmail, "")); v != "" {
		if a, err := mail.ParseAddress(v); err == nil {
			return a.Address
		}
		h.d.Log.Warn("site: invalid contact.email setting; falling back to the first admin", "value", v)
	}
	users, ok := kernel.Get[auth.Service](h.d, kernel.ServiceAuth)
	if !ok {
		return ""
	}
	for page := 1; page <= 50; page++ {
		list, total, err := users.List(ctx, auth.ListQuery{Page: page, PerPage: 100, Sort: "created_at", Order: "asc"})
		if err != nil {
			h.d.Log.Error("site: list users for contact recipient", "err", err)
			return ""
		}
		for _, u := range list {
			if u.Role == "admin" && u.Status == auth.StatusActive && u.Email != "" {
				return u.Email
			}
		}
		if page*100 >= total || len(list) == 0 {
			break
		}
	}
	return ""
}

// formMessage builds the owner's email in the site locale; the caller sets To.
func (h *handlers) formMessage(ctx context.Context, sub FormSubmission) mailer.Message {
	loc := h.siteLocaleOf(ctx)
	title := strings.TrimSpace(h.d.Settings.GetString(ctx, "site.title", ""))
	if title == "" {
		title = "GoRun"
	}
	t := func(key string, args ...any) string { return h.d.I18n.T(loc, key, args...) }
	rows := [][2]string{
		{t("site.form.mail_name"), sub.Name},
		{t("site.form.mail_email"), sub.Email},
		{t("site.form.mail_page"), sub.Page},
		{t("site.form.mail_time"), sub.SubmittedAt},
	}
	link := kernel.BaseURL(ctx, h.d) + "/admin" + submissionsPath + "/" + sub.ID
	if sub.AttachmentSize > 0 {
		rows = append(rows, [2]string{t("site.form.mail_attachment"), fmt.Sprintf("%s (%s)", sub.AttachmentName, humanSize(sub.AttachmentSize))})
	}
	var text, body strings.Builder
	notice := t("site.form.mail_review_notice")
	fmt.Fprintf(&text, "%s\n\n", notice)
	fmt.Fprintf(&body, "<p>%s</p>", html.EscapeString(notice))
	body.WriteString(`<table cellpadding="4">`)
	for _, r := range rows {
		fmt.Fprintf(&text, "%s: %s\n", r[0], r[1])
		fmt.Fprintf(&body, "<tr><th style=\"text-align:start\">%s</th><td>%s</td></tr>", html.EscapeString(r[0]), html.EscapeString(r[1]))
	}
	body.WriteString("</table>")
	text.WriteString("\n" + sub.Message + "\n")
	fmt.Fprintf(&body, `<p style="white-space:pre-wrap">%s</p>`, html.EscapeString(sub.Message))
	fmt.Fprintf(&text, "\n%s: %s\n", t("site.form.mail_open"), link)
	fmt.Fprintf(&body, `<p><a href="%s">%s</a></p>`, html.EscapeString(link), html.EscapeString(t("site.form.mail_open")))
	reply := (&mail.Address{Name: sub.Name, Address: sub.Email}).String()
	msg := mailer.Message{ReplyTo: reply, Subject: t("site.form.mail_subject", title, sub.Name),
		HTML: `<div dir="` + h.d.I18n.Dir(loc) + `">` + body.String() + "</div>", Text: text.String()}
	if sub.AttachmentSize > 0 && sub.AttachmentSize <= mailAttachmentLimit {
		if data, err := os.ReadFile(h.uploadPath(sub.ID)); err == nil {
			msg.Attachments = []mailer.Attachment{{Name: sub.AttachmentName, ContentType: sub.AttachmentType, Data: data}}
		} else {
			h.d.Log.Error("site: read form attachment for mail", "id", sub.ID, "err", err)
		}
	}
	return msg
}

// Customer receipts are opt-in. The durable claim prevents queue replay from
// sending another confirmation. Ambiguous SMTP failures stay visible for review
// instead of automatically retrying and risking duplicate delivery.
func (h *handlers) deliverCustomerReceipt(ctx context.Context, sub FormSubmission) {
	var enabled bool
	if h.d.Settings.Get(ctx, "forms.customer_receipt", &enabled) != nil || !enabled {
		return
	}
	sender, ok := kernel.Get[formSender](h.d, mailer.ServiceName)
	if !ok || !sender.Configured(ctx) {
		return
	}
	result, err := h.d.DB.ExecContext(ctx, `INSERT INTO form_receipt_deliveries (submission_id, status, created_at) VALUES (?, 'sending', ?) ON CONFLICT(submission_id) DO NOTHING`, sub.ID, db.Now())
	if err != nil {
		h.d.Log.Error("site: claim customer receipt", "err", err)
		return
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return
	}
	title := h.d.Settings.GetString(ctx, "site.title", "GoRun")
	subject := title + " — Request received " + sub.ID
	text := "We received your message. Reference: " + sub.ID + ".\nIt is pending review. This is not a payment confirmation, service approval, or automatic activation or delivery. We will contact you after review."
	dir := "ltr"
	if h.siteLocaleOf(ctx) == "ar" {
		subject = title + " — استلمنا طلبك " + sub.ID
		text = "استلمنا رسالتك. رقم المرجع: " + sub.ID + ".\nطلبك قيد المراجعة. هذه ليست رسالة تأكيد دفع أو قبول الخدمة أو تفعيل أو تسليم تلقائي. سنتواصل معك بعد مراجعة الطلب."
		dir = "rtl"
	}
	msg := mailer.Message{To: sub.Email, Subject: subject, Text: text, HTML: `<div dir="` + dir + `"><p>` + strings.ReplaceAll(html.EscapeString(text), "\n", "</p><p>") + `</p></div>`}
	err = sender.SendMessage(ctx, msg)
	status, detail, sent := "sent", "", db.Now()
	if err != nil {
		status, detail, sent = "failed", err.Error(), ""
	}
	if _, saveErr := h.d.DB.ExecContext(ctx, "UPDATE form_receipt_deliveries SET status = ?, last_error = ?, sent_at = ? WHERE submission_id = ?", status, detail, sent, sub.ID); saveErr != nil {
		h.d.Log.Error("site: record customer receipt", "err", saveErr)
	}
	h.d.Audit.Record(ctx, "site.form.customer_receipt."+status, "form_submission", sub.ID, nil)
}
