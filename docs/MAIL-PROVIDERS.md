# SMTP and Resend

Open **Settings → Mail** (`/admin/settings/mail`, requires `settings.manage`).
SMTP remains the default when `mail.provider` is absent. Existing SMTP configuration is retained when switching providers.

## Resend

1. [Verify the sender domain in Resend](https://resend.com/domains).
2. [Create a sending API key](https://resend.com/api-keys), preferably restricted to that domain.
3. Choose Resend, enter the key and a sender address on the verified domain, and save.
4. Optionally press **Send test email** to send to the currently signed-in user's address.

The application does not create a Resend account or verify DNS. Saving never sends mail or tests the provider. “Configured” means local fields are present; even API acceptance does not prove inbox delivery. API keys and SMTP passwords are encrypted using the application key; forms and APIs return only presence flags. Blank secret inputs preserve saved values; explicit clear removes them. Keep the application encryption key stable.

The adapter posts to the fixed `https://api.resend.com/emails` endpoint with a 30-second timeout, rejects redirects, bounds replies to 64 KiB and JSON requests to 32 MiB including attachments. It does not retry POSTs. A timeout can mean the message was accepted; existing application job retry policies remain in effect, so check Resend's activity before manually retrying an uncertain send. Attachments use base64 data, never remote URLs. No provider webhooks or delivery tracking are included.

Native mail form edits use a version token; stale saves return 409. The settings API accepts `mail.provider` (`smtp` or `resend`) and write-only `mail.resend_api_key`; `null` clears the key, `""` preserves it. Responses expose `mail.resend_api_key_set`, never the key. API merge writes also check their settings snapshots atomically.

إعداد Resend: وثّق نطاق المرسل لدى Resend، وأنشئ مفتاح إرسال، ثم اختر المزود وأدخل المفتاح وعنوان المرسل واحفظ. الحفظ لا يرسل بريدًا. زر الاختبار إجراء إرسال صريح إلى بريد المستخدم الحالي. وجود الإعدادات أو قبول الطلب لا يعني وصول الرسالة. إعدادات SMTP تبقى محفوظة عند التبديل.

Official API contract: [Send email](https://resend.com/docs/api-reference/emails/send-email), [attachments](https://resend.com/docs/dashboard/emails/attachments), [idempotency](https://resend.com/docs/dashboard/emails/idempotency-keys). Verified through official documentation; acceptance tests use a fake transport, not live provider credentials.
