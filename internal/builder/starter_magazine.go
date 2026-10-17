package builder

func magazinePages(w starterWriter, title, tagline string) []starterPage {
	// The masthead already shows the title and tagline; the cover invites reading instead.
	hero := w.section("hero", "Stories worth your time", "قصص تستحق وقتك", "Long reads, short notes and conversations from the editors.", "مقالات طويلة وملاحظات قصيرة وحوارات من فريق التحرير.")
	hero.Image = w.image("cover-perspectives.svg")
	hero.ImageAlt = w.word("Original editorial cover illustration", "رسم غلاف تحريري أصلي")
	hero.Variant = "split"
	hero.Link = "@topics"
	hero.LinkLabel = w.word("Explore the publication", "اكتشف موضوعات المجلة")
	posts := w.section("text", "Latest stories", "أحدث المقالات", "The latest public articles appear here automatically.", "تظهر أحدث المقالات المنشورة هنا تلقائيًا.")
	posts.Feed = "posts"
	topics := w.section("features", "Ideas worth exploring", "أفكار تستحق القراءة", "A sample editorial scope. Adapt it to your publication.", "أمثلة لنطاق المحتوى. عدّلها لتناسب مجلتك.")
	topics.Items = []Entry{{Title: w.word("Culture and people", "الثقافة والناس"), Text: w.word("Stories about communities, ideas and everyday life.", "قصص عن المجتمعات والأفكار والحياة اليومية."), Image: w.image("cover-culture.svg"), ImageAlt: w.word("Original culture cover artwork", "رسم غلاف أصلي للثقافة")}, {Title: w.word("Design and craft", "التصميم والحرفة"), Text: w.word("A closer look at how useful things are made.", "نظرة أقرب إلى طريقة صنع الأشياء المفيدة."), Image: w.image("cover-craft.svg"), ImageAlt: w.word("Original craft cover artwork", "رسم غلاف أصلي للحرفة")}}
	note := w.section("text", "A note from the editors", "كلمة التحرير", "State the publication's purpose, standards and the audience it serves.", "اشرح هدف المجلة ومعاييرها والجمهور الذي تخدمه.")
	note.Link = "@about"
	note.LinkLabel = w.word("About the publication", "عن المجلة")
	aboutHero := w.section("hero", "A clear editorial point of view", "وجهة نظر تحريرية واضحة", "Replace this example with your publication's story.", "استبدل هذا المثال بقصة مجلتك.")
	aboutHero.Image = w.image("cover-culture.svg")
	aboutHero.ImageAlt = w.word("Original editorial culture artwork", "رسم تحريري أصلي للثقافة")
	aboutHero.Variant = "split"
	topicsHero := w.section("hero", "Follow your curiosity", "اتبع فضولك", "Introduce the themes covered by your publication.", "عرّف بالموضوعات التي تغطيها المجلة.")
	return []starterPage{w.page("home", "Home", "الرئيسية", hero, posts, topics, note), w.page("about", "About", "عن المجلة", aboutHero, note, topics), w.page("topics", "Topics", "الموضوعات", topicsHero, topics, posts), w.contact()}
}
