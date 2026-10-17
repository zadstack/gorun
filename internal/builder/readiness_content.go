package builder

import (
	"database/sql"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/menus"
	"github.com/zadstack/gorun/internal/theme"
	"golang.org/x/net/html"
)

func localReadinessFile(root, path string) string {
	if path == "" || filepath.IsAbs(path) || strings.Contains(path, "\\") {
		return "fail"
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "fail"
		}
		return "unknown"
	}
	defer dir.Close()
	file, err := dir.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "escapes") {
			return "fail"
		}
		return "unknown"
	}
	defer file.Close()
	st, err := file.Stat()
	if err != nil {
		return "unknown"
	}
	if !st.Mode().IsRegular() || st.Size() == 0 {
		return "fail"
	}
	return "pass"
}
func (b *readinessBuilder) image(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil {
		return "fail"
	}
	if u.IsAbs() || u.Host != "" {
		return "unknown"
	}
	if strings.HasPrefix(u.Path, "/media/") {
		return localReadinessFile(filepath.Join(b.d.Config.DataDir, "media"), strings.TrimPrefix(u.Path, "/media/"))
	}
	if strings.HasPrefix(u.Path, "/themes/") {
		parts := strings.SplitN(strings.TrimPrefix(u.Path, "/themes/"), "/", 3)
		if len(parts) != 3 || parts[1] != "assets" {
			return "fail"
		}
		lookup, ok := kernel.Get[theme.InfoLookup](b.d, kernel.ServiceTheme)
		if !ok {
			return "unknown"
		}
		info, e := lookup.Lookup(parts[0])
		if e != nil {
			return "fail"
		}
		return localReadinessFile(filepath.Join(info.Dir, "assets"), parts[2])
	}
	return "unknown"
}
func readinessText(body string) string {
	z := html.NewTokenizer(strings.NewReader(body))
	var text strings.Builder
	for {
		token := z.Next()
		if token == html.ErrorToken {
			break
		}
		if token == html.TextToken {
			text.Write(z.Text())
			text.WriteByte(' ')
		}
	}
	return strings.TrimSpace(text.String())
}
func (b *readinessBuilder) inspectContent(cs content.Service, page content.Page[content.Item]) {
	scope := "pass"
	scopeEvidence := b.word("Inspected published public content items: ", "المحتويات العامة المنشورة المفحوصة: ") + readinessCount(len(page.Items)) + " / " + readinessCount(page.Total)
	if page.Total > len(page.Items) {
		scope = "warn"
		scopeEvidence += b.word(". Partial inspection; remaining pages were not inspected.", ". فحص جزئي؛ بقية الصفحات لم تُفحص.")
	}
	b.add(1, "inspection", "Inspection scope", "نطاق الفحص", scope, scopeEvidence, "/admin/content/page")
	placeholders, missingAlt, missingFiles, unverified, images := 0, 0, 0, 0, 0
	hasContact := false
	partial := page.Total > len(page.Items)
	placeholderLink, altLink, imageLink := "/admin/content/page", "/admin/media", "/admin/media"
	placeholderTitle, altTitle, imageTitle := "", "", ""
	for _, it := range page.Items {
		fix := "/admin/content/" + it.Type + "/" + it.ID
		if it.Template == "builder" {
			fix = "/admin/builder/" + it.ID
		}
		if len(it.Body) > 256*1024 {
			partial = true
			continue
		}
		text := strings.ToLower(readinessText(it.Body))
		for _, sample := range []string{"replace this", "replace these", "example project", "sample project", "original illustrative examples", "استبدل", "مشروع تجريبي", "أمثلة توضيحية", "أمثلة بصرية أصلية", "lorem ipsum"} {
			if strings.Contains(text, sample) {
				placeholders++
				if placeholderTitle == "" {
					placeholderTitle = it.Title
					placeholderLink = fix
				}
				break
			}
		}
		if it.FeaturedMediaID != "" {
			images++
			if images > 200 {
				partial = true
			} else {
				ms, ok := kernel.Get[media.Service](b.d, kernel.ServiceMedia)
				if !ok {
					unverified++
				} else {
					m, e := ms.Get(b.ctx, it.FeaturedMediaID)
					if e != nil {
						if errors.Is(e, media.ErrNotFound) {
							missingFiles++
						} else {
							unverified++
						}
					} else {
						if strings.TrimSpace(m.Alt) == "" {
							missingAlt++
							if altTitle == "" {
								altTitle = it.Title
								altLink = "/admin/media"
							}
						}
						source := m.URL
						if v, ok := m.Variants["large"]; ok {
							source = v.URL
						}
						switch b.image(source) {
						case "fail":
							missingFiles++
							if imageTitle == "" {
								imageTitle = it.Title
								imageLink = "/admin/media"
							}
						case "unknown":
							unverified++
						}
					}
				}
			}
		}

		z := html.NewTokenizer(strings.NewReader(it.Body))
		for {
			tok := z.Next()
			if tok == html.ErrorToken {
				break
			}
			if tok != html.StartTagToken && tok != html.SelfClosingTagToken {
				continue
			}
			t := z.Token()
			var src, alt, class, action string
			for _, a := range t.Attr {
				switch a.Key {
				case "src":
					src = a.Val
				case "alt":
					alt = a.Val
				case "class":
					class = a.Val
				case "action":
					action = a.Val
				}
			}
			if t.Data == "section" && strings.Contains(" "+class+" ", " gp-contact ") {
				hasContact = true
			}
			if t.Data == "form" && action == "/forms/contact" {
				hasContact = true
			}
			if t.Data != "img" {
				continue
			}
			images++
			if images > 200 {
				partial = true
				continue
			}
			if strings.TrimSpace(alt) == "" {
				missingAlt++
				if altTitle == "" {
					altTitle = it.Title
					altLink = fix
				}
			}
			switch b.image(src) {
			case "fail":
				missingFiles++
				if imageTitle == "" {
					imageTitle = it.Title
					imageLink = fix
				}
			case "unknown":
				unverified++
			}
		}
	}
	status := "pass"
	if partial || len(page.Items) == 0 {
		status = "unknown"
	}
	if placeholders > 0 {
		status = "warn"
	}
	b.add(1, "placeholders", "Starter text", "النصوص التجريبية", status, b.word("Items containing sample/replacement wording: ", "محتويات بها نصوص أمثلة أو استبدال: ")+readinessCount(placeholders)+b.firstTitle(placeholderTitle), placeholderLink)
	status = "pass"
	if partial || len(page.Items) == 0 {
		status = "unknown"
	}
	if missingAlt > 0 {
		status = "warn"
	}
	b.add(1, "alt", "Image descriptions", "أوصاف الصور", status, b.word("Images with empty/missing alt: ", "صور بوصف بديل فارغ أو مفقود: ")+readinessCount(missingAlt)+b.word(". Decorative images may intentionally have empty alt.", ". قد يكون الوصف فارغًا عمدًا للصور الزخرفية.")+b.firstTitle(altTitle), altLink)
	status = "pass"
	if unverified > 0 || partial || len(page.Items) == 0 {
		status = "unknown"
	}
	if missingFiles > 0 {
		status = "fail"
	}
	b.add(1, "images", "Local image availability", "توفر الصور المحلية", status, b.word("Missing/invalid local images: ", "صور محلية مفقودة أو غير صالحة: ")+readinessCount(missingFiles)+b.word("; remote/unverified images: ", "؛ صور خارجية أو غير متحقق منها: ")+readinessCount(unverified)+b.firstTitle(imageTitle), imageLink)
	status = "warn"
	if hasContact {
		status = "pass"
	}
	b.add(2, "contact_form", "Published contact section", "قسم تواصل منشور", status, b.word("A published contact section/form was found: ", "تم العثور على قسم أو نموذج تواصل منشور: ")+b.word(map[bool]string{true: "yes", false: "no"}[hasContact], map[bool]string{true: "نعم", false: "لا"}[hasContact])+b.word(". Submission and delivery remain unverified.", ". الإرسال والتسليم غير متحقق منهما."), "/admin/content/page")
	if partial {
		b.add(1, "bounds", "Inspection bounds", "حدود الفحص", "warn", b.word("An oversized body or image cap was reached. Some checks are incomplete.", "تم بلوغ حد حجم المحتوى أو الصور. بعض الفحوص غير مكتملة."), "/admin/content/page")
	}
}
func (b *readinessBuilder) navigation(cs content.Service, page content.Page[content.Item]) {
	locale, e := b.setting("site.locale")
	if locale == "" {
		locale = i18n.DefaultLocale
	}
	var menuID string
	if e == nil {
		e = b.d.DB.QueryRowContext(b.ctx, `SELECT id FROM menus WHERE location='primary' AND locale IN (?, '') ORDER BY CASE WHEN locale=? THEN 0 ELSE 1 END LIMIT 1`, locale, locale).Scan(&menuID)
	}
	if e != nil {
		status := "unknown"
		if errors.Is(e, sql.ErrNoRows) {
			status = "warn"
		}
		b.add(0, "navigation", "Primary navigation", "القائمة الرئيسية", status, b.word("Raw primary menu is unavailable; database errors are not treated as a healthy menu.", "القائمة الرئيسية الأصلية غير متاحة؛ أخطاء قاعدة البيانات لا تعني أن القائمة سليمة."), "/admin/menus")
		return
	}
	rows, e := b.d.DB.QueryContext(b.ctx, `SELECT kind,ref_id,url FROM menu_items WHERE menu_id=? ORDER BY position,id LIMIT 201`, menuID)
	if e != nil {
		b.add(0, "navigation", "Primary navigation", "القائمة الرئيسية", "unknown", b.word("Raw menu items could not be inspected.", "تعذر فحص عناصر القائمة الأصلية."), "/admin/menus")
		return
	}
	items := []menus.Item{}
	for rows.Next() {
		var it menus.Item
		if e = rows.Scan(&it.Kind, &it.RefID, &it.URL); e != nil {
			break
		}
		items = append(items, it)
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		b.add(0, "navigation", "Primary navigation", "القائمة الرئيسية", "unknown", b.word("Menu query failed.", "تعذر الاستعلام عن القائمة."), "/admin/menus")
		return
	}
	paths := map[string]bool{}
	linker := theme.NewLinker(b.d)
	for i := range page.Items {
		paths[linker.Item(b.ctx, &page.Items[i])] = true
	}
	invalid, unknown := 0, 0
	for _, it := range items[:min(len(items), 200)] {
		switch it.Kind {
		case menus.KindContent:
			item, err := cs.Get(b.ctx, it.RefID)
			if err != nil {
				if errors.Is(err, content.ErrNotFound) {
					invalid++
				} else {
					unknown++
				}
			} else {
				typ, e := cs.GetType(b.ctx, item.Type)
				if e != nil {
					unknown++
				} else if item.Status != "published" || !typ.Public {
					invalid++
				}
			}
		case menus.KindCustom:
			u, e := url.Parse(it.URL)
			if e != nil || u.User != nil {
				invalid++
			} else if u.Host != "" || u.IsAbs() {
				unknown++
			} else if u.Path != "" && !paths[u.Path] {
				unknown++
			}
		default:
			unknown++
		}
	}
	if len(items) > 200 {
		unknown++
	}
	status := "pass"
	if len(items) == 0 {
		status = "warn"
	}
	if unknown > 0 {
		status = "unknown"
	}
	if invalid > 0 {
		status = "fail"
	}
	b.add(0, "navigation", "Primary navigation", "القائمة الرئيسية", status, b.word("Checked raw links: ", "روابط أصلية مفحوصة: ")+readinessCount(min(len(items), 200))+b.word("; unpublished/private/missing: ", "؛ غير منشورة أو خاصة أو مفقودة: ")+readinessCount(invalid)+b.word("; external/routes not established locally: ", "؛ خارجية أو مسارات غير مثبتة محليًا: ")+readinessCount(unknown), "/admin/menus")
}

var readinessBackupName = regexp.MustCompile(`^(\d{8}T\d{6}Z)\.(db|tar\.gz)$`)

func (b *readinessBuilder) backup() {
	status := "warn"
	evidence := b.word("No complete nonempty backup pair found. Restore is not tested.", "لم يُعثر على زوج ملفات نسخة احتياطية كامل وغير فارغ. الاستعادة لم تُختبر.")
	dir, err := os.OpenRoot(filepath.Join(b.d.Config.DataDir, "backups"))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			status = "unknown"
		}
	} else {
		defer dir.Close()
		f, e := dir.Open(".")
		if e != nil {
			status = "unknown"
		} else {
			defer f.Close()
			entries, e := f.ReadDir(201)
			if e != nil && !errors.Is(e, io.EOF) {
				status = "unknown"
			} else {
				pairs := map[string]int{}
				for _, entry := range entries[:min(len(entries), 200)] {
					match := readinessBackupName.FindStringSubmatch(entry.Name())
					if match == nil {
						continue
					}
					if localReadinessFile(filepath.Join(b.d.Config.DataDir, "backups"), entry.Name()) == "pass" {
						pairs[match[1]]++
					}
				}
				for _, count := range pairs {
					if count == 2 {
						status = "pass"
						evidence = b.word("A nonempty database/archive backup pair exists. Contents, freshness and restore are unverified.", "يوجد زوج قاعدة بيانات وأرشيف غير فارغ للنسخة الاحتياطية. المحتويات والحداثة والاستعادة غير متحقق منها.")
					}
				}
				if len(entries) > 200 {
					status = "unknown"
					evidence = b.word("Backup directory exceeds the 200-entry inspection cap.", "مجلد النسخ تجاوز حد الفحص البالغ 200 ملف.")
				}
			}
		}
	}
	b.add(2, "backup", "Backup availability", "توفر النسخ الاحتياطية", status, evidence, "/admin/system/backups")
}

func (b *readinessBuilder) activeTheme() {
	status := "unknown"
	evidence := b.word("Active theme metadata could not be inspected.", "تعذر فحص بيانات القالب النشط.")
	ts, ok := kernel.Get[theme.Service](b.d, kernel.ServiceTheme)
	if ok {
		slug := ts.Active(b.ctx).Slug
		lookup, ok := ts.(theme.InfoLookup)
		if ok {
			_, err := lookup.Lookup(slug)
			if err == nil {
				status = "pass"
				evidence = b.word("Installed active theme metadata: ", "بيانات القالب النشط المثبت: ") + slug + b.word(". Rendering is unverified.", ". عرض الصفحات غير متحقق منه.")
			} else if errors.Is(err, theme.ErrNotFound) || errors.Is(err, theme.ErrInvalidTheme) {
				status = "fail"
				evidence = b.word("Active theme metadata is missing or invalid.", "بيانات القالب النشط مفقودة أو غير صالحة.")
			}
		}
	}
	b.add(0, "active_theme", "Active theme", "القالب النشط", status, evidence, "/admin/appearance/themes")
}

func (b *readinessBuilder) firstTitle(title string) string {
	if title == "" {
		return ""
	}
	return b.word("; first item: ", "؛ أول محتوى: ") + title
}
