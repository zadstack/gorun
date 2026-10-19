package builder

import (
	"github.com/zadstack/gorun/internal/brand"
	"github.com/zadstack/gorun/internal/db"
)

type starterPage struct {
	Slug, Title string
	Document    Document
}
type starterChoice struct {
	ID, Name, Description, Theme, Preview string
	Identity                              brand.Identity
}

func starterChoices(ar bool) []starterChoice {
	word := func(en, arabic string) string {
		if ar {
			return arabic
		}
		return en
	}
	out := []starterChoice{}
	for _, choice := range []struct{ id, nameEN, nameAR, descEN, descAR string }{
		{"company", "Company & services", "شركة وخدمات", "A light company site: home, about, services, work and contact.", "موقع شركة فاتح متكامل: الرئيسية، من نحن، الخدمات، الأعمال والتواصل."},
		{"portfolio", "Project portfolio", "معرض الأعمال", "An image-led portfolio: home, work, about, services and contact.", "بورتفوليو يبرز صور المشاريع: الرئيسية، الأعمال، النبذة، الخدمات والتواصل."},
		{"magazine", "Editorial magazine", "مجلة تحريرية", "A complete editorial publication: home, about, topics and contact, with article and archive layouts.", "مجلة متكاملة: الرئيسية، عن المجلة، الموضوعات والتواصل، مع تصميم للمقالات والأرشيف."},
	} {
		design := starterDesign(choice.id)
		out = append(out, starterChoice{ID: choice.id, Name: word(choice.nameEN, choice.nameAR), Description: word(choice.descEN, choice.descAR), Theme: design.Theme, Preview: "/static/admin/starter-previews/" + choice.id + ".png", Identity: design.Identity})
	}
	return out
}

type starterWriter struct{ locale, theme string }

func (w starterWriter) word(en, ar string) string {
	if w.locale == "ar" {
		return ar
	}
	return en
}
func (w starterWriter) section(kind, enTitle, arTitle, enText, arText string) Section {
	section := DefaultSection(kind, w.locale)
	section.ID = db.NewID()
	section.Title = w.word(enTitle, arTitle)
	section.Text = w.word(enText, arText)
	section.Items = nil
	return section
}
func (w starterWriter) page(slug, en, ar string, sections ...Section) starterPage {
	return starterPage{slug, w.word(en, ar), Document{Version: 1, Sections: sections}}
}
func (w starterWriter) image(file string) string {
	return "/themes/" + w.theme + "/assets/images/" + file
}
func (w starterWriter) cta() Section {
	s := w.section("cta", "Let's build something useful", "نحو مشروع يستحق التنفيذ", "Start with a conversation about what you need.", "ابدأ بمحادثة حول ما يحتاجه مشروعك.")
	s.Link = "@contact"
	s.LinkLabel = w.word("Start a conversation", "ابدأ محادثة")
	return s
}
func (w starterWriter) contact() starterPage {
	hero := w.section("hero", "Let's talk", "نتحدث عن مشروعك", "Tell us about your goals, scope and timing.", "أخبرنا بأهداف مشروعك ونطاقه والوقت المناسب لك.")
	form := w.section("contact", "Send a message", "أرسل رسالة", "Use the form below. Set your real contact details and notification email before launch.", "استخدم النموذج التالي. أضف بيانات تواصلك وبريد التنبيهات الحقيقي قبل الإطلاق.")
	faq := w.section("faq", "Before we start", "قبل أن نبدأ", "A few details help us respond clearly.", "بعض التفاصيل تساعدنا على الرد بوضوح.")
	faq.Items = []Entry{{Title: w.word("What should I include?", "ماذا أكتب في الرسالة؟"), Text: w.word("Describe the project, your audience and the outcome you want.", "صف المشروع وجمهوره والنتيجة التي تريد الوصول إليها.")}, {Title: w.word("Where are contact details edited?", "أين أعدّل بيانات التواصل؟"), Text: w.word("Replace this example with your actual email, working hours and location.", "استبدل هذا المثال ببريدك ومواعيد عملك وموقعك الحقيقي.")}}
	return w.page("contact", "Contact", "تواصل معنا", hero, form, faq)
}
func starterPages(kind, title, tagline, locale string) []starterPage {
	w := starterWriter{locale, starterDesign(kind).Theme}
	switch kind {
	case "company":
		return companyPages(w, title, tagline)
	case "portfolio":
		return portfolioPages(w, title, tagline)
	case "magazine":
		return magazinePages(w, title, tagline)
	}
	return nil
}
