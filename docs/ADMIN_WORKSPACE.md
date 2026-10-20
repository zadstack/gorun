# Admin workspace

The admin is bilingual (**Arabic** and **English**) with full RTL when Arabic is active. Use this page for day-to-day layout; first public site steps are in [SITE_BUILDER.md](SITE_BUILDER.md), themes in [THEMES.md](THEMES.md), agents in [MCP.md](MCP.md).

## Bilingual admin (AR / EN)

- **UI language:** the top bar has a locale switch (`POST /admin/locale`) that toggles Arabic ⇄ English for the signed-in session. Arabic layouts are RTL.
- **Account default:** **Users → edit user → Locale** sets that person's preferred admin locale.
- **Site locale (public):** **Settings → General → Locale** (`site.locale`) chooses the default public language; other supported locales are served under `/<locale>/…`.
- **Content language:** new posts and pages default to the signed-in admin UI language (not `site.locale`). The editor shows an explicit **Content language** picker on create; use **Translations** on an existing item to add another locale.
- Admin strings ship in both catalogs; if a key is missing in one language the other is used as fallback.

Switching the admin UI language does not change the public `site.locale` or rewrite published URLs by itself. It does change the default content language for new items until you pick otherwise in the editor.

## Dashboard

The dashboard separates everyday work from diagnostics. It opens with a short guide, integration setup status and quick actions, followed by your pinned tools, recent content, audit activity, jobs, system information and your saved canvases. Additional module widgets continue to work.

Open **Customize** before arranging dashboard sections by dragging or with their arrow buttons; the order is saved for your account, and **Restore default order** returns to the default layout.

The recent-content table shows the most recently updated items in public content types, including unpublished items the signed-in user can access. Authors and contributors see only their own items. Private intake reports never enter this table. Titles open the existing content editor; the dashboard never publishes or saves content.

Integration setup status reads local configuration only: opening the dashboard does not test any provider connection.

## Pin tools to the dashboard

Use **Add to quick access** beside an authorized contextual page tab (with touch, mouse or keyboard). The item becomes a personal dashboard panel and a main-sidebar **Quick access** shortcut immediately; its contextual page tab is hidden on the next page navigation or reload. Use **Remove from quick access** beside the sidebar shortcut, or **Return to navigation** on its dashboard panel, to unpin it. The sidebar and an on-page dashboard widget refresh immediately; the contextual tab returns on the next page navigation or reload. Pins and their order survive reload and are private to your account. Quick access rechecks current permissions and workspace visibility; hidden or unavailable destinations are not exposed. An old `builder.identity` pin projects to the authorized `site-builder` entry and duplicates are combined without rewriting saved preferences. Pinning never changes roles, calls a provider or publishes content, and panels recheck your current permissions.

See [Personal quick access](SIDEBAR-QUICK-ACCESS.md) for saved-state refresh, legacy pin compatibility and permission filtering.

The canvas gallery shows only your own saved canvases, with a few safe content previews per board.

## Sections and contextual tabs

The primary sidebar has up to six entries: **Home**, **Content**, **Website**, **Canvas & agents**, **Inbox** and **Settings**. **Store** appears only when a permitted commerce entry is available. Empty sections are omitted. Content includes content types and taxonomies; Website combines appearance, menus, SEO and redirects. Settings contains general settings, team/roles, integrations and system tools. Unknown extension groups remain accessible under Settings.

Select a section to see its page tabs above the current screen. Larger sections have a category row (for example **Settings → Team & roles → Users**). These are ordinary links and work without JavaScript. Nested editors keep their parent section selected. Existing URLs and route permissions are unchanged.

## Arrange the sidebar

Open **Customize sidebar**, which remains available even when every other link is hidden. Move groups and items with their up/down buttons, choose visible links and save. The order and visibility are saved for your own user account. Keyboard controls are supported. **Restore defaults** clears both order and visibility preferences.

Ordering and hiding never grant or revoke permissions. New permitted module links remain available, and removed links do not break the saved order. Other team members keep their own preferences.

## العربية

لوحة الإدارة ثنائية اللغة (عربية وإنجليزية) مع RTL كامل عند اختيار العربية. بدّل لغة الواجهة من زر اللغة في الشريط العلوي. لغة الحساب الافتراضية من **المستخدمون ← تعديل ← اللغة**. لغة الموقع العامة من **الإعدادات ← عام** ولا تتغير تلقائيًا عند تبديل لغة الواجهة.

الرئيسية تبدأ بدليل قصير وحالة إعداد التكاملات والإجراءات السريعة، ثم أدواتك المثبّتة وآخر المحتوى والنشاط والمهام والنظام ولوحاتك المحفوظة. افتح «تخصيص» ثم رتّب الأقسام بالسحب أو أزرار الأسهم، ويُحفظ الترتيب لحسابك. الكاتب والمساهم يشاهدان محتواهما فقط، ولا تدخل البلاغات الخاصة في الجدول. حالة الإعداد تقرأ الإعدادات المحلية فقط ولا تختبر أي مزوّد.

استخدم زر «إضافة إلى الوصول السريع» بجانب تبويب الصفحة داخل قسمها؛ يظهر الاختصار في القائمة الرئيسية واللوحة الشخصية فورًا، ويختفي تبويب الصفحة عند الانتقال التالي أو إعادة التحميل. لإلغاء التثبيت استخدم «إزالة من الوصول السريع» بجانب اختصار القائمة، أو «إرجاع إلى التنقل» في لوحته داخل الرئيسية. تتحدث القائمة واللوحة الموجودة في الصفحة فورًا، ويعود تبويب الصفحة عند الانتقال التالي أو إعادة التحميل. التثبيت لا يغيّر الصلاحيات ولا ينشر أي محتوى.

القائمة الرئيسية تجمع الصفحات في ستة أقسام كحد أقصى: الرئيسية، المحتوى، الموقع، الكانفس والوكلاء، الوارد والإعدادات. يظهر المتجر عند توفر أدوات تجارة مسموح بها. اختر القسم ثم التبويب المطلوب؛ الإعدادات تجمع العام والفريق والتكاملات والنظام. تختفي الأقسام الفارغة، وتبقى روابط الإضافات الجديدة متاحة داخل الإعدادات.

من «تخصيص القائمة» يمكنك تحريك المجموعات والعناصر بأزرار لأعلى ولأسفل، وإظهارها أو إخفاؤها، ثم حفظ الترتيب لحسابك فقط. «استعادة الافتراضي» تعيد الترتيب والظهور الأصليين. الصلاحيات لا تتغير.

أول موقع عام: [SITE_BUILDER.md](SITE_BUILDER.md). تبديل القالب: **المظهر ← القوالب**، والتفاصيل في [THEMES.md](THEMES.md).

## Site design

**Website → Design → Site design** shares a saved-site summary and two page tabs: Build site and Visual identity. Creating drafts, activating a prepared site and saving identity remain separate explicit actions. The local identity sample never saves itself. The Build tab requires all existing builder permissions; settings-only users can still open identity. See [the workspace guide](SITE-DESIGN-WORKSPACE.md).
