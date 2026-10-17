# Metricool · مسودات الشبكات الاجتماعية

GoRun can create **text drafts** in a configured Metricool brand. It does not schedule or publish social posts, upload media, read analytics, or automatically send website content. Metricool API access requires an Advanced or Custom plan according to its [official guide](https://help.metricool.com/basic-guide-for-api-integration-r97af). The integration follows the [official API schema](https://app.metricool.com/api/swagger.json).

1. An administrator opens **Settings → Metricool connection** (`/admin/settings/metricool`) and enters the account `userId`, brand `blogId` and API token. The token is encrypted with the application secret key and never redisplayed. A blank field keeps it; the explicit clear option removes it. Keep the application key stable so existing credentials remain decryptable.
2. **Test brand access** only reads `GET /api/admin/simpleProfiles` and checks that the configured brand exists and is not deleted. Passing proves read access at that time, not social posting permissions or draft creation. Changing settings clears this result.
3. A user with `content.publish` opens **Metricool drafts** (`/admin/metricool`), writes/reviews text, selects an already connected text-capable network, supplies a future planning date and IANA timezone, and checks the explicit review/authorization box.
4. **Send reviewed draft** sends one request to `POST /api/v2/scheduler/posts`, always with `draft: true` and `autoPublish: false`. GoRun confirms success only when the response includes a positive draft ID and confirms draft status. Any final scheduling or public publication happens separately in Metricool.

Text networks offered: Facebook, X/Twitter, LinkedIn, Threads and Bluesky. Availability and text limits also depend on the actual connected network. Planning metadata is required by Metricool even for drafts; choose a future time within two years. GoRun accepts up to 12,000 UTF-8 bytes and 20 requests per user per hour. It does not select or extract private CMS content.

Every form carries a unique request ID. GoRun durably records the actor and a digest of the exact text, planning data and configuration version **before** contacting Metricool. Repeating that form cannot send another request; changing its payload or actor is rejected. A timeout, invalid response, crash, or failure to persist confirmation remains **uncertain**, even if Metricool might have saved the draft. Check Metricool before deliberately starting another draft. There is no automatic POST retry or automatic reconciliation. The private ledger retains IDs/statuses/digests, not draft text or provider secrets.

The connection starts unconfigured on each instance. Fake-provider tests verify the request/response contract and native authorization/CSRF/idempotency. No live Metricool token or real draft creation was available during implementation; configure and verify your own account before relying on it.

## العربية

يرسل GoRun **مسودات نصية فقط** إلى علامة محددة في Metricool. لا توجد جدولة أو نشر اجتماعي تلقائي أو رفع وسائط أو تحليلات أو إرسال تلقائي لمحتوى الموقع.

1. افتح **اتصال Metricool** كمسؤول، وأدخل `userId` و`blogId` ورمز API. يُحفظ الرمز مشفرًا ولا يُعرض مجددًا. الحقل الفارغ يُبقي الرمز الحالي، وخيار المسح الصريح يزيله.
2. اختبر الوصول للعلامة. الاختبار يقرأ قائمة العلامات فقط؛ لا يثبت صلاحية إنشاء المسودات أو النشر. تعديل الإعدادات يلغي نتيجة الاختبار السابقة.
3. من **مسودات Metricool**، يكتب المستخدم صاحب صلاحية `content.publish` النص، ويختار شبكة مرتبطة مسبقًا، وتاريخ تخطيط لاحقًا ومنطقة زمنية IANA، ثم يؤكد مراجعة النص والموافقة على إرساله كمسودة.
4. يحفظ GoRun الطلب مرة واحدة ويرسل `draft=true` و`autoPublish=false` دائمًا. تأكيد النجاح يحتاج معرّفًا صالحًا وحالة مسودة في رد Metricool. أي نشر لاحق يتم داخل Metricool بصورة منفصلة.

إذا تعذّر تأكيد النتيجة، راجع Metricool قبل إنشاء طلب جديد: قد تكون المسودة حُفظت رغم انقطاع الاتصال. إعادة إرسال نفس النموذج لا تعيد الطلب الخارجي. لا نعيد المحاولة تلقائيًا ولا نزعم أن الاتصال مفعل قبل إدخال بياناتك واختباره.
