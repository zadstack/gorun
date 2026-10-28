package builder

import (
	"strings"

	"github.com/zadstack/gorun/internal/mailer"
)

func (b *readinessBuilder) contactMail() {
	b.contactRecipients()
	b.mailConfiguration()
	// Keep the existing IDs for report consumers while making their labels provider-neutral.
	b.add(2, "smtp_delivery", "Mail provider acceptance", "قبول مزود البريد", "unknown", b.word("No message sent. Provider acceptance is unverified; use the mail test. Acceptance does not prove inbox receipt.", "لم تُرسل رسالة. قبول المزود غير متحقق منه؛ استخدم اختبار البريد. قبول المزود لا يثبت الوصول إلى صندوق الوارد."), "/admin/settings/mail")
	b.add(2, "mail_inbox", "Inbox receipt", "الوصول إلى صندوق الوارد", "unknown", b.word("Actual inbox receipt must be confirmed by the recipient after a controlled test.", "يجب أن يؤكد المستلم الوصول الفعلي إلى صندوق الوارد بعد اختبار مضبوط."), "/admin/settings/mail")
}

func (b *readinessBuilder) contactRecipients() {
	key := "forms.recipients"
	recipient, err := b.setting(key)
	if err == nil && strings.TrimSpace(recipient) == "" {
		key = "contact.email"
		recipient, err = b.setting(key)
	}
	valid := mailer.ValidPlainAddress(strings.TrimSpace(recipient))
	if key == "forms.recipients" {
		recipients, parseErr := mailer.ParseRecipients(recipient)
		valid = parseErr == nil && len(recipients) > 0
	}
	status := "pass"
	evidence := b.word("Explicit contact recipients are configured; addresses are not shown.", "مستلمون صريحون لرسائل التواصل مضبوطون؛ لا تُعرض العناوين.")
	if err != nil {
		status = "unknown"
		evidence = b.word("Recipient settings unavailable.", "تعذر قراءة إعداد مستلم البريد.")
	} else if !valid {
		status = "warn"
		evidence = b.word("Explicit recipients are missing or invalid; the runtime may fall back to an administrator. Review form recipients and contact email.", "المستلمون الصريحون غير مضبوطين أو غير صالحين؛ قد يعود التشغيل إلى بريد المدير. راجع مستلمي النماذج وبريد التواصل.")
	}
	fix := "/admin/settings/general"
	if key == "forms.recipients" {
		fix = "/admin/system/form-submissions"
	}
	b.add(2, "contact_email", "Contact recipients", "مستلمو التواصل", status, evidence, fix)
}

func (b *readinessBuilder) mailConfiguration() {
	provider, err := b.setting("mail.provider")
	keys := []string{"mail.host"}
	switch provider {
	case "", "smtp":
	case "resend":
		keys = []string{"mail.resend_api_key", "mail.from"}
	default:
		if err == nil {
			b.add(2, "smtp_config", "Mail configuration", "إعداد البريد", "warn", b.word("Selected mail provider is unsupported.", "مزود البريد المحدد غير مدعوم."), "/admin/settings/mail")
			return
		}
	}
	for _, key := range keys {
		if _, settingErr := b.setting(key); settingErr != nil {
			err = settingErr
		}
	}
	status := "warn"
	evidence := b.word("Selected mail provider settings are incomplete. Only local setting presence is checked.", "إعدادات مزود البريد المحدد غير مكتملة. يُفحص وجود الإعدادات المحلية فقط.")
	if err != nil {
		status = "unknown"
		evidence = b.word("Mail provider settings unavailable.", "تعذر قراءة إعدادات مزود البريد.")
	} else if mailer.New(b.d.Settings, b.d.Log).Configured(b.ctx) {
		status = "pass"
		evidence = b.word("Selected mail provider settings are present. Secrets are not decrypted or shown; credentials, provider acceptance and inbox receipt remain unverified.", "إعدادات مزود البريد المحدد موجودة. لا تُفك الأسرار ولا تُعرض؛ صلاحية بيانات الاعتماد وقبول المزود والوصول لصندوق الوارد غير متحقق منها.")
	}
	b.add(2, "smtp_config", "Mail configuration", "إعداد البريد", status, evidence, "/admin/settings/mail")
}
