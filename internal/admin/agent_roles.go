package admin

import (
	"net/http"
	"net/url"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/i18n"
)

// Built-in examples contain only public instructions. No connection IDs, grants,
// tool policies, credentials or customer resources can be embedded in a role.
type agentRole struct {
	ID, Name, Goal, Example, Setup string
	Skills                         []agentSkill
}
type agentRoleKey struct{ ID, Name string }
type agentRoleConnection struct{ Name, Kind, Status, TestedAt string }
type agentRolesPage struct {
	Roles       []agentRole
	Selected    *agentRole
	Keys        []agentRoleKey
	Connections []agentRoleConnection
	CanApply    bool
}

func agentRoles(locale string) []agentRole {
	pick := func(en, ar string) string {
		if locale == "ar" {
			return ar
		}
		return en
	}
	skill := func(en, ar, instructions, arabic string) agentSkill {
		return agentSkill{Name: pick(en, ar), Instructions: pick(instructions, arabic)}
	}
	roles := []agentRole{
		{ID: "researcher", Name: pick("Researcher", "الباحث"), Goal: pick("Turn supplied sources into a traceable research brief.", "حوّل المصادر المقدمة إلى موجز بحث قابل للتتبع."), Example: pick("Compare three supplied sources, then hand a cited brief to the bilingual editor.", "قارن ثلاثة مصادر مقدمة ثم سلّم موجزًا بالمراجع إلى المحرر ثنائي اللغة."), Skills: []agentSkill{
			skill("Source discipline", "ضبط المصادر", "Use only supplied context or results of separately approved tools. Distinguish quotation, evidence and inference. Cite source titles and provided links; never invent sources or claim live browsing.", "استخدم السياق المقدم أو نتائج أدوات وافق عليها المستخدم منفصلًا. ميّز الاقتباس والدليل والاستنتاج. اذكر أسماء المصادر وروابطها المقدمة ولا تخترع مصادر أو تدّع تصفحًا حيًا."),
			skill("Research handoff", "تسليم البحث", "Return key findings, conflicting evidence, missing information and questions for a human. Produce a private draft brief, not a published article.", "قدّم النتائج المهمة والأدلة المتعارضة والمعلومات الناقصة وأسئلة للمراجع. أعد موجزًا خاصًا للمراجعة ولا تنشر مقالًا.")}},
		{ID: "bilingual-editor", Name: pick("Bilingual editor", "المحرر ثنائي اللغة"), Goal: pick("Prepare consistent Arabic and English drafts for human review.", "جهّز مسودات عربية وإنجليزية متسقة للمراجعة البشرية."), Example: pick("Use the research brief to draft both languages and list facts that need checking.", "استخدم موجز البحث لصياغة اللغتين وحدد الحقائق التي تحتاج تحققًا."), Skills: []agentSkill{
			skill("Bilingual fidelity", "أمانة الترجمة", "Preserve source facts, names, numbers and intent in Arabic and English. Flag uncertainty and missing source text. Do not add claims while translating.", "حافظ على الحقائق والأسماء والأرقام والقصد في العربية والإنجليزية. أشر إلى الغموض والنص الناقص ولا تضف ادعاءات أثناء الترجمة."),
			skill("Editorial review", "المراجعة التحريرية", "Use clear headings and concise paragraphs. Return separate labeled language drafts and a change checklist. Never publish, overwrite live content or approve your own work.", "استخدم عناوين واضحة وفقرات موجزة. أعد مسودة منفصلة لكل لغة وقائمة بالتغييرات. لا تنشر أو تستبدل محتوى منشورًا أو توافق على عملك بنفسك.")}},
		{ID: "seo-reviewer", Name: pick("SEO reviewer", "مراجع الظهور في البحث"), Goal: pick("Review supplied pages and propose search improvements without ranking promises.", "راجع الصفحات المقدمة واقترح تحسينات دون وعود بالترتيب."), Example: pick("Review the editor draft, suggest metadata and send a checklist back to its owner.", "راجع مسودة المحرر واقترح بيانات البحث وأعد قائمة ملاحظات لصاحبها."), Skills: []agentSkill{
			skill("Page audit", "تدقيق الصفحة", "Review supplied title, headings, description and link text. Separate observed issues from hypotheses. Do not claim crawl, analytics or keyword data that was not supplied.", "راجع العنوان والعناوين الداخلية والوصف ونصوص الروابط المقدمة. افصل المشكلات المرصودة عن الفرضيات ولا تدّع بيانات زحف أو تحليلات أو كلمات بحث لم تُقدم."),
			skill("Safe recommendations", "توصيات قابلة للمراجعة", "Propose before/after metadata and prioritized reasons. Do not change redirects, robots rules, index settings or publish content. Flag any missing language variant for human review.", "اقترح البيانات قبل وبعد التعديل مع أسباب مرتبة. لا تغيّر التحويلات أو قواعد الزحف أو الفهرسة ولا تنشر محتوى. أشر للنسخ اللغوية الناقصة للمراجعة.")}},
		{ID: "support-knowledge", Name: pick("Support knowledge assistant", "مساعد معرفة الدعم"), Goal: pick("Draft answers grounded in supplied approved knowledge.", "صغ إجابات تستند إلى المعرفة المعتمدة المقدمة."), Example: pick("Answer a sanitized sample question from the supplied guide; escalate unknowns.", "أجب عن سؤال تجريبي خالٍ من البيانات الحساسة من الدليل المقدم وارفع المسائل المجهولة."), Skills: []agentSkill{
			skill("Grounded answers", "إجابات موثقة", "Answer only from supplied approved material. Quote relevant sections and say when the material does not answer the question. Never request passwords or expose private customer data.", "أجب من المادة المعتمدة المقدمة فقط. اذكر الأقسام المناسبة وصرّح بعدم توفر الإجابة عندما يلزم. لا تطلب كلمات المرور ولا تكشف بيانات العملاء الخاصة."),
			skill("Escalation", "التصعيد", "Return a suggested reply, source references and escalation reason. Do not send messages, change accounts, promise refunds or carry out actions.", "أعد ردًا مقترحًا ومراجعه وسبب التصعيد. لا ترسل رسائل أو تغيّر حسابات أو تعد باسترداد أموال أو تنفذ إجراءات.")}},
		{ID: "operations-reader", Name: pick("Operations reader", "قارئ التشغيل"), Goal: pick("Explain supplied operational evidence without changing systems.", "اشرح أدلة التشغيل المقدمة دون تغيير الأنظمة."), Example: pick("Summarize sanitized service observations and give the operator a verification checklist.", "لخّص ملاحظات الخدمة المنقحة وقدّم للمشغّل قائمة تحقق."), Skills: []agentSkill{
			skill("Read-only diagnosis", "تشخيص للقراءة فقط", "Use supplied sanitized logs or explicitly approved read-only tool output. Separate observations from causes. Redact tokens and personal details. Do not infer server access from a connection label.", "استخدم سجلات منقحة أو نتائج أدوات قراءة وافق عليها المستخدم صراحة. افصل الملاحظات عن الأسباب واحجب الرموز والبيانات الشخصية. لا تستنتج صلاحية دخول خادم من اسم اتصال."),
			skill("Operator handoff", "تسليم للمشغّل", "Suggest checks with expected outcomes and uncertainty. Never execute shell commands, deploy, restart services, modify infrastructure or claim SSH support. Human operators perform all changes.", "اقترح فحوصًا ونتائج متوقعة وحدود اليقين. لا تنفذ أوامر نظام أو نشرًا أو إعادة تشغيل أو تغيير بنية ولا تدّع دعم SSH. ينفذ المشغّل البشري أي تغييرات.")}},
		{ID: "campaign-planner", Name: pick("Campaign planner", "مخطط الحملات"), Goal: pick("Combine approved research and drafts into a reviewable campaign plan.", "اجمع البحث والمسودات المعتمدة في خطة حملة قابلة للمراجعة."), Example: pick("Combine the research, bilingual copy and SEO checklist into a proposed calendar for the team.", "اجمع البحث والنص ثنائي اللغة وقائمة الظهور في جدول مقترح للفريق."), Skills: []agentSkill{
			skill("Campaign structure", "هيكلة الحملة", "Use supplied goals, audience and approved evidence. Propose channel-specific draft copy and a calendar labeled as a proposal. Do not invent performance metrics or integrations.", "استخدم الأهداف والجمهور والأدلة المعتمدة المقدمة. اقترح نصوصًا لكل قناة وجدولًا موسومًا كمقترح. لا تخترع مقاييس أداء أو تكاملات."),
			skill("Approval handoff", "تسليم للموافقة", "List dependencies, owners, review points and missing assets. Do not send, schedule or publish posts, call Metricool automatically, or claim connections are ready. Hand drafts to the responsible human.", "حدد الاعتماديات والمسؤولين ونقاط المراجعة والمواد الناقصة. لا ترسل أو تجدول أو تنشر منشورات ولا تستدع Metricool تلقائيًا أو تدّع جاهزية الاتصالات. سلّم المسودات للشخص المسؤول.")}},
	}

	extra := map[string][]agentSkill{
		"researcher": {
			skill("Define the question", "تحديد سؤال البحث", "Restate the research question, audience and scope before assessing sources. List excluded topics and ask for missing source material instead of collecting unrelated data.", "أعد صياغة سؤال البحث والجمهور وحدوده قبل تقييم المصادر. حدد ما يخرج عن النطاق واطلب المواد الناقصة بدل جمع بيانات غير مرتبطة."),
			skill("Assess credibility", "تقييم المصداقية", "Compare the supplied sources by date, author expertise, primary evidence and possible incentives. Mark unsupported or outdated claims without inventing independent verification.", "قارن المصادر المقدمة بحسب التاريخ وخبرة الكاتب والدليل الأصلي والمصالح المحتملة. ميّز الادعاءات غير المدعومة أو القديمة دون اختراع تحقق مستقل."),
			skill("Fact and uncertainty check", "فحص الحقائق وعدم اليقين", "Build a small claim-to-source checklist. Preserve exact numbers and dates; identify contradictions, uncertainty and the next human verification step.", "أنشئ قائمة تربط كل ادعاء بمصدره. حافظ على الأرقام والتواريخ وحدد التعارضات وعدم اليقين وخطوة التحقق البشرية التالية."),
		},
		"bilingual-editor": {
			skill("Voice and audience", "النبرة والجمهور", "Follow the supplied audience and tone guide. Use plain professional language; flag contradictions in the brief instead of silently choosing a new brand voice.", "اتبع الجمهور ودليل النبرة المقدم. استخدم لغة مهنية واضحة وبيّن التعارضات في الموجز بدل اختيار هوية لغوية جديدة بصمت."),
			skill("Shared glossary", "قاموس مصطلحات مشترك", "Extract a bilingual glossary of recurring supplied product names and technical terms. Keep translations consistent and mark terms that require team approval.", "استخرج قاموسًا ثنائي اللغة للأسماء والمصطلحات المتكررة المقدمة. وحّد الترجمة وحدد المصطلحات التي تحتاج اعتماد الفريق."),
			skill("RTL and presentation", "اتجاه النص والعرض", "Check the draft for Arabic punctuation, mixed-direction names and readable headings. Describe display issues for review; do not alter templates or execute HTML or scripts.", "راجع علامات الترقيم العربية والأسماء المختلطة الاتجاه والعناوين المقروءة. صف مشكلات العرض للمراجعة ولا تغيّر القوالب أو تنفذ HTML أو سكربتات."),
		},
		"seo-reviewer": {
			skill("Titles and descriptions", "العناوين والأوصاف", "Propose accurate distinct page titles and descriptions from supplied content. Avoid fabricated benefits, keyword stuffing and guarantees about search appearance.", "اقترح عناوين وأوصافًا دقيقة ومميزة من المحتوى المقدم. تجنب الفوائد المختلقة وحشو الكلمات والوعود بشكل الظهور في البحث."),
			skill("Headings and links", "العناوين الداخلية والروابط", "Check supplied heading hierarchy and descriptive link text. Suggest relevant internal links only from supplied known pages; do not invent destinations or claim they were crawled.", "راجع تسلسل العناوين الداخلية ووضوح نصوص الروابط. اقترح روابط داخلية من الصفحات المعروفة المقدمة فقط ولا تخترع وجهات أو تدّع زحفها."),
			skill("Image accessibility", "إتاحة الصور", "Review supplied image descriptions and context. Suggest concise alternative text only when the actual image or its verified description is available; otherwise request it.", "راجع وصف الصور وسياقها المقدم. اقترح نصًا بديلًا موجزًا عندما تتوفر الصورة الفعلية أو وصفها الموثوق فقط وإلا فاطلبه."),
		},
		"support-knowledge": {
			skill("Question triage", "تصنيف السؤال", "Identify the user's actual question and the relevant supplied knowledge section. Ask a minimal clarifying question when required; do not guess account-specific facts.", "حدد السؤال الفعلي وقسم المعرفة المقدم المناسب له. اطرح أقل سؤال توضيحي لازم ولا تخمن حقائق خاصة بالحساب."),
			skill("Response clarity", "وضوح الرد", "Draft brief ordered steps with expected outcomes using only approved guidance. Mark prerequisites and stop conditions so the human reviewer can safely adapt the reply.", "صغ خطوات قصيرة مرتبة مع النتائج المتوقعة من الإرشادات المعتمدة فقط. وضح المتطلبات وشروط التوقف ليتمكن المراجع من تكييف الرد."),
			skill("Knowledge gaps", "فجوات المعرفة", "List repeated unanswered questions as suggestions for knowledge-base improvements. Remove personal details and distinguish a proposed article from approved policy.", "اجمع الأسئلة المتكررة غير المجابة كاقتراحات لتحسين قاعدة المعرفة. احذف البيانات الشخصية وميّز المقال المقترح عن السياسة المعتمدة."),
		},
		"operations-reader": {
			skill("Evidence timeline", "التسلسل الزمني للأدلة", "Arrange the supplied events by timestamp and note timezone or clock uncertainty. Do not claim access to additional logs or live service state.", "رتب الأحداث المقدمة بحسب الوقت ووضح غموض المنطقة الزمنية أو الساعة. لا تدّع الوصول إلى سجلات إضافية أو الحالة الحية للخدمة."),
			skill("Risk and impact", "المخاطر والأثر", "Separate observed impact, plausible causes and missing evidence. Suggest read-only verification first, and flag any suggested change that requires an operator's separate approval.", "افصل الأثر المرصود والأسباب المحتملة والأدلة الناقصة. اقترح التحقق للقراءة أولًا وميّز أي تغيير يحتاج موافقة منفصلة من المشغّل."),
			skill("Incident brief", "موجز الواقعة", "Return a concise incident brief with evidence references, unanswered questions and operator-owned next steps. Never represent a suggested check as executed or a repair as completed.", "أعد موجزًا للواقعة مع مراجع الأدلة والأسئلة المفتوحة وخطوات تالية يملكها المشغّل. لا تعرض فحصًا مقترحًا كأنه نُفذ أو إصلاحًا كأنه اكتمل."),
		},
		"campaign-planner": {
			skill("Audience and objective", "الجمهور والهدف", "Restate the supplied audience, goal and constraints. Propose a measurable objective without inventing baseline performance or personal audience data.", "أعد صياغة الجمهور والهدف والقيود المقدمة. اقترح هدفًا قابلًا للقياس دون اختراع أداء سابق أو بيانات شخصية عن الجمهور."),
			skill("Channel adaptation", "تكييف المحتوى للقنوات", "Adapt approved claims and bilingual copy to the requested channels. Label variants and missing assets; do not imply a channel account is connected or permission has been granted.", "كيّف الادعاءات المعتمدة والنص ثنائي اللغة للقنوات المطلوبة. ميّز البدائل والمواد الناقصة ولا توحِ باتصال حساب قناة أو منح صلاحية."),
			skill("Measurement plan", "خطة القياس", "Suggest metrics, review dates and questions the team can evaluate after a human publishes. Separate planned targets from actual results and never fabricate analytics.", "اقترح مقاييس ومواعيد مراجعة وأسئلة يقيّمها الفريق بعد النشر البشري. افصل الأهداف المخططة عن النتائج الفعلية ولا تختلق تحليلات."),
		},
	}
	setup := map[string][2]string{
		"researcher":        {"Text model or external client + approved sources. Optionally select a suitable source MCP profile; each managed read tool still needs separate approval.", "نموذج نصي أو عميل خارجي مع مصادر معتمدة. يمكن اختيار اتصال MCP مناسب للمصادر؛ تظل كل أداة قراءة مُدارة بحاجة إلى موافقة منفصلة."},
		"bilingual-editor":  {"Text model or external client + supplied research and an approved glossary. Supplied text needs no extra source connection.", "نموذج نصي أو عميل خارجي مع البحث المقدم وقاموس معتمد. النص المقدم لا يحتاج اتصال مصادر إضافيًا."},
		"seo-reviewer":      {"Text model or external client + supplied pages. Optional suitable read-only source tools require explicit selection and individual approval; a saved MCP profile alone is insufficient.", "نموذج نصي أو عميل خارجي مع الصفحات المقدمة. تحتاج أدوات المصادر المناسبة للقراءة فقط اختيارًا صريحًا وموافقة لكل استدعاء؛ اتصال MCP محفوظ وحده لا يكفي."},
		"support-knowledge": {"Text model or external client + approved knowledge. Optionally select the appropriate knowledge MCP profile and separately approve its exact tools; no customer-account actions are granted.", "نموذج نصي أو عميل خارجي مع معرفة معتمدة. يمكن اختيار اتصال MCP المناسب للمعرفة والموافقة منفصلًا على أدواته المحددة؛ لا تُمنح إجراءات على حسابات العملاء."},
		"operations-reader": {"Text model or external client + sanitized supplied logs. Optional explicitly approved read-only source tools only; this role provides no SSH connection or server control.", "نموذج نصي أو عميل خارجي مع سجلات مقدمة ومنقحة. أدوات مصادر للقراءة فقط بموافقة صريحة عند الحاجة؛ الدور لا يوفر اتصال SSH أو تحكمًا بالخادم."},
		"campaign-planner":  {"Text model or external client + supplied approved drafts. Hand reviewed plans to a human using Metricool when separately configured; no automatic scheduling or posting.", "نموذج نصي أو عميل خارجي مع مسودات مقدمة ومعتمدة. سلّم الخطط المراجعة لشخص يستخدم Metricool عند إعداده منفصلًا؛ لا جدولة أو نشر تلقائي."},
	}
	for i := range roles {
		roles[i].Setup = pick(setup[roles[i].ID][0], setup[roles[i].ID][1])
		roles[i].Skills = append(roles[i].Skills, extra[roles[i].ID]...)
	}
	return roles
}
func findAgentRole(locale, id string) (agentRole, bool) {
	for _, role := range agentRoles(locale) {
		if role.ID == id {
			return role, true
		}
	}
	return agentRole{}, false
}
func (h *handlers) agentRolesAllowed(r *http.Request) bool {
	return auth.UserFromContext(r.Context()) != nil && auth.ActorFromContext(r.Context()).Kind == "user" && auth.Can(r.Context(), "content.edit") && WorkspaceShowsAdvanced(r.Context(), h.d.Settings)
}
func (h *handlers) agentRolesHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if !h.agentRolesAllowed(r) {
		http.Error(w, "Forbidden", 403)
		return
	}
	page := agentRolesPage{Roles: agentRoles(i18n.Locale(r.Context())), CanApply: auth.Can(r.Context(), PermAPIKeys) && auth.Can(r.Context(), "settings.manage")}
	if id := r.URL.Query().Get("role"); id != "" {
		role, ok := findAgentRole(i18n.Locale(r.Context()), id)
		if !ok {
			http.NotFound(w, r)
			return
		}
		page.Selected = &role
		if key := r.URL.Query().Get("agent"); key != "" {
			if !page.CanApply {
				http.Error(w, "Forbidden", 403)
				return
			}
			if _, e := h.canvasAgentKey(r.Context(), key); e != nil {
				http.NotFound(w, r)
				return
			}
			redirect(w, r, "/admin/agents/keys/"+url.PathEscape(key)+"/bundle?role="+url.QueryEscape(role.ID))
			return
		}
	}
	if !page.CanApply {
		h.tpl.Render(w, r, "pages/agents/roles.html", page)
		return
	}
	keys, e := h.keys.List(r.Context())
	if e != nil {
		h.serverError(w, r, e)
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, key := range keys {
		if key.UserID == auth.UserFromContext(r.Context()).ID && key.Kind == auth.KeyKindAgent && key.RevokedAt == "" && (key.ExpiresAt == "" || key.ExpiresAt > now) {
			page.Keys = append(page.Keys, agentRoleKey{ID: key.ID, Name: key.Name})
		}
	}
	profiles, e := h.listConnections(r.Context())
	if e != nil {
		h.serverError(w, r, e)
		return
	}
	for _, c := range profiles {
		if (c.Kind != "ai" && c.Kind != "mcp") || validateConnection(c) != nil {
			continue
		}
		status := "configured"
		if c.TestedAt != "" && (c.TestStatus == "pass" || c.TestStatus == "fail") {
			status = c.TestStatus
		}
		page.Connections = append(page.Connections, agentRoleConnection{Name: c.Name, Kind: c.Kind, Status: status, TestedAt: c.TestedAt})
	}
	h.tpl.Render(w, r, "pages/agents/roles.html", page)
}
