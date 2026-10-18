# GoRun

[![ci](https://github.com/zadstack/gorun/actions/workflows/ci.yml/badge.svg)](https://github.com/zadstack/gorun/actions/workflows/ci.yml) [![License: AGPL v3](https://img.shields.io/badge/license-AGPL--3.0-167252.svg)](LICENSE) ![Go 1.27](https://img.shields.io/badge/go-1.27-256b9b.svg)

![GoRun — your content, your team, your agents](assets/branding/github-banner.svg)

**العربية** · [English](#english)

**نظام إدارة محتوى يعمل فيه فريقك ووكلاء الذكاء الاصطناعي معًا، وتبقى القرارات الحساسة بيد البشر.**

يجمع GoRun في لوحة تحكم واحدة، عربية وإنجليزية مع دعم كامل لاتجاه RTL، بين سير عمل الفريق، ووكلاء يعملون عبر REST وMCP بصلاحيات محددة، ولوحات عمل (Canvases) تربط المحتوى بالتعليمات والنتائج. وهو نظام مفتوح المصدر تستضيفه بنفسك بترخيص [GNU AGPL v3](LICENSE).

[الفريق](#للفريق) · [الوكلاء](#للوكلاء) · [اللوحات](#اللوحات-canvases) · [البدء السريع](#البدء-السريع) · [التوثيق](#التوثيق)

لقطات الشاشة من نسخة محلية معزولة بمحتوى تجريبي خيالي (Nakhla Journal)، لا من بيانات عملاء ولا من مزود متصل.

### للفريق

- **أدوار تناسب طريقة عمل المحررين.** أدوار جاهزة هي `admin` و`editor` و`author` و`contributor`، ولكل منها صلاحيات صريحة.
- **مراجعة قبل النشر.** مسودات، وحالة «بانتظار المراجعة»، ونشر مجدول، وسجل نسخ يمكن الاستعادة منه، وسلة محذوفات.
- **سجل يوثّق من فعل ماذا.** سجل تدقيق لا يُعدَّل بعد كتابته، يشمل المستخدمين والمفاتيح، ومنها كل استدعاء لأدوات MCP.
- **محتوى منظّم.** مقالات وصفحات وأنواع وحقول مخصصة وتصنيفات وقوائم ووسائط في لوحة تحكم واحدة.

<table>
  <tr>
    <td width="50%"><img src="assets/branding/screenshots/content-list-ar.png" alt="قائمة المقالات في لوحة تحكم GoRun بالعربية واتجاه من اليمين إلى اليسار، مع مقالات منشورة ومسودات بالعربية والإنجليزية" /></td>
    <td width="50%"><img src="assets/branding/screenshots/editor-en.png" alt="محرر المقالات في GoRun بالإنجليزية مع النص والمقتطف والصورة البارزة ولوحة النشر" /></td>
  </tr>
  <tr>
    <td><strong>المحتوى</strong><br />واجهة عربية أصيلة لمحتوى بلغتين.</td>
    <td><strong>المحرر</strong><br />مسودات ونشر مجدول وصورة بارزة؛ سجل النسخ في الشريط الجانبي.</td>
  </tr>
</table>

### للوكلاء

1. أنشئ مفتاحًا من نوع **AI agent** من **وكلاء الذكاء الاصطناعي ← مفاتيح API** بالنطاقات (scopes) التي يحتاجها فقط.
2. وجّه عميل MCP إلى `https://your-site.example/mcp` (أو شغّل `gorun mcp` محليًا عبر stdio).
3. راجع الطلبات الحساسة من **وكلاء الذكاء الاصطناعي ← الموافقات**؛ ويستعلم الوكيل عن النتيجة بالأداة `check_approval`.

- **حدود واضحة من الأساس.** لا يتجاوز المفتاح صلاحيات دور صاحبه، ولا يفعل إلا ما تسمح به نطاقاته.
- **البشر يوافقون على الخطوات الحساسة.** النشر والحذف وتغيير الإعدادات وتعديل المحتوى المنشور تنتظر موافقة بشرية افتراضيًا. لا تستطيع مفاتيح الوكلاء استخدام REST للموافقة على طلباتها، والموافقة الذاتية نطاق مستقل لا يمنحه `*`.
- **REST لبقية التكاملات.** تستخدم السكربتات والخدمات مفاتيح REST منفصلة على `/api/v1` (موصوفة في `/api/v1/openapi.json`)، أما مفاتيح الوكلاء فمرفوضة هناك وتبقى على MCP. وتُبلغ الـWebhooks الخدمات الأخرى بالتغييرات.

يمكنك أن تبدأ بهذه التعليمات بعد ربط الوكيل:

```text
Inspect the site's structure, content types, and the tools available to your key.
Explain your proposed changes before writing anything.
Create new content as drafts and preserve existing content and settings.
If a tool requests approval, report its request ID and wait for the decision.
```

[MCP والصلاحيات](docs/MCP.md) · [واجهة REST](docs/API.md) · [نماذج الوكلاء](docs/AGENT-MODELS.md)

<table>
  <tr>
    <td width="50%"><img src="assets/branding/screenshots/agent-keys-en.png" alt="صفحة مفاتيح API في GoRun بالإنجليزية: مفاتيح وكلاء ومفتاح REST، لكل منها نطاقات محددة مثل content.view وcanvas.run" /></td>
    <td width="50%"><img src="assets/branding/screenshots/approvals-ar.png" alt="قائمة موافقات الوكلاء في GoRun بالعربية: طلبا نشر من وكيل التحرير بانتظار القرار، وقرار موافقة سابق" /></td>
  </tr>
  <tr>
    <td><strong>مفاتيح الوكلاء</strong><br />نطاقات محددة لكل مفتاح، وREST منفصل عن MCP.</td>
    <td><strong>الموافقات</strong><br />طلبات الوكيل الحساسة تنتظر قرارًا بشريًا.</td>
  </tr>
</table>

### اللوحات (Canvases)

اللوحات (**لوحة التحكم ← اللوحات**) مساحة عمل تشغيلية، وليست أداة لتصميم الصفحات.

![يُرسَل محتوى مختار وتعليمات إلى وكيل متصل، وتصبح النتيجة ملاحظة خاصة جديدة لمراجعتك](assets/branding/reviewed-workflow.svg)

- **اجمع عملك في مكان واحد.** ملاحظات وقوائم مهام وروابط ومراجع لمقالات وصفحات، وودجات للموافقات والمهام الخلفية وطلبات الخدمة.
- **شغّل الوكلاء على سياق تختاره أنت.** اربط المصادر والتعليمات بمزوّد نصوص مُعَدّ أو بمفتاح وكيل مسمّى، وراجع السياق كما سيُرسَل، ثم شغّل.
- **النتائج تصل ملاحظاتٍ جديدة.** تُضاف كل نتيجة ملاحظةً خاصة مع بيان مصدرها، ولا تستبدل بطاقاتك ولا تنشر أي محتوى.
- **تكرار دوري بعد المراجعة.** الجدولة كل ساعة أو يوم أو أسبوع تبدأ معطّلة، وتحتاج تفعيلًا صريحًا، وتعيد التحقق من الصلاحيات في كل تشغيل.
- **صلاحيات صريحة للوكلاء.** أنت تحدد العناصر التي يستطيع كل وكيل قراءتها أو الإضافة إليها أو التشغيل عليها في كل لوحة.

<img src="assets/branding/screenshots/canvas-en.png" alt="لوحة في GoRun بالإنجليزية: مرجع لمقال وتعليمات متصلة بوكيل بحث مسمّى، وملاحظة المحرر، ونتيجة الوكيل مضافة ملاحظةً جديدة مع مصدرها" />

[مساحة اللوحات](docs/CANVASES.md) · [عمليات اللوحات](docs/CANVAS-OPERATIONS-CONTRACT.md) · [استوديو المحتوى](docs/CONTENT_STUDIO.md)

### وأيضًا

| المجال | ما تحصل عليه |
| --- | --- |
| منشئ المواقع والقوالب | مواقع جاهزة للبدء، ومنشئ صفحات مبني على الأقسام (12 نوعًا من الأقسام، وتراجع وإعادة، وحفظ تلقائي، ومعاينات)، وخمسة قوالب، وقوالب فرعية، وتخصيص القوالب |
| الترحيل من ووردبريس | استيراد موجّه عبر REST أو ملف WXR مع ربط الحقول ومعاينة تجريبية وتحقق؛ لا تُنقل الإضافات ولا تخطيطات منشئي الصفحات ولا التعليقات ولا طلبات WooCommerce |
| استوديو المحتوى | مقترحات نصوص ووسائط قابلة للمراجعة من مزودي ذكاء اصطناعي أو MCP مُعدّين صراحة |
| التشغيل | مهام خلفية دائمة، وفحوص صحة، ونسخ احتياطي، وتحويلات، وبيانات SEO، وخرائط موقع، وتخزين وسائط متوافق مع S3 |
| التجارة | منتجات رقمية، والدفع عبر Stripe Checkout، واستحقاقات للوصول إلى MCP |

الميزات المعتمدة على مزودين خارجيين تحتاج بيانات اعتماد صالحة. حفظ الاتصال أو تحميل قائمة النماذج لا يثبت أن التوليد أو الدفع يعمل.

[منشئ المواقع](docs/SITE_BUILDER.md) · [القوالب](docs/THEMES.md) · [الترحيل](docs/MIGRATION.md) · [التجارة](docs/COMMERCE.md)

<table>
  <tr>
    <td width="40%"><img src="assets/branding/screenshots/builder-ar.png" alt="منشئ الصفحات في GoRun بالعربية: محرر أقسام الواجهة الرئيسية بجانب معاينة مباشرة للصفحة" /></td>
    <td width="40%"><img src="assets/branding/screenshots/theme-home-en.png" alt="الصفحة الرئيسية للقالب الافتراضي بالإنجليزية مع مقال بارز وصورته" /></td>
    <td width="20%"><img src="assets/branding/screenshots/theme-post-ar-mobile.png" alt="مقال عربي في القالب الافتراضي على شاشة جوال" /></td>
  </tr>
  <tr>
    <td><strong>منشئ الصفحات</strong><br />أقسام ومعاينة مباشرة ونشر صريح.</td>
    <td><strong>القالب الافتراضي</strong><br />CSS مبني مسبقًا، بلا سكربتات خارجية.</td>
    <td><strong>على الجوال</strong><br />RTL كامل.</td>
  </tr>
</table>

### البدء السريع

**صورة Docker:** تُنشر الإصدارات صورةً متعددة المعماريات (linux/amd64 وlinux/arm64) على GHCR بالوسوم `0.1.0` و`0.1` و`latest`:

```sh
docker run -d --name gorun -p 8080:8080 -v gorun-data:/data \
  -e GORUN_BASE_URL=https://cms.example.com \
  -e GORUN_SECRET_KEY="<openssl rand -base64 32>" \
  -e GORUN_SETUP_TOKEN="<openssl rand -hex 24>" \
  ghcr.io/zadstack/gorun:0.1.0
```

تعمل الصورة في وضع الإنتاج (`GORUN_ENV=prod`) وتحفظ بياناتها في `/data`. احتفظ بقيمة `GORUN_SECRET_KEY` ثابتة لأنها توقّع الجلسات وتشفّر الأسرار المحفوظة، وأدخل `GORUN_SETUP_TOKEN` في `/admin/setup` عند إنشاء المدير الأول ثم احذفه. ملف Compose الجاهز وخطوات Coolify في [دليل Docker / Coolify](deploy/coolify.md).

**الملفات التنفيذية:** كل [إصدار على GitHub](https://github.com/zadstack/gorun/releases) يرفق أرشيفات لنظامي linux وdarwin بمعماريتي amd64 وarm64، وفيها البرنامج ومجلد `themes/` والوثائق، مع ملف `SHA256SUMS`:

```sh
curl -fLO https://github.com/zadstack/gorun/releases/download/v0.1.0/gorun-0.1.0-linux-amd64.tar.gz
curl -fLO https://github.com/zadstack/gorun/releases/download/v0.1.0/SHA256SUMS-0.1.0.txt
sha256sum --ignore-missing -c SHA256SUMS-0.1.0.txt
tar xzf gorun-0.1.0-linux-amd64.tar.gz && cd gorun-0.1.0-linux-amd64
GORUN_ENV=dev GORUN_DATA=./data GORUN_BASE_URL=http://127.0.0.1:8080 ./gorun serve
```

**من المصدر:** يحتاج البناء من المصدر إلى **Go 1.27** و`make` (يثبّت المستودع إصدار الأدوات Go 1.27.2).

```sh
git clone https://github.com/zadstack/gorun.git
cd gorun
make build

GORUN_ENV=dev \
GORUN_DATA=./data \
GORUN_THEMES=./themes \
GORUN_ADDR=127.0.0.1:8080 \
GORUN_BASE_URL=http://127.0.0.1:8080 \
./bin/gorun serve
```

افتح **http://127.0.0.1:8080/admin/setup** لإنشاء المدير الأول، ثم استخدم **http://127.0.0.1:8080/admin**. شغّل البرنامج من جذر المستودع ليجد مجلد `themes/`.

في الإنتاج، اضبط مجلد بيانات دائمًا، وHTTPS، وعنوان الموقع العام (`GORUN_BASE_URL`)، ومفتاح تشفير ثابتًا، و`setup_token` لحماية إنشاء المدير الأول، مع `GORUN_ENV=prod`، وشغّل عملية خادم واحدة لكل مجلد بيانات؛ يفرض النظام ذلك بقفل `gorun.lock` داخل مجلد البيانات، فترفض نسخة ثانية من `gorun serve` (أو `gorun mcp --jobs`) التشغيل برسالة واضحة. خلف وكيل عكسي، أضف عنوانه إلى `GORUN_TRUSTED_PROXIES` (لا يثق النظام بترويسات التمرير افتراضيًا). تحمل استجابات HTTPS ترويسة HSTS لمدة سنة، وخيارا النطاقات الفرعية والـpreload اختياريان. ينسخ `gorun backup` قاعدة البيانات والوسائط إلى `<data>/backups/`. للاستعادة افحص النسخة ثم استعدها من **تشغيل النظام ← النسخ الاحتياطية ← فحص / استعادة** أثناء التشغيل، أو بالأمر `gorun backup-restore` والخدمة متوقفة؛ في الحالتين تُكتب النسخة في مسار مجلد جديد (يجب ألا يكون موجودًا) ولا تُدمج في المجلد الحي. تُضبط الإعدادات في `gorun.toml` أو عبر متغيرات `GORUN_*`، ولا تحفظ كلمات المرور أو مفاتيح المزودين في المستودع. راجع [دليل التثبيت](docs/GORUN-INSTALLATION.md) و[دليل الإنتاج](docs/PRODUCTION.md) و[دليل Docker / Coolify](deploy/coolify.md).

### تحت الغطاء

- **ملف تنفيذي واحد بلغة Go.** لوحة التحكم مضمّنة فيه، وقوالب المواقع تأتي معه في مجلد `themes/`.
- **SQLite.** لا حاجة إلى خادم قاعدة بيانات منفصل.
- **بلا Node.js أو PHP وقت التشغيل.** يُستخدم Node.js فقط لاختبارات المتصفح الاختيارية في `e2e/`.
- **CSS مبني مسبقًا.** يأتي القالب الافتراضي بملف Tailwind مبني ومحفوظ في المستودع (نحو 12 KB، و3 KB مضغوطًا) بدل مترجم يعمل في المتصفح؛ يعيد `make css` بناءه بأداة Tailwind المستقلة دون npm.
- **قابل للبناء عليه.** `cmd/gorun` غلاف رفيع حول الحزمة `pkg/cli`، ويستطيع برنامجك الخاص إضافة وحداته وأوامره عبر `cli.Option`. راجع [البناء فوق النواة](docs/ARCHITECTURE.md#building-on-the-core--البناء-فوق-النواة).

```sh
make build && make test && make lint
```

### التوثيق

| ابدأ من هنا | البناء والتكامل | التشغيل والمساهمة |
| --- | --- | --- |
| [التثبيت](docs/GORUN-INSTALLATION.md) | [البنية](docs/ARCHITECTURE.md) | [تشغيل الإنتاج](docs/PRODUCTION.md) |
| [مساحة اللوحات](docs/CANVASES.md) | [واجهة REST](docs/API.md) | [Docker / Coolify](deploy/coolify.md) |
| [منشئ المواقع](docs/SITE_BUILDER.md) | [MCP](docs/MCP.md) | [الأمان](SECURITY.md) |
| [القوالب](docs/THEMES.md) | [الإضافات](docs/EXTENSIONS.md) | [المساهمة](CONTRIBUTING.md) |
| [الترحيل من ووردبريس](docs/MIGRATION.md) | [التجارة](docs/COMMERCE.md) | [سجل التغييرات](CHANGELOG.md) |

### حالة المشروع

GoRun في الإصدار [v0.1.0](https://github.com/zadstack/gorun/releases/tag/v0.1.0) وما زال قيد التطوير النشط؛ استخدم آخر [إصدار](https://github.com/zadstack/gorun/releases) أو ابنِه من الفرع `main`. الحدود الحالية:

- عملية خادم واحدة لكل مجلد بيانات SQLite (يفرضها القفل `<data>/gorun.lock`)، ولا يوجد تعدد مواقع بقاعدة بيانات مشتركة.
- اللوحات خاصة بصاحبها مع صلاحيات صريحة للوكلاء، وليست مساحة عمل مشتركة لعدة مستخدمين.
- يحتفظ منشئ الصفحات بالمسودة الحالية وآخر تخطيط منشور، لا بسجل كامل للتخطيطات.
- لا يوجد محدّث تلقائي للنواة، ولا توليد موقع كامل من تعليمة واحدة.
- تُستعاد النسخ الاحتياطية إلى مسار مجلد بيانات جديد فقط (يجب ألا يكون موجودًا) (`gorun backup-restore` أو **تشغيل النظام ← النسخ الاحتياطية ← فحص / استعادة**)، ويحتاج الانتقال إليها إلى الأسرار التي يحتفظ بها المشغّل وإيقاف العملية الحالية. ويجب التحقق من التوليد الفعلي لدى المزودين ومن المدفوعات الحية في كل نشر.

### المساهمة

نرحّب ببلاغات الأخطاء ومشكلات التوثيق وأفكار الميزات عبر [Issues](https://github.com/zadstack/gorun/issues)، وبطلبات الدمج. وقّع كل commit بـ`git commit -s` وفق [DCO](https://developercertificate.org/) (دون CLA)، ويتحقق CI من التوقيع؛ التفاصيل في [CONTRIBUTING.md](CONTRIBUTING.md). أبلغ عن الثغرات الأمنية بشكل خاص كما في [SECURITY.md](SECURITY.md).

### الترخيص

GoRun برنامج حر بترخيص **[GNU Affero General Public License v3.0](LICENSE)**. إذا شغّلت نسخة معدّلة منه كخدمة عبر الشبكة، فإن ترخيص AGPL يُلزمك بإتاحة الشيفرة المصدرية المقابلة لمستخدميها.

تحتفظ مكونات الطرف الثالث بتراخيصها الخاصة (راجع `web/admin/static/vendor/*.LICENSE` و`web/admin/static/fonts/zad/OFL.txt` و`internal/builder/static/THREE-LICENSE.txt`). راجع [NOTICE](NOTICE) للإسناد.

## English

**A CMS where your team and AI agents work together, and people keep the final say.**

GoRun brings team workflows, agents with scoped access over REST and MCP, and canvases that connect content, instructions and results into one bilingual (English/Arabic, RTL) admin. It is self-hosted, open source, and licensed under the [GNU AGPL v3](LICENSE).

[Team](#for-your-team) · [Agents](#for-your-agents) · [Canvases](#canvases) · [Quick start](#quick-start) · [Docs](#documentation)

Screenshots come from an isolated local instance with fictional demo content (Nakhla Journal), not customer data or a connected provider.

## For your team

- **Roles that match how editors work.** Seeded `admin`, `editor`, `author` and `contributor` roles with explicit permissions.
- **Review before it goes live.** Drafts, pending review, scheduled publishing, revisions with restore, and trash.
- **A record of who did what.** An append-only audit log covers people and keys, including every MCP tool call.
- **Structured content.** Posts, pages, custom types and fields, taxonomies, menus and media in one admin.

<table>
  <tr>
    <td width="50%"><img src="assets/branding/screenshots/content-list-ar.png" alt="The GoRun posts list in Arabic with a right-to-left layout, showing published posts and drafts in Arabic and English" /></td>
    <td width="50%"><img src="assets/branding/screenshots/editor-en.png" alt="The GoRun post editor in English with body, excerpt, featured image and the publish panel" /></td>
  </tr>
  <tr>
    <td><strong>Content</strong><br />A native Arabic interface for bilingual content.</td>
    <td><strong>Editor</strong><br />Drafts, scheduling and featured images; revisions sit in the sidebar.</td>
  </tr>
</table>

## For your agents

1. Create an **AI agent** key in **AI Agents → API keys** with only the scopes it needs.
2. Point your MCP client at `https://your-site.example/mcp` (or run `gorun mcp` locally over stdio).
3. Review sensitive requests in **AI Agents → Approvals**; the agent polls `check_approval` for the result.

- **Bounded by design.** A key never does more than its owner's role allows, and only what its scopes list.
- **People approve the risky parts.** Publishing, deleting, changing settings and editing live content wait for a human by default. Agent keys cannot use REST to approve their own calls, and self-approval is a separate scope that `*` does not grant.
- **REST for your other integrations.** Scripts and services use separate REST API keys against `/api/v1` (described by `/api/v1/openapi.json`); agent keys are refused there and stay on MCP. Webhooks notify other services of changes.

```text
Inspect the site's structure, content types, and the tools available to your key.
Explain your proposed changes before writing anything.
Create new content as drafts and preserve existing content and settings.
If a tool requests approval, report its request ID and wait for the decision.
```

[MCP and permissions](docs/MCP.md) · [REST API](docs/API.md) · [Agent models](docs/AGENT-MODELS.md)

<table>
  <tr>
    <td width="50%"><img src="assets/branding/screenshots/agent-keys-en.png" alt="The GoRun API keys page in English: agent keys and a REST key, each with its own scopes such as content.view and canvas.run" /></td>
    <td width="50%"><img src="assets/branding/screenshots/approvals-ar.png" alt="The GoRun agent approvals queue in Arabic: two publish requests from the editorial agent awaiting a decision, and one earlier approval" /></td>
  </tr>
  <tr>
    <td><strong>Agent keys</strong><br />Scopes per key; REST kept apart from MCP.</td>
    <td><strong>Approvals</strong><br />Sensitive agent calls wait for a person.</td>
  </tr>
</table>

## Canvases

Canvases (**Dashboard → Canvases**) are an operational workspace, not a page designer.

![Selected content and a prompt go to a connected agent; the result becomes a new private note for your review](assets/branding/reviewed-workflow.svg)

- **Gather the work.** Notes, checklists, links, article and page references, and widgets for approvals, jobs and service requests.
- **Run agents on context you choose.** Connect sources and a prompt to a configured text provider or a named agent key, review the exact context, then run it.
- **Results arrive as new notes.** Each result is appended as a private note with provenance; it never overwrites your cards or publishes content.
- **Recurring, but reviewed.** Hourly, daily or weekly schedules start disabled, need explicit activation, and recheck permissions on every run.
- **Explicit agent grants.** You decide which nodes an agent can read, append to or run on each canvas.

<img src="assets/branding/screenshots/canvas-en.png" alt="A GoRun canvas in English: an article reference and a brief connected to a named research agent, an editor note, and the agent's result appended as a new note with provenance" />

[Canvas workspace](docs/CANVASES.md) · [Canvas operations](docs/CANVAS-OPERATIONS-CONTRACT.md) · [Content studio](docs/CONTENT_STUDIO.md)

## Also included

| Area | What you get |
| --- | --- |
| Site builder and themes | Starter sites, a section-based page builder (12 section types, undo/redo, autosave, previews), five themes, child themes and template overrides |
| WordPress migration | Guided REST or WXR import with mapping, dry-run preview and validation; plugins, page-builder layouts, comments and WooCommerce orders are not transferred |
| Content studio | Reviewable text and media candidates from explicitly configured AI or MCP providers |
| Operations | Persistent jobs, health checks, backups, redirects, SEO metadata, sitemaps and S3-compatible media storage |
| Commerce | Virtual products, Stripe Checkout, and entitlements for MCP access |

Provider-dependent features need valid credentials. Saving a connection or loading its model list does not prove that generation or payment works.

[Site builder](docs/SITE_BUILDER.md) · [Themes](docs/THEMES.md) · [Migration](docs/MIGRATION.md) · [Commerce](docs/COMMERCE.md)

<table>
  <tr>
    <td width="40%"><img src="assets/branding/screenshots/builder-ar.png" alt="The GoRun page builder in Arabic: the hero section editor beside a live preview of the page" /></td>
    <td width="40%"><img src="assets/branding/screenshots/theme-home-en.png" alt="The default theme home page in English with a featured post and its cover image" /></td>
    <td width="20%"><img src="assets/branding/screenshots/theme-post-ar-mobile.png" alt="An Arabic post in the default theme on a phone screen" /></td>
  </tr>
  <tr>
    <td><strong>Page builder</strong><br />Sections, live preview, explicit publishing.</td>
    <td><strong>Default theme</strong><br />Precompiled CSS, no third-party scripts.</td>
    <td><strong>Mobile</strong><br />Full RTL.</td>
  </tr>
</table>

## Quick start

**Container image:** releases publish a multi-arch image (linux/amd64, linux/arm64) to GHCR, tagged `0.1.0`, `0.1` and `latest`:

```sh
docker run -d --name gorun -p 8080:8080 -v gorun-data:/data \
  -e GORUN_BASE_URL=https://cms.example.com \
  -e GORUN_SECRET_KEY="<openssl rand -base64 32>" \
  -e GORUN_SETUP_TOKEN="<openssl rand -hex 24>" \
  ghcr.io/zadstack/gorun:0.1.0
```

The image runs in production mode (`GORUN_ENV=prod`) and keeps its data in `/data`. Keep `GORUN_SECRET_KEY` stable: it signs sessions and encrypts stored secrets. Enter `GORUN_SETUP_TOKEN` at `/admin/setup` to create the first administrator, then remove it. The Compose file and Coolify steps are in the [Docker / Coolify guide](deploy/coolify.md).

**Binaries:** every [GitHub release](https://github.com/zadstack/gorun/releases) attaches linux and darwin archives for amd64 and arm64, containing the binary, `themes/` and the docs, plus a `SHA256SUMS` file:

```sh
curl -fLO https://github.com/zadstack/gorun/releases/download/v0.1.0/gorun-0.1.0-linux-amd64.tar.gz
curl -fLO https://github.com/zadstack/gorun/releases/download/v0.1.0/SHA256SUMS-0.1.0.txt
sha256sum --ignore-missing -c SHA256SUMS-0.1.0.txt
tar xzf gorun-0.1.0-linux-amd64.tar.gz && cd gorun-0.1.0-linux-amd64
GORUN_ENV=dev GORUN_DATA=./data GORUN_BASE_URL=http://127.0.0.1:8080 ./gorun serve
```

**From source:** building from source requires **Go 1.27** and `make` (the repository pins the Go 1.27.2 toolchain).

```sh
git clone https://github.com/zadstack/gorun.git
cd gorun
make build

GORUN_ENV=dev \
GORUN_DATA=./data \
GORUN_THEMES=./themes \
GORUN_ADDR=127.0.0.1:8080 \
GORUN_BASE_URL=http://127.0.0.1:8080 \
./bin/gorun serve
```

Open **http://127.0.0.1:8080/admin/setup** to create the first administrator, then use **http://127.0.0.1:8080/admin**. Run from the repository root so the server finds `themes/`.

For production, set a persistent data directory, HTTPS, the public base URL, a persistent encryption key and a setup token, and run one server process per data directory; GoRun enforces this with an OS lock on `<data>/gorun.lock`, and a second `gorun serve` (or `gorun mcp --jobs`) on the same directory refuses to start. Behind a reverse proxy, list it in `GORUN_TRUSTED_PROXIES` (no forwarded headers are trusted by default). HTTPS responses carry a one-year HSTS header; subdomains and preload are opt-in. `gorun backup` snapshots the database and media into `<data>/backups/`. To restore, inspect the pair and restore it from **Operations → Backups → Inspect / restore** while the site runs, or with `gorun backup-restore` while it is stopped; either way the copy lands in a new directory path that must not already exist and never merges into the live one. See the [installation guide](docs/GORUN-INSTALLATION.md), [production guide](docs/PRODUCTION.md) and [Docker / Coolify guide](deploy/coolify.md).

## Under the hood

- **One Go binary.** The admin is embedded; website themes ship alongside in `themes/`.
- **SQLite.** No separate database service to run.
- **No Node.js or PHP at runtime.** Node.js is only used by the optional browser tests in `e2e/`.
- **Precompiled theme CSS.** The default theme ships a committed Tailwind stylesheet (about 12 KB, 3 KB gzipped) instead of an in-browser compiler; `make css` rebuilds it with the standalone Tailwind CLI, no npm required.
- **Built to extend.** `cmd/gorun` is a thin wrapper around `pkg/cli`; your own binary can add modules and commands with `cli.Option`s. See [Building on the core](docs/ARCHITECTURE.md#building-on-the-core--البناء-فوق-النواة).

```sh
make build && make test && make lint
```

## Documentation

| Start here | Build and integrate | Operate and contribute |
| --- | --- | --- |
| [Installation](docs/GORUN-INSTALLATION.md) | [Architecture](docs/ARCHITECTURE.md) | [Production operations](docs/PRODUCTION.md) |
| [Canvas workspace](docs/CANVASES.md) | [REST API](docs/API.md) | [Docker / Coolify](deploy/coolify.md) |
| [Site builder](docs/SITE_BUILDER.md) | [MCP](docs/MCP.md) | [Security](SECURITY.md) |
| [Themes](docs/THEMES.md) | [Extensions](docs/EXTENSIONS.md) | [Contributing](CONTRIBUTING.md) |
| [WordPress migration](docs/MIGRATION.md) | [Commerce](docs/COMMERCE.md) | [Changelog](CHANGELOG.md) |

## Project status

GoRun is an actively developed [v0.1.0](https://github.com/zadstack/gorun/releases/tag/v0.1.0); use the latest [release](https://github.com/zadstack/gorun/releases) or build from `main`. Current boundaries:

- One server process per SQLite data directory (enforced by `<data>/gorun.lock`); no shared-database multisite.
- Canvases are private to their owner, with explicit agent grants; they are not a shared multiplayer workspace.
- The page builder keeps the current draft and last published layout, not a full layout history.
- No automatic core updater, and no complete site generation from a single prompt.
- Backup restore writes only into a new data directory path that must not already exist (`gorun backup-restore` or **Operations → Backups → Inspect / restore**); cutover still needs operator-held secrets and a process stop. Real provider generation and live payments must be verified per deployment.

## Contributing

Bug reports, documentation problems and feature ideas are welcome as [issues](https://github.com/zadstack/gorun/issues), and pull requests are welcome too. Sign off every commit with `git commit -s` under the [Developer Certificate of Origin](https://developercertificate.org/) (no CLA); CI checks the sign-off. See [CONTRIBUTING.md](CONTRIBUTING.md). Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).

## License

GoRun is free software under the **[GNU Affero General Public License v3.0](LICENSE)**. If you run a modified version as a network service, the AGPL requires you to offer its users the corresponding source code.

Third-party components keep their own licenses (see `web/admin/static/vendor/*.LICENSE`, `web/admin/static/fonts/zad/OFL.txt` and `internal/builder/static/THREE-LICENSE.txt`). See [NOTICE](NOTICE) for attribution.

Copyright © 2026 ZAD STACK / Ahmed Morsy.
