package wpmigrate

import (
	"fmt"
	"sort"
	"strings"
)

// Report is the Discovery Report (spec §4.7 step 2): what the source
// exposes, sorted into three buckets.
type Report struct {
	Site               SiteSummary               `json:"site"`
	Auth               AuthSummary               `json:"auth"`
	Available          []Finding                 `json:"available"`
	Partial            []Finding                 `json:"partial"`
	Unavailable        []Finding                 `json:"unavailable"`
	ContentStateCounts map[string]map[string]int `json:"content_state_counts,omitempty" doc:"Readable source rows by WordPress content type and source state. Unavailable types have unknown counts and are absent; source header totals are separate bounds."`
	Counts             map[string]int            `json:"counts"`
	Collections        map[string]CollectionRead `json:"collections,omitempty"`
	Plugins            map[string]bool           `json:"plugins"`
	Shortcodes         map[string]int            `json:"shortcodes"`
	Templates          map[string]int            `json:"templates"`
	OffloadedMediaHost string                    `json:"offloaded_media_host"`
	SourceKind         string                    `json:"source_kind"` // rest | wxr
	MetaKeys           map[string][]string       `json:"meta_keys"`   // wp type → meta keys seen in samples
	GeneratedAt        string                    `json:"generated_at"`
}

// SiteSummary identifies the source site.
type SiteSummary struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	Version string `json:"version"`
	Locale  string `json:"locale"`
}

// AuthSummary says how the source was read. Mode is anonymous or
// app_password; Error is "auth_failed" when credentials were rejected.
type AuthSummary struct {
	Mode  string `json:"mode"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// Finding is one line of a bucket. Items lists names when useful (page
// slugs with custom templates, shortcode tags).
type Finding struct {
	Kind  string            `json:"kind"`
	Label map[string]string `json:"label"`
	Count int               `json:"count"`
	Note  map[string]string `json:"note,omitempty"`
	Items []string          `json:"items,omitempty"`
	// Sampled marks counts taken from the first page (100 items) of pages
	// and posts rather than the whole site.
	Sampled bool `json:"sampled,omitempty"`
}

// Find returns the finding of kind in any bucket and its bucket name.
func (r *Report) Find(kind string) (*Finding, string) {
	for _, b := range []struct {
		name string
		list []Finding
	}{{"available", r.Available}, {"partial", r.Partial}, {"unavailable", r.Unavailable}} {
		for i := range b.list {
			if b.list[i].Kind == kind {
				return &b.list[i], b.name
			}
		}
	}
	return nil, ""
}

func (r *Report) add(bucket string, f Finding) {
	switch bucket {
	case "available":
		r.Available = append(r.Available, f)
	case "partial":
		r.Partial = append(r.Partial, f)
	default:
		r.Unavailable = append(r.Unavailable, f)
	}
}

// labels are the bilingual labels and notes of finding kinds. Kinds with a
// ":" suffix (type:post) fall back to their prefix.
var labels = map[string][2]map[string]string{
	"type":                   {{"en": "Content type", "ar": "نوع محتوى"}, nil},
	"taxonomy":               {{"en": "Taxonomy", "ar": "تصنيف"}, nil},
	"media":                  {{"en": "Media library", "ar": "مكتبة الوسائط"}, {"en": "Files are downloaded and re-linked.", "ar": "تُنزَّل الملفات ويُعاد ربطها."}},
	"users":                  {{"en": "Authors", "ar": "الكتّاب"}, {"en": "Imported as authors with a random password; they sign in with a magic link.", "ar": "يُستورَدون ككتّاب بكلمة مرور عشوائية ويدخلون برابط سحري."}},
	"users_private":          {{"en": "Authors (emails hidden)", "ar": "الكتّاب (البريد مخفي)"}, {"en": "The users list needs an application password; without it every post is attributed to you.", "ar": "قائمة المستخدمين تحتاج كلمة مرور تطبيق؛ بدونها تُنسب كل المقالات إليك."}},
	"users_endpoint_private": {{"en": "Authors (users endpoint private)", "ar": "الكتّاب (قائمة المستخدمين محجوبة)"}, {"en": "The site blocks its users list (HTTP 403 from a plugin or security setting): every post is attributed to you unless you upload a WXR export.", "ar": "الموقع يحجب قائمة المستخدمين (HTTP 403 من إضافة أو إعداد أمان): تُنسب كل المقالات إليك ما لم ترفع ملف WXR."}},
	"products_no_commerce":   {{"en": "WooCommerce products (catalog)", "ar": "منتجات WooCommerce (الكتالوج)"}, {"en": "Readable, but the commerce module is not installed: products are skipped until it is (a later re-run imports them).", "ar": "قابلة للقراءة لكن وحدة المتجر غير مثبتة: تُتخطى المنتجات حتى تُثبَّت (وإعادة التشغيل لاحقًا تستوردها)."}},
	"menus":                  {{"en": "Menus", "ar": "القوائم"}, nil},
	"menus_auth":             {{"en": "Menus", "ar": "القوائم"}, {"en": "Menus are only readable with an application password (or from a WXR export).", "ar": "القوائم لا تُقرأ إلا بكلمة مرور تطبيق (أو من ملف تصدير WXR)."}},
	"raw_content":            {{"en": "Raw block markup", "ar": "ترميز الكتل الخام"}, {"en": "Gutenberg blocks are converted to clean HTML.", "ar": "تُحوَّل كتل غوتنبرغ إلى HTML نظيف."}},
	"raw_content_auth":       {{"en": "Raw block markup", "ar": "ترميز الكتل الخام"}, {"en": "Without an application password only rendered HTML is available; it is cleaned of WordPress classes.", "ar": "بدون كلمة مرور تطبيق يتوفر HTML المعروض فقط، ويُنظَّف من أصناف ووردبريس."}},
	"settings":               {{"en": "Site title, tagline and front page", "ar": "عنوان الموقع ووصفه والصفحة الرئيسية"}, {"en": "Read from the WordPress settings and imported; other settings (date format, reading options…) are not.", "ar": "تُقرأ من إعدادات ووردبريس وتُستورد؛ أما بقية الإعدادات (صيغة التاريخ، خيارات القراءة…) فلا."}},
	"settings_public":        {{"en": "Site title, tagline and front page", "ar": "عنوان الموقع ووصفه والصفحة الرئيسية"}, {"en": "Taken from the public site index (front page when it exposes one); the full settings need an application password and are not imported.", "ar": "تؤخذ من فهرس الموقع العام (والصفحة الرئيسية إن ظهرت)؛ الإعدادات الكاملة تحتاج كلمة مرور تطبيق ولا تُستورد."}},
	"seo_yoast":              {{"en": "Yoast SEO", "ar": "Yoast SEO"}, {"en": "Titles, descriptions, canonical, robots and Open Graph are mapped.", "ar": "تُنقل العناوين والأوصاف والرابط الأساسي وrobots وOpen Graph."}},
	"seo_rankmath":           {{"en": "Rank Math", "ar": "Rank Math"}, {"en": "Mapped only when rank_math_* meta is exposed.", "ar": "يُنقل فقط إذا كانت حقول rank_math_* مكشوفة."}},
	"acf":                    {{"en": "ACF fields", "ar": "حقول ACF"}, nil},
	"acf_hidden":             {{"en": "ACF fields", "ar": "حقول ACF"}, {"en": "ACF is active but its fields are hidden from REST (enable \"Show in REST\").", "ar": "ACF مفعّل لكن حقوله مخفية عن REST (فعّل \"Show in REST\")."}},
	"products":               {{"en": "WooCommerce products (catalog)", "ar": "منتجات WooCommerce (الكتالوج)"}, {"en": "Name, slug, descriptions, price, categories and images.", "ar": "الاسم والرابط والأوصاف والسعر والتصنيفات والصور."}},
	"product_meta":           {{"en": "Product plan mapping (MCBB meta)", "ar": "ربط الباقات (حقول MCBB)"}, {"en": "Private product meta is hidden from the public API: connect with an application password or upload a WXR export.", "ar": "حقول المنتج الخاصة مخفية عن الواجهة العامة: اتصل بكلمة مرور تطبيق أو ارفع ملف WXR."}},
	"media_offloaded":        {{"en": "Offloaded media", "ar": "وسائط على خادم خارجي"}, {"en": "Some files live on another host (CDN/offload plugin); they are downloaded from there.", "ar": "بعض الملفات على خادم آخر (CDN أو إضافة نقل)؛ تُنزَّل من هناك."}},
	"custom_templates":       {{"en": "Custom page templates", "ar": "قوالب صفحات مخصصة"}, {"en": "PHP templates are not migrated: rebuild them as theme templates/components.", "ar": "قوالب PHP لا تُنقل: أعد بناءها كقوالب ومكوّنات في الثيم."}},
	"shortcodes":             {{"en": "Shortcodes", "ar": "الأكواد المختصرة"}, {"en": "Left in place and listed in the validation report; rebuild them as theme components.", "ar": "تُترك كما هي وتُسرد في تقرير التحقق؛ أعد بناءها كمكوّنات في الثيم."}},
	"page_builder":           {{"en": "Page builder layouts", "ar": "تخطيطات منشئ الصفحات"}, {"en": "Only the HTML output survives; layouts must be rebuilt.", "ar": "يبقى ناتج HTML فقط؛ يجب إعادة بناء التخطيطات."}},
	"plugin_pages":           {{"en": "Plugin-rendered pages", "ar": "صفحات تعرضها إضافات"}, {"en": "Cart, checkout, account and shop pages come from GoRun commerce instead.", "ar": "صفحات السلة والدفع والحساب والمتجر تأتي من متجر GoRun بدلًا منها."}},
	"theme_settings":         {{"en": "Theme settings and widgets", "ar": "إعدادات الثيم والودجات"}, {"en": "Not readable through REST; recreate them in the GoRun theme.", "ar": "غير قابلة للقراءة عبر REST؛ أعد إنشاءها في ثيم GoRun."}},
	"comments":               {{"en": "Comments", "ar": "التعليقات"}, {"en": "Not migrated in v1.", "ar": "لا تُنقل في الإصدار الأول."}},
	"passwords":              {{"en": "User passwords", "ar": "كلمات مرور المستخدمين"}, {"en": "Never readable; users sign in with a magic link.", "ar": "غير قابلة للقراءة أبدًا؛ يدخل المستخدمون برابط سحري."}},
	"orders":                 {{"en": "Orders and customers", "ar": "الطلبات والعملاء"}, {"en": "Not migrated: customers start fresh.", "ar": "لا تُنقل: يبدأ العملاء من جديد."}},
}

// finding builds a Finding from the label table.
func finding(kind string, count int, items ...string) Finding {
	key := kind
	if _, ok := labels[key]; !ok {
		key, _, _ = strings.Cut(kind, ":")
	}
	l := labels[key]
	f := Finding{Kind: kind, Count: count, Label: l[0], Note: l[1], Items: items}
	if f.Label == nil {
		f.Label = map[string]string{"en": kind, "ar": kind}
	}
	return f
}

// typeFinding labels a content type or taxonomy with its WordPress name.
func typeFinding(prefix, slug, name string, count int) Finding {
	f := finding(prefix+":"+slug, count)
	base := labels[prefix][0]
	f.Label = map[string]string{"en": fmt.Sprintf("%s: %s", base["en"], name), "ar": fmt.Sprintf("%s: %s", base["ar"], name)}
	return f
}

// sortedCounts renders a count map as "key ×n" items, most frequent first.
func sortedCounts(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = fmt.Sprintf("%s ×%d", k, m[k])
	}
	return out
}

func sum(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}
