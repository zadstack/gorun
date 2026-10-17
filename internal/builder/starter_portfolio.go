package builder

func portfolioPages(w starterWriter, title, tagline string) []starterPage {
	hero := w.section("hero", title, title, tagline, tagline)
	hero.Variant = "split"
	hero.HeroScene = "ribbons"

	hero.ImageAlt = w.word("Original studio mark artwork", "عمل بصري أصلي للاستوديو")
	hero.Link = "@work"
	hero.LinkLabel = w.word("View selected work", "شاهد الأعمال")
	work := w.section("projects", "Selected work", "أعمال مختارة", "Original illustrative examples. Replace with your own work before launch.", "أمثلة بصرية أصلية. استبدلها بأعمالك الحقيقية قبل الإطلاق.")
	work.Items = []Entry{{Title: w.word("Orbit / Digital experience", "مدار / تجربة رقمية"), Text: w.word("Example project: describe your role, process and outcome.", "مشروع تجريبي: صف دورك وطريقة العمل والنتيجة."), Image: w.image("project-orbit.svg"), ImageAlt: w.word("Original orbit project artwork", "رسم أصلي لمشروع مدار")}, {Title: w.word("Grid / Visual system", "شبكة / نظام بصري"), Text: w.word("Example project: add a concise project story.", "مشروع تجريبي: أضف قصة مختصرة للمشروع."), Image: w.image("project-grid.svg"), ImageAlt: w.word("Original grid project artwork", "رسم أصلي لمشروع شبكة")}, {Title: w.word("Form / Product design", "تكوين / تصميم منتج"), Text: w.word("Example project: explain the problem and design decisions.", "مشروع تجريبي: اشرح المشكلة وقرارات التصميم."), Image: w.image("project-form.svg"), ImageAlt: w.word("Original form project artwork", "رسم أصلي لمشروع تكوين")}}
	intro := w.section("text", "Independent work. Clear thinking.", "عمل مستقل وفكر واضح", "Introduce your expertise and what makes your approach useful.", "عرّف بخبرتك وما يجعل طريقة عملك مفيدة.")
	intro.Link = "@about"
	intro.LinkLabel = w.word("More about me", "اعرف المزيد عني")
	services := w.section("services", "How I can help", "كيف أساعدك", "Replace these examples with your own expertise and services.", "استبدل هذه الأمثلة بخبراتك وخدماتك الحقيقية.")
	services.Items = []Entry{{Title: w.word("Digital design", "التصميم الرقمي"), Text: w.word("Websites and products with clear user journeys.", "مواقع ومنتجات بمسارات استخدام واضحة.")}, {Title: w.word("Visual identity", "الهوية البصرية"), Text: w.word("A coherent visual language across touchpoints.", "لغة بصرية متناسقة عبر نقاط التواصل.")}, {Title: w.word("Creative direction", "التوجيه الإبداعي"), Text: w.word("A strong direction for an idea or project.", "اتجاه واضح لفكرة أو مشروع.")}}
	aboutHero := w.section("hero", "The person behind the work", "الشخص وراء العمل", "Add your real background, approach and interests.", "أضف خلفيتك الحقيقية وأسلوبك واهتماماتك.")
	aboutHero.Image = w.image("studio-mark.svg")
	aboutHero.ImageAlt = hero.ImageAlt
	aboutHero.Variant = "split"
	workHero := w.section("hero", "Projects, not just pictures", "مشاريع لها قصة", "Give each project context and show your contribution.", "أضف سياقًا لكل مشروع ووضّح مساهمتك فيه.")
	serviceHero := w.section("hero", "A focused set of capabilities", "خبرات واضحة ومتخصصة", "Make it easy to understand what you offer.", "سهّل على الزائر فهم ما تقدمه.")
	return []starterPage{w.page("home", "Home", "الرئيسية", hero, work, intro, w.cta()), w.page("work", "Work", "الأعمال", workHero, work, w.cta()), w.page("about", "About", "نبذة عني", aboutHero, intro, services, w.cta()), w.page("services", "Services", "الخدمات", serviceHero, services, w.cta()), w.contact()}
}
