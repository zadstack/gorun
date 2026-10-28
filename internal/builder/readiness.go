package builder

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/zadstack/gorun/internal/brand"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/settings"
)

type readinessCheck struct{ ID, Title, Status, Evidence, Fix string }
type readinessGroup struct {
	Title  string
	Checks []readinessCheck
}
type readinessReport struct {
	Groups []readinessGroup
	Note   string
}
type readinessBuilder struct {
	ctx    context.Context
	d      *kernel.Deps
	ar     bool
	report readinessReport
}

func (b *readinessBuilder) word(en, ar string) string {
	if b.ar {
		return ar
	}
	return en
}
func (b *readinessBuilder) add(group int, id, titleEN, titleAR, status, evidence, fix string) {
	b.report.Groups[group].Checks = append(b.report.Groups[group].Checks, readinessCheck{id, b.word(titleEN, titleAR), status, evidence, fix})
}
func (b *readinessBuilder) setting(key string) (string, error) {
	var v string
	err := b.d.Settings.Get(b.ctx, key, &v)
	if errors.Is(err, settings.ErrNotFound) {
		return "", nil
	}
	return v, err
}
func buildReadiness(ctx context.Context, d *kernel.Deps) readinessReport {
	b := &readinessBuilder{ctx: ctx, d: d, ar: i18n.Locale(ctx) == "ar"}
	b.report.Groups = []readinessGroup{{Title: b.word("Site and identity", "الموقع والهوية")}, {Title: b.word("Published content", "المحتوى المنشور")}, {Title: b.word("Contact and operations", "التواصل والتشغيل")}}
	b.report.Note = b.word("Read-only local inspection of up to 100 public published items, 200 images and 200 navigation links. No network requests, browser rendering, email delivery, load test or restore test is performed. Partial inspection is shown explicitly.", "فحص محلي للقراءة فقط بحد أقصى 100 محتوى منشور عام و200 صورة و200 رابط قائمة. لا يتضمن طلبات شبكة أو عرض المتصفح أو إرسال البريد أو اختبار الحمل أو الاستعادة. يظهر الفحص الجزئي بوضوح.")
	cs, ok := kernel.Get[content.Service](d, kernel.ServiceContent)
	front, err := b.setting("site.front_page")
	var home *content.Item
	status := "warn"
	evidence := b.word("No static front page selected; the theme may use a dynamic home.", "لم تُحدد صفحة رئيسية ثابتة؛ قد يستخدم القالب صفحة ديناميكية.")
	if err != nil || !ok {
		status = "unknown"
		evidence = b.word("Front page settings/content unavailable.", "تعذر قراءة إعدادات الصفحة الرئيسية أو المحتوى.")
	} else if front != "" {
		home, err = cs.Get(ctx, front)
		if err != nil {
			status = "unknown"
			if errors.Is(err, content.ErrNotFound) {
				status = "fail"
			}
			evidence = b.word("Selected front page is unavailable.", "الصفحة الرئيسية المحددة غير متاحة.")
		} else {
			typ, e := cs.GetType(ctx, home.Type)
			if e != nil {
				status = "unknown"
				evidence = b.word("Content type could not be inspected.", "تعذر فحص نوع المحتوى.")
			} else if home.Status != "published" || !typ.Public {
				status = "fail"
				evidence = b.word("Front page is unpublished or belongs to a private content type.", "الرئيسية غير منشورة أو تنتمي إلى نوع محتوى خاص.")
			} else {
				status = "pass"
				evidence = home.Title
			}
		}
	}
	if status != "pass" {
		home = nil
	}
	b.add(0, "front", "Public front page", "صفحة رئيسية عامة", status, evidence, "/admin/settings/general")
	siteTitle, titleErr := b.setting("site.title")
	status = "pass"
	evidence = strings.TrimSpace(siteTitle)
	if titleErr != nil {
		status = "unknown"
		evidence = b.word("Site title settings unavailable.", "تعذر قراءة إعداد اسم الموقع.")
	} else if evidence == "" {
		status = "fail"
		evidence = b.word("Site title is empty; public pages fall back to GoRun.", "اسم الموقع فارغ؛ الصفحات العامة تعود إلى GoRun.")
	}
	b.add(0, "site_title", "Site title", "اسم الموقع", status, evidence, "/admin/settings/general")
	var identity brand.Identity
	err = d.Settings.Get(ctx, "builder.identity", &identity)
	status = "pass"
	evidence = b.word("Saved identity has valid contrast, colors, font and width.", "الهوية المحفوظة ذات تباين وألوان وخط وعرض صالح.")
	if errors.Is(err, settings.ErrNotFound) {
		status = "warn"
		evidence = b.word("No saved identity; theme defaults are used.", "لا توجد هوية محفوظة؛ تُستخدم إعدادات القالب.")
	} else if err != nil {
		status = "unknown"
		evidence = b.word("Identity could not be decoded.", "تعذر قراءة الهوية.")
	} else if brand.Validate(identity) != nil {
		status = "fail"
		evidence = b.word("Saved identity is invalid.", "الهوية المحفوظة غير صالحة.")
	}
	b.add(0, "identity", "Visual identity", "الهوية البصرية", status, evidence, "/admin/appearance/identity")
	b.logo()
	b.activeTheme()
	if !ok {
		b.add(1, "inspection", "Content inspection", "فحص المحتوى", "unknown", b.word("Content service unavailable.", "خدمة المحتوى غير متاحة."), "/admin/content/page")
	} else {
		page, e := cs.Query(ctx, content.Query{Status: "published", PublicOnly: true, PerPage: 100, Page: 1})
		if e != nil {
			b.add(1, "inspection", "Content inspection", "فحص المحتوى", "unknown", b.word("Published content query failed.", "تعذر الاستعلام عن المحتوى المنشور."), "/admin/content/page")
		} else {
			b.inspectContent(cs, page)
			b.navigation(cs, page)
		}
	}
	b.seo(home)
	b.contactMail()
	base, e := b.setting("site.base_url")
	if base == "" {
		base = d.Config.BaseURL
	}
	u, ue := url.Parse(base)
	status = "warn"
	evidence = b.word("Local/development URL; allowed for local preparation, not proof of public reachability.", "عنوان محلي أو تطويري؛ مناسب للتجهيز المحلي ولا يثبت الوصول العام.")
	if e != nil {
		status = "unknown"
		evidence = b.word("Base URL settings unavailable.", "تعذر قراءة إعداد عنوان الموقع.")
	} else if ue != nil || u.Hostname() == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		status = "fail"
		evidence = b.word("Base URL is invalid.", "عنوان الموقع غير صالح.")
	} else if u.Scheme == "https" && u.Hostname() != "localhost" && net.ParseIP(u.Hostname()) == nil && !strings.HasSuffix(u.Hostname(), ".test") && !strings.HasSuffix(u.Hostname(), ".local") {
		status = "pass"
		evidence = b.word("HTTPS public-style URL configured; DNS, TLS and runtime are unverified.", "عنوان HTTPS بصيغة عامة مضبوط؛ DNS وTLS والتشغيل غير متحقق منها.")
	}
	b.add(2, "base_url", "Public URL configuration", "إعداد العنوان العام", status, evidence, "/admin/settings/general")
	b.backup()
	return b.report
}
func (b *readinessBuilder) logo() {
	id, err := b.setting("site.logo")
	status := "warn"
	evidence := b.word("No logo selected.", "لم يُحدد شعار.")
	if err != nil {
		status = "unknown"
		evidence = b.word("Logo setting unavailable.", "تعذر قراءة إعداد الشعار.")
	} else if id != "" {
		ms, ok := kernel.Get[media.Service](b.d, kernel.ServiceMedia)
		if !ok {
			status = "unknown"
		} else {
			m, e := ms.Get(b.ctx, id)
			if e != nil || !strings.HasPrefix(m.Mime, "image/") {
				status = "fail"
				evidence = b.word("Selected logo image is missing or invalid.", "صورة الشعار المحددة مفقودة أو غير صالحة.")
				if e != nil && !errors.Is(e, media.ErrNotFound) {
					status = "unknown"
					evidence = b.word("Logo image record could not be inspected.", "تعذر فحص سجل صورة الشعار.")
				}
			} else {
				status = b.image(m.URL)
				evidence = b.word("Logo image exists; its file availability has not been established.", "صورة الشعار موجودة؛ توفر ملفها لم يُتحقق منه.")
				if status == "pass" {
					evidence = b.word("Logo image record and local file are available.", "سجل صورة الشعار وملفها المحلي متاحان.")
				} else if status == "fail" {
					evidence = b.word("Logo image record exists but its local file is missing or invalid.", "سجل صورة الشعار موجود لكن ملفها المحلي مفقود أو غير صالح.")
				}
			}
		}
	}
	b.add(0, "logo", "Site logo", "شعار الموقع", status, evidence, "/admin/appearance/identity")
}
func (b *readinessBuilder) seo(home *content.Item) {
	status := "warn"
	evidence := b.word("No published public front page was available for metadata inspection.", "لم تتوفر صفحة رئيسية عامة منشورة لفحص البيانات الوصفية.")
	if home != nil && home.Status == "published" {
		title, e := b.setting("site.title")
		if e != nil {
			status = "unknown"
		} else if strings.TrimSpace(title) == "" || title == "GoRun" {
			evidence = b.word("Choose a specific site title.", "اختر اسمًا مميزًا للموقع.")
		} else {
			description := home.Excerpt
			if meta, ok := home.SEO["description"].(string); ok && meta != "" {
				description = meta
			}
			if strings.TrimSpace(description) == "" {
				if len(home.Body) > 256*1024 {
					status = "unknown"
				} else {
					description = readinessText(home.Body)
				}
			}
			if strings.TrimSpace(description) == "" {
				evidence = b.word("Front-page description and textual fallback are empty.", "وصف الرئيسية والنص البديل فارغان.")
				if status == "unknown" {
					evidence = b.word("Front-page body exceeded the local inspection size limit.", "تجاوز محتوى الصفحة الرئيسية حد حجم الفحص المحلي.")
				}
			} else {
				status = "pass"
				evidence = b.word("Site title and front-page description/text fallback are present; rendered metadata remains unverified.", "عنوان الموقع ووصف الرئيسية أو نصها البديل موجودان؛ البيانات المعروضة في المتصفح غير متحقق منها.")
			}
		}
	}
	fix := "/admin/settings/general"
	if home != nil {
		fix = "/admin/content/" + home.Type + "/" + home.ID
	}
	b.add(1, "seo", "Home title and description", "عنوان ووصف الرئيسية", status, evidence, fix)
}
func readinessCount(n int) string { return fmt.Sprintf("%d", n) }
