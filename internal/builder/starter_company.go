package builder

func companyPages(w starterWriter, title, tagline string) []starterPage {
	hero := w.section("hero", title, title, tagline, tagline)
	hero.Variant = "split"
	hero.HeroScene = "orbit"

	hero.ImageAlt = w.word("Original abstract strategy illustration", "رسم تجريدي أصلي لمسارات التخطيط")
	hero.Link = "@services"
	hero.LinkLabel = w.word("Explore our services", "اكتشف خدماتنا")
	services := w.section("services", "Clear services. Useful outcomes.", "خدمات واضحة ونتائج مفيدة", "A sample service offering, ready to replace with your business.", "أمثلة للخدمات يمكنك استبدالها بما تقدمه شركتك.")
	services.Items = []Entry{{Title: w.word("Strategy and discovery", "الاستراتيجية والتخطيط"), Text: w.word("Define the audience, priorities and a practical plan.", "حدّد الجمهور والأولويات وخطة عملية للتنفيذ."), Image: w.image("strategy.svg"), ImageAlt: w.word("Strategy illustration", "رسم توضيحي للتخطيط"), Link: "@services"}, {Title: w.word("Design and identity", "التصميم والهوية"), Text: w.word("Create a clear visual language and coherent experience.", "ابنِ لغة بصرية واضحة وتجربة متناسقة."), Image: w.image("identity.svg"), ImageAlt: w.word("Identity illustration", "رسم توضيحي للهوية"), Link: "@services"}, {Title: w.word("Build and delivery", "التنفيذ والتسليم"), Text: w.word("Turn the design into a working, maintainable product.", "حوّل التصميم إلى منتج يعمل ويمكن تطويره."), Image: w.image("delivery.svg"), ImageAlt: w.word("Delivery illustration", "رسم توضيحي للتنفيذ"), Link: "@services"}}
	process := w.section("features", "From first brief to final delivery", "من الفكرة الأولى إلى التسليم", "A simple way to work together.", "طريقة واضحة للعمل معًا.")
	process.Items = []Entry{{Title: w.word("Understand", "نفهم"), Text: w.word("Agree on the problem and what success means.", "نتفق على المشكلة ومعايير نجاح الحل.")}, {Title: w.word("Design", "نصمم"), Text: w.word("Review a concrete direction before implementation.", "نراجع اتجاهًا واضحًا قبل التنفيذ.")}, {Title: w.word("Deliver", "ننفذ"), Text: w.word("Test the result and hand it over with clear guidance.", "نختبر النتيجة ونسلّمها مع إرشادات واضحة.")}}
	work := w.section("projects", "Selected directions", "نماذج اتجاهات التصميم", "Illustrative examples, not client work or claimed results.", "أمثلة توضيحية للتصميم، وليست أعمالًا أو نتائج لعملاء حقيقيين.")
	work.Items = []Entry{{Title: w.word("Service platform", "منصة خدمات"), Text: w.word("Sample project concept.", "تصور تجريبي لمشروع."), Image: w.image("delivery.svg"), ImageAlt: w.word("Abstract platform artwork", "تصور تجريدي لمنصة خدمات")}, {Title: w.word("Brand system", "نظام هوية"), Text: w.word("Sample visual system.", "تصور تجريبي للهوية البصرية."), Image: w.image("identity.svg"), ImageAlt: w.word("Abstract identity artwork", "تصور تجريدي للهوية")}}
	// A placeholder to replace with a real client's words, never an invented quote.
	voice := w.section("testimonials", "What clients say", "ماذا يقول عملاؤنا", "Replace with a short quote from a real client, with their permission.", "استبدل هذا باقتباس قصير من عميل حقيقي بعد موافقته.")
	voice.Items = []Entry{{Title: w.word("[Client name, role and company]", "[اسم العميل، منصبه وشركته]"), Text: w.word("[A short quote about working with you and the result they got.]", "[اقتباس قصير عن تجربة العمل معكم والنتيجة التي حصل عليها.]")}}
	aboutHero := w.section("hero", "A thoughtful team for useful work", "فريق يهتم بجودة العمل", "Replace this introduction with your actual team and story.", "استبدل هذه المقدمة بقصة فريقك الحقيقية.")
	aboutHero.Variant = "split"
	aboutHero.Image = w.image("identity.svg")
	aboutHero.ImageAlt = w.word("Original identity illustration", "رسم أصلي للهوية")
	about := w.section("text", "Clarity is our starting point", "الوضوح هو نقطة البداية", "Explain what your business believes, whom it serves and how it works.", "عرّف بما تؤمن به شركتك والجمهور الذي تخدمه وطريقة عملك.")
	serviceHero := w.section("hero", "Built around your priorities", "خدمات تبدأ من أولوياتك", "Replace these examples with your real scope and deliverables.", "استبدل هذه الأمثلة بنطاق خدماتك ومخرجاتها الحقيقية.")
	workHero := w.section("hero", "A place for your best work", "مساحة لأفضل أعمالك", "Add real projects and explain your role in each.", "أضف مشاريعك الحقيقية واشرح دورك في كل مشروع.")
	return []starterPage{w.page("home", "Home", "الرئيسية", hero, services, process, work, voice, w.cta()), w.page("about", "About", "من نحن", aboutHero, about, process, w.cta()), w.page("services", "Services", "الخدمات", serviceHero, services, process, w.cta()), w.page("work", "Work", "الأعمال", workHero, work, w.cta()), w.contact()}
}
