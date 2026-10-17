# Private report intake · البلاغات الخاصة

Private intake is **disabled by default**. It provides a limited, human-operated queue for school safety or violence concerns and follow-ups. It is not an emergency channel, a promise of investigation, or a complete safeguarding/case-management system. The operator must designate reviewers, provide an appropriate privacy notice and define its own response process before enabling it.

## Enable and receive

An administrator with `settings.manage` opens `/admin/settings/reports` and explicitly enables intake. Public forms are `/reports/incident` and `/reports/follow-up`, with the ordinary site locale prefix for other languages (for an English-default site: `/ar/reports/incident`). No account is required. Follow-up requires the previous reference; this is a new report, not a public lookup of the original.

Forms accept a category, general area, optional date, description, optional email and follow-up reference. The confirmation checkbox is required. Descriptions are limited to 5,000 characters; request bodies to 96 KiB before CSRF parsing. There are no attachments. The form asks reporters to avoid children’s names and unnecessary identifying information. Report references do not grant read access.

Accepted reports are encrypted with the application key and per-record associated data in the separate `private_intake` table. They do not enter CMS content, search, sitemap, contact submissions, email hooks, API/MCP content tools or AI workflows. Browser responses use no-store/no-referrer/noindex. This does not promise anonymous network access or end-to-end encryption: normal infrastructure logs may exist. Preserve the application encryption key; losing it makes the stored payloads unreadable.

## Review

Only `intake.review` grants `/admin/reports` and its detail/update routes. Administrators have their existing wildcard grant; ordinary editors are denied. The list shows reference, kind, status and timestamps, not the report body or contact details. Opening a detail is audited without its payload.

A reviewer can set New / Under review / Needs information / Closed, and append a private note. Every update records the actor ID, time and status inside the encrypted payload. Form versions are checked atomically, so stale or repeated submissions cannot overwrite another review. Notes are limited to 3,000 characters and 100 updates per record. A status change does not contact the reporter and does not assert a finding. Disabling intake stops public submission while preserving restricted access to existing records.

No exports, bulk downloads, email notifications, deletion/retention automation, external escalation or public tracking are included. Keep operational procedures and access review appropriate to your institution. Tests use synthetic reports only; no real child or reporter data is seeded.

## العربية

الاستقبال **معطّل افتراضيًا**. يوفّر قائمة خاصة لمراجعة البلاغات والمتابعات بشريًا، وليس خدمة طوارئ أو ضمان تحقيق أو نظامًا متكاملًا لإدارة الحالات. عيّن المراجعين وإجراءات الاستجابة وإشعار الخصوصية قبل التفعيل من `/admin/settings/reports`.

النموذجان العامان هما `/reports/incident` و`/reports/follow-up`، مع بادئة اللغة المعتادة. لا يلزم حساب. المتابعة تُنشئ بلاغًا جديدًا بمرجع سابق؛ المرجع لا يسمح بقراءة البلاغ الأصلي. لا توجد مرفقات أو بحث عام بالمرجع.

تُشفّر البيانات والملاحظات في جدول مستقل بمفتاح التطبيق. لا تصل إلى محتوى الموقع أو البحث أو صندوق التواصل أو البريد أو الذكاء الاصطناعي. لا يعني ذلك إخفاء سجلات الشبكة أو تشفيرًا تامًا بين الطرفين.

المراجعة تحتاج `intake.review`؛ المحرر العادي ممنوع. تظهر القائمة بيانات مرجعية فقط، والتفاصيل للمراجع المصرّح له. تغيير الحالة وإضافة ملاحظة يتطلبان نسخة حديثة من النموذج ويُسجّلان مع هوية المراجع وتوقيت الإجراء. تغيير الحالة لا يرسل ردًا ولا يثبت نتيجة. تعطيل الاستقبال لا يمس البلاغات الموجودة.
