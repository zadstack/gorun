# Site forms

Every form on a GoRun site can store its submissions. Each form gets a table of its own in the admin, whose
columns are that form's fields. After a submission is stored, the only automatic action is the
`site.form.submitted` webhook. There are no new notification emails.

Open **Inbox → Forms** (`/admin/forms`). It needs the same permission as the contact inbox (`settings.manage`).

## How a form comes into being

1. **Builder.** In the page builder add a **Form** section. Set the title, the slug (derived from an English title,
   editable; an Arabic title leaves it empty and asks for one), the button label, the thank-you message and the
   fields. Publishing the page creates the form with every field declared, so the server validates each one.
   Removing the section and republishing closes the form (it answers 410), and its table and entries stay.
   A draft page registers nothing, and the editor previews the form with every control disabled.
2. **Capture.** Any form that posts to `/forms/{slug}` with the CSRF token is accepted. Its first submission creates
   the form, and every field name it has not seen is added as *discovered*. In a theme:

   ```html
   <form method="post" action="/forms/quote">
     <input type="hidden" name="_csrf" value="{{csrf}}">
     <input name="name"> <input name="budget">
     <button type="submit">Send</button>
   </form>
   ```

   Page content cannot hold a `<form>` (the sanitizer removes it), so a captured form lives in a theme template.
   A discovered field can be **pinned** in the admin (type, label, choices, required). From then on the server
   validates it, and old entries keep what they stored.

A builder form that takes a captured form's slug adopts it: its fields become pinned and its entries are kept.
If another page already uses a slug, publishing moves the form to the page that publishes it, with its entries.

## Limits

| Limit | Value |
|---|---|
| Fields per form | 30 |
| Characters per value | 4,000 |
| Captured forms per site | 50 |
| File fields per form | 1 (builder forms only) |
| Choices per choice field | 20 |
| Form sections per page | 10 |
| Request body | 64 KiB (plus the attachment limit when a file field is shown) |

Captured field names match `[a-z0-9_]{1,40}`. Names that start with `_`, plus `website`, are ignored. Any other
breach rejects the whole submission with 422 and creates nothing, so a flood of random names cannot create a form.
Slugs match `[a-z0-9-]{1,40}`, and `contact` and `comment` are reserved.

## The admin tables

- **`/admin/forms`** lists each form with its source (builder or captured), entries, new entries and last entry.
  The contact form shows as a row that opens its own inbox, `/admin/system/form-submissions`.
- **`/admin/forms/{slug}`** shows date, status and up to six visible fields, newest first, 50 a page.
  - **Filters:** status (new, read, spam), a date range (inclusive days, UTC) and a text search over the stored values.
  - **CSV:** **Export** downloads every field, including hidden and retired ones, with the active filter applied.
    The columns are `id`, `created_at`, `status`, `page`, then the field names. Cells that start with `=`, `+`, `-`
    or `@` are neutralized against spreadsheet formula injection.
  - **Entry page:** opening an entry marks it read. An entry can be marked spam or deleted.
  - **Fields:** pin a discovered field, hide or show a column, rename its label, reorder. These change presentation only
    and never rewrite stored entries. A field a builder form no longer declares keeps its column in the CSV.
- **Attachments.** A builder form with a file field stores the file with the entry. Download it from the entry page.
  The download needs the same permission and is served as an attachment with `nosniff`.
- **Deleting.** A captured form, or a builder form whose section is gone, can be deleted from its table after a
  confirmation that shows the entry count. This removes its entries and files. An open builder form cannot be
  deleted (409). Deleting an entry removes its file. Exports and deletions are audited, without field values.

## Webhook

`site.form.submitted` fires once per stored entry (a double click is deduplicated). The payload is the contact
form's, plus:

```json
{ "id": "…", "form": "quote", "fields": { "name": "Alice", "budget": "500" },
  "page": "/quote", "ip": "…", "submitted_at": "…" }
```

`form` is the slug and `fields` holds the stored values as text. For the contact form `form` is `contact` and the
fixed `name`, `email` and `message` stay. The receipt and notification emails remain the contact form's alone.
See [WEBHOOKS.md](WEBHOOKS.md); the payload holds personal data, so point webhooks only at systems allowed to get it.

## The contact form stays as it is

`/forms/contact` keeps its fixed fields, its inbox at `/admin/system/form-submissions`, its CSV, attachment,
receipt email and `form_submissions` table. Nothing was migrated, and `contact` is not a slug for new forms.

## Showing a builder form in a theme

```html
{{form "booking" csrf .Request.URL.Path}}
```

This renders the published builder form `booking` with the same markup and validation as a builder page, and posts to
`/forms/booking`. The third argument is where the visitor returns to (a local path). It renders nothing for an
unknown, closed or captured form. Builder pages inject their forms automatically. See
[THEME-DEVELOPMENT.md](THEME-DEVELOPMENT.md) for the classes.

## Security

- **CSRF:** `POST /forms/{slug}` needs the token. A request without it is refused and stores nothing.
- **Honeypot:** a filled hidden `website` field gets a silent success and stores nothing.
- **Rate limit:** 5 submissions a minute per IP, shared with the contact form (comments and reports have their own limits).
- **Values are text.** They are stored as text, escaped wherever they are shown, and never evaluated. Numbers, dates
  and choices are checked on pinned fields.
- **No files on captured forms.** Only a builder form with a file field takes one: one file, checked against the contact
  form's size limit and type allowlist, and only while attachments are enabled.
- **Replay:** the same submission within the replay window creates one entry.
- **Audit:** submissions are recorded without their values.

## العربية

أي نموذج في موقعك يحفظ رسائله في لوحة التحكم، وله جدول خاص أعمدته هي حقوله. بعد حفظ الرسالة
يُرسل فقط حدث الويب هوك `site.form.submitted`، ولا توجد رسائل بريد جديدة. افتح **الوارد ← النماذج**
(`/admin/forms`) بنفس صلاحية صندوق التواصل.

**طريقتان لإنشاء نموذج**

1. **من المنشئ:** أضف قسم **نموذج** في صفحة المنشئ، وحدّد العنوان والمعرّف (slug) ونص الزر ورسالة الشكر والحقول.
   نشر الصفحة ينشئ النموذج وتصبح كل حقوله مُعرَّفة ويتحقق منها الخادم. العنوان العربي يترك المعرّف فارغًا
   فاكتبه بحروف لاتينية. حذف القسم ثم إعادة النشر يغلق النموذج (يردّ 410) وتبقى الرسائل. المسودة لا تُسجّل شيئًا،
   والمعاينة تعرض الحقول معطّلة.
2. **الالتقاط:** أي نموذج `method="post"` و`action="/forms/{slug}"` مع حقل `_csrf` يُقبل. أول رسالة تنشئ النموذج وتُسجَّل
   الحقول الجديدة كحقول *مكتشفة*. يوضع هذا النموذج في قالب الثيم لأن محتوى الصفحات يحذف وسم `<form>`.
   يمكن **تثبيت** الحقل المكتشف من اللوحة (النوع والعنوان والخيارات والإلزام) فيتحقق منه الخادم بعدها.

**الحدود:** 30 حقلًا في النموذج، و4000 حرف في القيمة، و50 نموذجًا ملتقطًا، وحقل ملف واحد (للنماذج من المنشئ فقط)،
و20 خيارًا في الحقل. أسماء الحقول الملتقطة `[a-z0-9_]` حتى 40 حرفًا. أي مخالفة ترفض الرسالة كلها (422) ولا تنشئ نموذجًا.

**الجداول:** كل نموذج له جدول بالأحدث أولًا، 50 في الصفحة، مع فلتر الحالة (جديد/مقروء/سبام) والتاريخ وبحث نصي.
زر **تصدير** ينزّل CSV بكل الحقول (حتى المخفية والقديمة) ويحترم الفلتر الحالي. فتح الرسالة يجعلها مقروءة، ويمكن تحديدها سبام
أو حذفها. تثبيت الحقول وإخفاؤها وإعادة تسميتها وترتيبها يغيّر العرض فقط ولا يعدّل البيانات المحفوظة. المرفق (لنموذج المنشئ
بحقل ملف) يُنزَّل من صفحة الرسالة بنفس الصلاحية. حذف نموذج ملتقط (أو نموذج منشئ أُزيل قسمه) يتم بعد تأكيد يعرض عدد
الرسائل، ويحذف رسائله وملفاته. النموذج المنشور المفتوح لا يُحذف. التصدير والحذف يُسجَّلان في سجل التدقيق.

**الويب هوك:** `site.form.submitted` مرة واحدة لكل رسالة، ويحمل `form` (المعرّف) و`fields` (القيم نصوصًا) إلى جانب
بيانات نموذج التواصل. الحمولة فيها بيانات شخصية، فوجّهها لأنظمة مسموح لها باستلامها.

**نموذج التواصل كما هو:** `/forms/contact` وصندوقه `/admin/system/form-submissions` وCSV والمرفق ورسالة الاستلام وجدول
`form_submissions` لم تتغير، ولا يمكن استخدام `contact` معرّفًا لنموذج جديد.

**في الثيم:**

```html
{{form "booking" csrf .Request.URL.Path}}
```

يعرض النموذج المنشور من المنشئ بنفس الشكل والتحقق، ولا يعرض شيئًا لنموذج غير موجود أو مغلق أو ملتقط.

**الأمان:** رمز CSRF مطلوب (بدونه يُرفض الطلب ولا يُحفظ شيء). حقل `website` المخفي فخّ للروبوتات فيُقبل بصمت ولا يُحفظ شيء.
الحد 5 رسائل في الدقيقة لكل IP، مشتركًا مع نموذج التواصل (للتعليقات والبلاغات حدودها الخاصة). القيم تُحفظ نصوصًا وتُعرض مُهرَّبة. النماذج الملتقطة لا تقبل
ملفات. الضغط المزدوج على زر الإرسال ينتج رسالة واحدة. التدقيق يسجّل الإرسال بدون القيم.
