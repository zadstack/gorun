# Site analytics

GoRun counts who visits your site, what they read, where they came from, on what device and from which
country, and how many of them submit a form. It is built in, sets no cookie, needs no consent banner and
sends nothing to a third party. It is on by default.

## What is counted

- **Page views** from a tiny script, `/_a/s.js` (under 1 KB, served from your own site). GoRun adds it
  before `</head>` of public HTML pages. On load, and after `history.pushState` or `popstate`, it sends one
  beacon to `/_a/e` with the path, the referrer, the window width and the page language.
- **Only the path counts.** A change of the `#hash` alone is not a new view. The query string is dropped,
  except `utm_source`, `utm_medium` and `utm_campaign`.
- **Unique visitors** are a cookieless daily hash: `sha256(daily random salt + site + IP + user agent)`,
  cut to 16 characters. The salt is random, changes at UTC midnight and the old one is deleted, so a
  visitor cannot be followed from one day to the next, and the hash cannot be reversed.
- **No IP address and no user agent are stored.** The IP is used only to make the hash and to look up the
  country. The user agent is reduced to a browser family, an OS family and a device size (mobile below 768
  px, tablet below 1024 px, otherwise desktop).
- **Referrers** are reduced to their host. An empty referrer, or your own site, is "direct".
- **Do Not Track and Global Privacy Control are respected.** If the browser sends either, the script does
  nothing.
- **Staff are not counted.** Pages are not tracked (and the script is not added) for anyone signed in with
  `content.edit`, for `/admin`, for builder and theme previews, and for responses that are not a 200 HTML
  page. Known bots and crawlers are dropped.
- **Form submissions** are counted as `submit` events (form slug and page) through the
  `site.form.submitted` hook. They carry no visitor, so they count submissions only.

## Country (optional GeoIP)

Without a database, every country shows as "unknown". To get countries, download the free
**GeoLite2-Country** database from MaxMind and save it as:

```
<data dir>/GeoLite2-Country.mmdb
```

`<data dir>` is the folder that holds `gorun.db` (`GORUN_DATA`). Restart GoRun after adding or replacing
the file. **Settings → Analytics** shows whether the file was found. GoRun does not ship or download it,
and you must follow MaxMind's licence.

## Settings

Open **Settings → Analytics** (`/admin/settings/analytics`, permission `settings.manage`).

- **Tracking on or off.** Off stops the script and the beacon. Existing data stays.
- **Excluded paths.** One prefix per line, each starting with `/`. It is a plain prefix match: `/shop` also
  matches `/shopping` and `/shop/cart`. Add a trailing slash (`/shop/`) to match a folder only.
  Up to 200 lines of 512 bytes.
- **Leave analytics out of backups.** Off by default (see Backups).
- **Delete data.** *Delete before a date* or *Delete all*. Both need you to type `DELETE`, are written to
  the audit log, and compact the file afterwards. Today's salt is kept so counting continues.
- **GeoIP status** and the number of stored events.

## Dashboard, CSV and API

**Analytics** (`/admin/analytics`, permission `analytics.view`) is under Site. An administrator has the
permission already; give it to a marketer's role to let them read without managing settings.

- Pick **today, 7 days, 30 days, 12 months or a custom range**. Every card compares with the previous period
  of the same length and says "up", "down" or "flat" as well as the percentage.
- Cards: visitors, views, views per visitor and submissions. A chart (server-drawn SVG with a hidden data
  table) shows visitors and views per day, or per hour for one day.
- Tables: top pages, sources (with a **Campaigns** tab for UTM), countries and devices (with **Browsers** and
  **OS** tabs). Each shows 10 rows; **Show all** shows up to 500. Click a row to filter the whole page. The
  filters are chips and live in the URL, so you can bookmark or share the view.
- **Forms** lists each form's page views, submissions and conversion rate, linking to its entries.
  Submissions cannot be split by source, country or device, so those filters hide the numbers.
- **Now** is the number of visitors in the last 5 minutes, refreshed every 30 seconds while the tab is open.
- A 7-day **widget** is on the admin home page.
- **CSV**: every table has an export link that keeps the period and filters. Files are UTF-8 with a BOM and
  cells that could run as spreadsheet formulas are neutralised. The export is not capped at 500 rows.
- **API**: `GET /api/v1/analytics/summary?from=&to=&page=&ref=&country=&device=`, with an API key that
  has the `analytics.view` scope. `from` and `to` are UTC days (`to` inclusive) or RFC 3339 times (`to`
  exclusive); the default is the last 7 days. **A range may be at most 400 days**, and each filter value
  **at most 512 bytes**; otherwise the answer is `400 validation_failed` naming the field. See
  `docs/API.md` for the response.

Everything is available in Arabic and English. In Arabic the page is right to left and the chart is mirrored.

## Backups

If `analytics.db` exists, each backup also writes `<stamp>-analytics.db`, a consistent copy recorded in the
manifest as an **optional** member. Older backups and sites without it stay valid. A restore puts the file
back next to `gorun.db` in the new directory. Details are in [BACKUPS.md](BACKUPS.md).

Analytics never blocks a core backup or an update: if `analytics.db` cannot be copied, the backup finishes
without it and shows a warning ("warn and skip"), which is also audited. Turn on **Leave analytics out of
backups**, or run `gorun backup --skip-analytics`, to leave it out on purpose.

## Turn the module off

Analytics is an optional module. To remove it completely (no script, no routes, no `analytics.db`):

```
GORUN_ADDONS='{"analytics":"disabled"}'
```

Restart GoRun. Existing `analytics.db` is left untouched on disk, so enabling the module again brings the
history back.

## Privacy notes

- No cookie, no local storage, no fingerprinting beyond the daily hash; no consent banner is needed for this
  data in most jurisdictions, but check your own legal advice.
- Nothing leaves your server. There is no external request and no third-party script.
- The visitor hash cannot be linked across days, and it is not stored for form submissions.
- Paths and UTM values you put in a URL are stored as written, so do not put personal data in URLs.
- **Raw events are kept forever.** There is no automatic expiry. Use **Delete before a date** in settings to
  trim old data. Rollups (`daily`) are rebuilt hourly for today and yesterday.
- The data lives in its own file, `<data dir>/analytics.db`.

## العربية

# إحصائيات الموقع

يحصي GoRun زوّار موقعك: من زاره، وماذا قرأ، ومن أين جاء، وبأي جهاز، ومن أي بلد، وكم منهم أرسل نموذجًا.
الميزة مدمجة، ولا تضع ملف تعريف ارتباط، ولا تحتاج شريط موافقة، ولا ترسل شيئًا إلى طرف ثالث. وهي مفعّلة
افتراضيًا.

## ما الذي يُحسب

- **مشاهدات الصفحات** عبر سكربت صغير `/_a/s.js` (أقل من 1 كيلوبايت ويُقدَّم من موقعك). يضيفه GoRun قبل
  `</head>` في صفحات الموقع العامة، ويرسل إشارة واحدة إلى `/_a/e` فيها المسار والمُحيل وعرض النافذة ولغة الصفحة.
- **المسار وحده هو ما يُحسب.** تغيير `#hash` فقط ليس مشاهدة جديدة. يُحذف الاستعلام عدا `utm_source` و
  `utm_medium` و`utm_campaign`.
- **الزوّار الفريدون** بتجزئة يومية بلا ملفات تعريف ارتباط: `sha256(ملح يومي عشوائي + الموقع + IP + وكيل
  المستخدم)` مقتطعة إلى 16 حرفًا. يتغير الملح عند منتصف الليل بتوقيت UTC ويُحذف القديم، فلا يمكن تتبع زائر
  من يوم إلى آخر ولا عكس التجزئة.
- **لا يُخزَّن عنوان IP ولا وكيل المستخدم.** يُستخدم IP لصنع التجزئة ومعرفة البلد فقط. ويُختزل وكيل المستخدم
  إلى عائلة المتصفح وعائلة النظام وحجم الجهاز (جوال أقل من 768، لوحي أقل من 1024، وإلا حاسوب).
- **المُحيل** يُختزل إلى اسم المضيف. والمُحيل الفارغ أو موقعك نفسه يُعدّ «مباشر».
- **يُحترم «عدم التتبع» (DNT) وGPC.** إن أرسلهما المتصفح لا يفعل السكربت شيئًا.
- **الموظفون لا يُحسبون.** لا يُتتبَّع من سجّل دخوله بصلاحية `content.edit`، ولا `/admin`، ولا معاينات
  المنشئ والقالب، ولا ما ليس صفحة HTML بحالة 200. وتُستبعد الروبوتات المعروفة.
- **إرسال النماذج** يُحسب حدثًا من نوع `submit` (اسم النموذج والصفحة) عبر الخطّاف `site.form.submitted`، دون
  زائر، فيحصي عدد الإرسالات فقط.

## البلد (GeoIP اختياري)

دون قاعدة بيانات يظهر البلد «غير معروف». للحصول على البلدان نزّل قاعدة **GeoLite2-Country** المجانية من
MaxMind واحفظها باسم:

```
<مجلد البيانات>/GeoLite2-Country.mmdb
```

مجلد البيانات هو الذي فيه `gorun.db` (`GORUN_DATA`). أعد تشغيل GoRun بعد إضافة الملف أو استبداله. تُظهر
صفحة **الإعدادات ← الإحصائيات** هل وُجد الملف. لا يوزّع GoRun الملف ولا ينزّله، والتزم بترخيص MaxMind.

## الإعدادات

افتح **الإعدادات ← الإحصائيات** (`/admin/settings/analytics`، الصلاحية `settings.manage`).

- **تشغيل التتبع أو إيقافه.** الإيقاف يوقف السكربت والإشارة، وتبقى البيانات.
- **مسارات مستبعدة.** بادئة في كل سطر تبدأ بـ `/`. المطابقة بادئة نصية بسيطة: `/shop` يطابق `/shopping`
  و`/shop/cart` أيضًا. أضف شرطة أخيرة (`/shop/`) لمطابقة المجلد فقط. حتى 200 سطر، وكل سطر حتى 512 بايت.
- **استبعاد الإحصائيات من النسخ الاحتياطية.** معطَّل افتراضيًا (انظر النسخ الاحتياطية).
- **حذف البيانات.** «حذف ما قبل تاريخ» أو «حذف الكل». يتطلبان كتابة `DELETE`، ويُسجَّلان في سجل التدقيق،
  ويُضغط الملف بعدهما. يبقى ملح اليوم ليستمر العدّ.
- **حالة GeoIP** وعدد الأحداث المخزّنة.

## اللوحة وCSV وواجهة API

**الإحصائيات** (`/admin/analytics`، الصلاحية `analytics.view`) تحت قسم الموقع. المدير يملك الصلاحية أصلًا،
ويمكنك منحها لدور مسوّق ليقرأ دون إدارة الإعدادات.

- اختر **اليوم، 7 أيام، 30 يومًا، 12 شهرًا أو نطاقًا مخصصًا**. تقارن كل بطاقة بالفترة السابقة المماثلة، وتذكر
  «ارتفاع» أو «انخفاض» أو «ثابت» مع النسبة.
- البطاقات: الزوّار والمشاهدات والمشاهدات لكل زائر والإرسالات. ورسم بياني (SVG من الخادم مع جدول بيانات
  مخفي) للزوّار والمشاهدات يوميًا، أو بالساعة ليوم واحد.
- الجداول: أكثر الصفحات، والمصادر (مع تبويب **الحملات** لـ UTM)، والبلدان، والأجهزة (مع تبويبي **المتصفحات**
  و**الأنظمة**). يعرض كل جدول 10 صفوف، و**عرض الكل** حتى 500. انقر صفًا لتصفية اللوحة كلها. تظهر المرشحات
  شرائح وتُحفظ في الرابط فيمكن وضع إشارة مرجعية عليها أو مشاركتها.
- **النماذج** تعرض لكل نموذج مشاهدات صفحته وإرساله ونسبة التحويل، برابط إلى مدخلاته. لا يمكن تقسيم
  الإرسالات حسب المصدر أو البلد أو الجهاز، فتخفي هذه المرشحات أرقامها.
- **الآن** عدد الزوّار في آخر 5 دقائق، ويتجدد كل 30 ثانية ما دامت التبويبة ظاهرة.
- **أداة** لآخر 7 أيام في الصفحة الرئيسية للوحة الإدارة.
- **CSV**: لكل جدول رابط تصدير يحافظ على الفترة والمرشحات. الملف UTF-8 مع BOM، وتُعطَّل الخلايا التي قد
  تُنفَّذ صيغةً في جداول البيانات. ولا يُقيَّد التصدير بـ 500 صف.
- **API**: `GET /api/v1/analytics/summary?from=&to=&page=&ref=&country=&device=` بمفتاح API له نطاق
  `analytics.view`. القيمتان `from` و`to` يوم UTC (`to` شاملة) أو وقت RFC 3339 (`to` غير شاملة)، والافتراضي
  آخر 7 أيام. **أقصى مدى 400 يوم**، و**أقصى طول لقيمة المرشح 512 بايت**، وإلا فالرد `400 validation_failed`
  مع اسم الحقل. راجع `docs/API.md` لشكل الرد.

كل شيء متاح بالعربية والإنجليزية. وفي العربية تكون الصفحة من اليمين إلى اليسار ويُعكس الرسم البياني.

## النسخ الاحتياطية

إن وُجد `analytics.db` كتبت كل نسخة احتياطية أيضًا `<stamp>-analytics.db`، نسخةً متسقة مسجّلة في الملف
الوصفي عضوًا **اختياريًا**. تبقى النسخ القديمة والمواقع التي بلا هذا الملف صالحة. تضع الاستعادة الملف إلى جانب
`gorun.db` في المجلد الجديد. التفاصيل في [BACKUPS.md](BACKUPS.md).

لا تعطّل الإحصائيات نسخة أساسية ولا تحديثًا: إن تعذّر نسخ `analytics.db` اكتملت النسخة بدونه مع تحذير
(تحذير وتخطٍّ) ويُسجَّل في التدقيق. فعّل **استبعاد الإحصائيات من النسخ الاحتياطية** أو شغّل
`gorun backup --skip-analytics` لاستبعادها عمدًا.

## إيقاف الوحدة

الإحصائيات وحدة اختيارية. لإزالتها كليًا (بلا سكربت ولا مسارات ولا `analytics.db`):

```
GORUN_ADDONS='{"analytics":"disabled"}'
```

أعد تشغيل GoRun. يبقى `analytics.db` القائم على القرص دون مساس، فإعادة التفعيل تعيد السجل.

## ملاحظات الخصوصية

- لا ملفات تعريف ارتباط ولا تخزين محلي ولا بصمة جهاز سوى التجزئة اليومية؛ ولا يلزم شريط موافقة لهذه البيانات في
  أغلب الأنظمة، لكن راجع استشارتك القانونية.
- لا يغادر شيء خادمك. لا طلبات خارجية ولا سكربت لطرف ثالث.
- لا تُربط التجزئة بين الأيام، ولا تُخزَّن لإرسالات النماذج.
- تُخزَّن المسارات وقيم UTM كما كُتبت، فلا تضع بيانات شخصية في الروابط.
- **الأحداث الخام تُحفظ إلى الأبد.** لا انتهاء تلقائيًا. استخدم «حذف ما قبل تاريخ» في الإعدادات لتقليص القديم.
  وتُعاد بناء التجميعات اليومية (`daily`) كل ساعة لليوم والأمس.
- البيانات في ملفها الخاص `<مجلد البيانات>/analytics.db`.
