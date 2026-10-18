# Migrating from WordPress / الترحيل من ووردبريس

GoRun ships a WordPress migrator (admin → **System → Migrations**, permission `migrations.run`).
It reads a WordPress site through its REST API, or from a WXR export file, and imports it with a
7-step wizard: **Connect → Scan → Map types → Map fields → Preview → Import → Validate**.
The same steps are available as REST operations under `/api/v1/migrations*` and as the
`wpmigrate.Service` used by the MCP agent.

يأتي GoRun بأداة ترحيل من ووردبريس (لوحة التحكم ← **النظام ← الترحيل**، الصلاحية `migrations.run`).
تقرأ الموقع عبر واجهة REST أو من ملف تصدير WXR، وتستورده عبر معالج من 7 خطوات:
**الاتصال ← الفحص ← ربط الأنواع ← ربط الحقول ← المعاينة ← الاستيراد ← التحقق**.
الخطوات نفسها متاحة عبر واجهة `/api/v1/migrations*` وعبر `wpmigrate.Service` الذي يستخدمه وكيل MCP.

---

## What moves / ما الذي ينتقل

| WordPress | GoRun | Notes |
|---|---|---|
| Posts, pages, other public post types | Content (`post`, `page`, or a new type with the same slug) | Pages keep their hierarchy; original publish dates are kept. |
| Gutenberg blocks | Clean HTML | Core blocks are converted (paragraph, heading, list, quote, image, gallery, columns, group, buttons, separator, spacer, table, code, preformatted, details, embed, html, cover → figure, media-text). Unknown blocks keep their inner HTML and are logged. Raw block markup needs an application password; otherwise the rendered HTML is cleaned of `wp-block-*`, `has-*`, `is-*` classes and inline styles. |
| Categories and tags | Taxonomy terms (matched by slug) | Arabic slugs are decoded (`%d8%a7…` → `ا…`). |
| Media library | Media (downloaded, deduplicated by source URL) | Alt, caption and title kept. Every size URL (`-300x200`, `-scaled`, `srcset`) is rewritten to the local file. |
| Authors | Users with the `author` role | Matched by email; existing users are reused. Passwords never move: authors sign in with a magic link. Emails need an application password (or WXR). |
| Yoast SEO (`yoast_head_json`) | SEO meta | Title, description, canonical, robots (`noindex` → `noindex,follow`), Open Graph, Twitter card, schema. |
| Menus | Menus | Need an application password, or come from WXR. |
| WooCommerce products | Commerce catalog (via the commerce module's importer) | Name, slug, descriptions, price (minor units), currency, categories → group, images, MCBB plan meta (`_mcbb_server`, `_mcbb_plan`, `_mcbb_months`). Without the commerce module products are reported as "needs commerce module" and skipped. |
| Changed permalinks | 301 redirects (enabled) | e.g. `/2026/08/31/slug/` → `/post/slug`; suffixed duplicate slugs too. A redirect is never created over a path an existing GoRun item already serves, nor from `/`. |
| Internal links | Relative paths | `https://old.site/<path>` → the new permalink, or `/<path>`. |
| Front page setting | `site.front_page` | The WordPress static front page becomes the GoRun front page. |

## What does not move / ما الذي لا ينتقل

- PHP page templates, theme settings, widgets, page-builder layouts (Elementor, Divi, Beaver) — rebuild them as theme templates/components. / قوالب PHP وإعدادات الثيم والودجات وتخطيطات منشئي الصفحات — أعد بناءها كقوالب ومكوّنات في الثيم.
- Shortcodes are **left in place** and listed (with counts) in the validation report. / تُترك الأكواد المختصرة كما هي وتُسرد بأعدادها في تقرير التحقق.
- Comments (v1), user passwords, plugin settings. / التعليقات (الإصدار الأول) وكلمات المرور وإعدادات الإضافات.
- WooCommerce orders and customers — customers start fresh. / طلبات وعملاء WooCommerce — يبدأ العملاء من جديد.
- Plugin-rendered pages (cart, checkout, my-account, shop) are skipped: GoRun commerce provides them. / صفحات السلة والدفع والحساب والمتجر تُتخطى لأن متجر GoRun يوفرها.

### REST status coverage / الحالات التي يغطيها ترحيل REST

- With credentials, REST migration requests published, draft, pending, scheduled and private records of supported post, page and custom post type collections; anonymous requests get published content only. Trash, auto-drafts and custom statuses are excluded, and discovery says so. / مع بيانات الدخول يجلب الترحيل المنشور والمسودات والمعلّق والمجدول والخاص؛ وبدونها المنشور فقط. سلة المهملات والمسودات التلقائية والحالات المخصصة مستثناة، ويذكر الاستكشاف ذلك.
- Draft or private WooCommerce products are not migrated: the catalog adapter reads the published Store API. / منتجات WooCommerce غير المنشورة لا تُرحَّل.
- Discovery reads every accessible page of each collection; a collection above 500 pages, or with invalid page bounds, fails instead of succeeding partially. Media discovery is a labelled five-page sample. / يقرأ الاستكشاف كل الصفحات المتاحة، ويفشل صراحةً فوق 500 صفحة، وعيّنة الوسائط خمس صفحات.
- A 401 is always fatal. A denied profile request is reported as `auth_unavailable`, not as a wrong password, and there is no anonymous fallback. / خطأ 401 يوقف الترحيل دائمًا، ولا يُعاد المحاولة دون دخول.
- Older discovery reports keep their original meaning; rescan before treating them as current coverage. / أعد الاستكشاف قبل الاعتماد على تقرير قديم.
- Migrating content does not make media URLs private. / ترحيل المحتوى لا يجعل روابط الوسائط خاصة.

## Authentication / المصادقة

Without credentials the migrator reads only the public REST API. Create an **application password**
in WordPress (Users → Profile → Application Passwords) and enter it with the username in step 1 to unlock:
raw block markup (`context=edit`), authors' emails, menus, drafts and WooCommerce `wc/v3` product meta.
The password is encrypted at rest (AES-256-GCM with the instance secret key) and is never shown again.

For REST content, edit context alone does not include drafts. This candidate requests the explicit
states `publish,draft,pending,future,private` in discovery and every selected import page. It preserves
draft/pending/private visibility and maps `future` to `scheduled` with its UTC date. Anonymous content
reads request `publish` only. Trash, auto-drafts, custom states and internal WordPress types are
excluded; attachments use their separate media path. An unexpected state in the REST content payload
is refused, not converted to published content. This contract applies to supported REST-enabled
content types; it does not prove WooCommerce draft coverage or arbitrary plugin semantics.

تطلب هذه النسخة المرشحة حالات `publish,draft,pending,future,private` صراحةً في الفحص وكل صفحة
استيراد؛ `context=edit` وحده لا يجلب المسودات. تُحفظ المسودة وقيد المراجعة والخاص، ويتحول
`future` إلى `scheduled` بتاريخ UTC. القراءة المجهولة تطلب المنشور فقط. المحذوف والمسودات
التلقائية والحالات المخصصة والأنواع الداخلية مستبعدة، وللمرفقات مسار وسائط مستقل. تُرفض
الحالة التي يعيدها REST خارج النطاق، ولا تتحول إلى محتوى منشور. هذا لا يثبت نقل كل سلوك إضافات ووردبريس.

If the credentials are wrong the scan stops with **auth_failed** — it never silently falls back to an
anonymous scan. The Discovery Report shows how the site was read (anonymous / application password / WXR)
and puts everything that needs authentication in the amber "Partly available" section.

بدون بيانات دخول تقرأ الأداة الواجهة العامة فقط. أنشئ **كلمة مرور تطبيق** في ووردبريس وأدخلها مع اسم المستخدم
في الخطوة الأولى لقراءة الكتل الخام وبريد الكتّاب والقوائم والمسودات وحقول منتجات WooCommerce. تُحفظ مشفرة
ولا تُعرض مرة أخرى. إن كانت البيانات خاطئة يتوقف الفحص بخطأ **auth_failed** ولا يتحول إلى فحص مجهول بصمت.

## Request rate / معدل الطلبات

Every request to a source (REST reads, retries included, and media downloads) waits on one token bucket per
source, shared by both job workers: 4 requests per second by default. Change it per migration with the
**Requests per second** field in step 1 (1–20; API: `options.rate_per_second`, clamped to 1–20), or for every
migration with the setting `wpmigrate.rate_per_second`. The rate of a source changes only when a run starts
(import or preview); a scan never retunes a running import. A full import of the test fixture site (about 170 requests) takes about 45 s at 4/s.

كل طلب إلى المصدر (قراءات REST وإعاداتها وتنزيل الوسائط) ينتظر دوره في «دلو» واحد لكل مصدر يتشاركه عاملا
المهام: 4 طلبات في الثانية افتراضيًا. غيّره لكل ترحيل من حقل «الطلبات في الثانية» في الخطوة الأولى (أو
`options.rate_per_second` في الواجهة البرمجية)، أو لكل الترحيلات بالإعداد `wpmigrate.rate_per_second`.

## The Discovery Report / تقرير الاستكشاف

Three colour-coded buckets with a plain explanation per line:

- **Will be migrated** (green): content types and counts, taxonomies, media, settings, Yoast, products…
- **Partly available** (amber): data hidden from the public API (raw blocks, menus, authors' emails, product meta),
  ACF fields not exposed to REST, media offloaded to another host.
- **Not migrated** (grey): custom page templates (with the page slugs), shortcodes (with counts), page builders,
  plugin pages, theme settings, comments, passwords, orders.

`content_state_counts` groups readable content by WordPress type and source state. Unavailable types
have unknown counts and are absent from this map. `collections` records `visible`, `reported_total`,
`reported_pages`, `pages_read`, `count_known`, `pages_known` and `limited`. WordPress can advertise
header totals larger than the rows this account may read; these totals are source bounds, not proof
of inaccessible content coverage. Unbounded collection reads stop with an error at the 500-page
limit. Discovery now reads every accessible content page, so it can make more requests than older
header-only scans. Credential revocation and cancellation abort the scan. Other collection failures
are explicit unavailable findings; selected content failures abort an import.

تعرض `content_state_counts` أعداد المحتوى القابل للقراءة حسب النوع وحالة المصدر. أعداد الأنواع
غير المتاحة مجهولة وليست صفرًا مثبتًا. حدود رؤوس WordPress قد تتجاوز الصفوف التي تسمح بها
صلاحيات الحساب؛ لا تثبت قراءة المحتوى المحجوب. يقرأ الفحص كل صفحات المحتوى المتاحة، بحد 500
صفحة يوقف القراءة بخطأ عند تجاوزه. فشل المصادقة أو الإلغاء يوقف الفحص؛ فشل مجموعة أخرى
يُعرض كغير متاح، وفشل المحتوى المحدد يوقف الاستيراد.

## Preview, import, resume / المعاينة والاستيراد والاستئناف

- **Preview** is a dry run of the whole pipeline: it records what would happen (create/update/reuse per kind,
  slug conflicts, shortcodes, unknown blocks, files and bytes to download) and writes no content, media, terms,
  users, menus or redirects.
  Its `content_state_counts` describes selected source rows, including planned skips; operation counts
  describe planned actions separately. Neither is a transferred or validated total. The per-row
  state plan shows source/local states, source UTC date and native `publish_at` (empty for draft/pending).
- **Import** runs as background jobs, item by item, in this order: authors → terms → media → content
  (pages by depth, then posts) → products → menus → redirects → URL rewrite → validation. Progress is live
  (polled every 2 seconds) with per-kind bars and the log tail.
- **Pause / Resume** at any time. **Retry failed** requeues only failed items (stored payloads; WordPress is not
  read again) and re-runs the link rewrite.
- **Re-running** a finished import compares the full fetched content payload, including body/state/date;
  a same-second change does not depend on a different WordPress `modified` date. New migrations of
  the same source update prior imported items by WordPress id, never by slug. Source changes replace
  imported destination fields; this is not a destination-edit conflict detector. Unchanged source
  payloads reuse prior items. Extra destination fields/terms are merged by the existing importer.
- **Slug conflicts** with GoRun content that the migration did not create follow the chosen policy:
  `suffix` (default, e.g. `page-2` plus a redirect), `skip`, or `overwrite`. Two WordPress items with the same
  slug (e.g. two `wp-mcp` pages under different parents) always get a suffix and a redirect.

## Validate / التحقق

The validation step compares counts per kind (WordPress vs imported/skipped/failed), checks that every
published item renders (HTTP 200 on its local URL; uses `GORUN_BASE_URL`, or the app in-process
for `gorun import-wxr`), lists broken internal links,
media still pointing at WordPress, unresolved shortcodes and changed slugs. The report is stored in the
migration log and can be downloaded as Markdown.

Render checks probe the public base URL (the site setting, else `GORUN_BASE_URL`). They may reach a private
or loopback address only when that URL is the configured `GORUN_BASE_URL` (`base_url`), or when
`GORUN_ALLOW_PRIVATE_NET` is set, so editing the **site base URL** setting cannot point the server at internal
hosts. / يفحص التحقق رابط الموقع العام، ولا يصل إلى عنوان خاص أو loopback إلا إذا طابق `GORUN_BASE_URL`
المضبوط في البيئة أو الإعدادات، أو عند تفعيل `GORUN_ALLOW_PRIVATE_NET`؛ فلا يكفي تعديل رابط الموقع من لوحة
الإدارة لتوجيه الخادم إلى الشبكة الداخلية.

## WXR fallback / ملف التصدير WXR

When the REST API hides data, export from WordPress (**Tools → Export → All content**) and either upload the
file in step 1 (up to 200 MB, stored under `<data>/imports/`) or run:

```
gorun import-wxr export.xml --dry-run                 # preview only, nothing imported
gorun import-wxr export.xml [--source-name "Old site"] [--currency AED] [--slug-policy suffix|skip|overwrite]
                              [--reset-options] [--data DIR]
```

The command boots GoRun without job workers (safe beside a running server), runs the whole pipeline
synchronously (`wpmigrate.ImportWXR`), checks rendering in-process, prints counts per kind and the report
link (`/admin/migrations/{id}/report.md`). It exits 0 when the import ends `validated` or `completed`,
and 1 otherwise (or on any error). The source and
migration appear in the wizard like any other. Running it again on the same file (or the same
`--source-name`, or another export of the same site) reuses that source and its latest migration, so it
updates what the first import created instead of duplicating it. The site is identified by the export's
blog URL (`wp:base_blog_url`, then the channel link; on multisite `wp:base_site_url` is the network root,
so each subsite stays its own source), ignoring `http`/`https` and host case. A re-run keeps the stored
options of that migration: `--currency` / `--slug-policy` given again are ignored, with a note in the
output and the migration log, unless `--reset-options` is passed. WXR carries postmeta — including the MCBB
product plan mapping and Yoast fields — authors with emails, terms, attachments (downloaded from
`attachment_url`) and navigation menus. Set the product currency in the upload form (WXR has none).

## Worked example: the fixture site / مثال عملي: موقع الاختبار

The test fixtures describe **Example Press**, a fictional WordPress site at `example.org`: every title, text,
author, product and media name in them is invented, and the Arabic text is placeholder wording. The anonymous
REST answers live in `internal/wpmigrate/testdata/example-press/`, and what an application password adds lives
in `internal/wpmigrate/testdata/example-press-auth-synthetic/`. `go run ./tools/wp-record -site <url>` records
another site for a local rehearsal; replace its content with fictional data before committing anything it
recorded. What the scan shows for Example Press:

- WordPress 7.1, Arabic (`ar`), Yoast SEO, WooCommerce (36 catalog products in AED), Blocksy theme; no ACF; all
  media served from example.org itself (no offload).
- **Custom templates are not migrated**: `template-platforms.php` (platforms), `template-pricing.php` (pricing) and
  `template-ep-product.php` (the six kit pages: compass-kit, garden-kit, lantern-kit, paper-kit, poster-kit,
  recipe-kit).
  Rebuild them as theme templates (`page-platforms`, `page-pricing`, …).
- **Site Kit shortcodes are not migrated**: `[ep_plans]`, `[ep_platform_cards]`, `[ep_contact_form]` stay in the
  content and are listed in the validation report; the theme renders plan cards, platform cards and the contact
  form as components fed by `products`.
- **MCBB product meta** (`_mcbb_server`, `_mcbb_plan`, `_mcbb_months`) is hidden from the public Store API: connect
  with an application password (wc/v3) or import a WXR export so products map to billing plans.
- The users directory is private (HTTP 403) without an application password: posts are then attributed to the
  person running the migration.
- Six kit slugs exist twice (under `/craft-apps/` and `/platforms/`): the second copy is imported as
  `<slug>-2` with a 301 from its old address.
- Posts move from `/YYYY/MM/DD/<slug>/` to `/post/<slug>` (Arabic slugs stay Arabic) with 301 redirects.
- Cart, checkout, my-account and shop pages are skipped (GoRun commerce replaces them).

- قوالب الصفحات المخصصة (المنصات والأسعار وصفحات الأدوات الست) **لا تنتقل** — تُبنى كقوالب في الثيم.
- أكواد Site Kit المختصرة `[ep_plans]` و`[ep_platform_cards]` و`[ep_contact_form]` **لا تُنفذ** — تبقى في المحتوى
  وتظهر في تقرير التحقق، ويعرضها الثيم كمكوّنات تعتمد على المنتجات.
- حقول MCBB للمنتجات مخفية عن الواجهة العامة: استخدم كلمة مرور تطبيق أو ملف WXR لربط المنتجات بالباقات.

## Validation availability

`validated` means that the validation report was generated; inspect its findings before accepting an import. `unverified_checks` records render/link checks that were unavailable or deliberately skipped, with path and stable reason codes. No HTTP response is not proof of a broken link or successful rendering. The progress view keeps polling while the report is being generated; only the final state ends polling. Database errors in migration health checks report failure.
