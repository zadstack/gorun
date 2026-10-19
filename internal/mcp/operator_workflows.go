package mcp

// Operator workflow starters for solo operators. Catalog is static JSON exposed
// as gorun://operator-workflows; agents read it over MCP and humans configure
// canvases in the admin. No custom connection types or bundled integrations.

type operatorWorkflowCatalog struct {
	Version  int                 `json:"version"`
	Policy   string              `json:"policy"`
	Workflows []operatorWorkflow `json:"workflows"`
}

type operatorWorkflow struct {
	ID              string                    `json:"id"`
	Title           map[string]string         `json:"title"`
	Description     map[string]string         `json:"description"`
	AgentKeyScopes  []string                  `json:"agent_key_scopes"`
	MCPTools        []string                  `json:"mcp_tools"`
	Canvas          operatorWorkflowCanvas    `json:"canvas"`
	PromptTemplate  map[string]string         `json:"prompt_template"`
	AdminSetup      []string                  `json:"admin_setup"`
	MCPPlaybook     []string                  `json:"mcp_playbook"`
	HumanGates      []string                  `json:"human_gates"`
}

type operatorWorkflowCanvas struct {
	Cards []operatorWorkflowCard `json:"cards"`
	Edges []operatorWorkflowEdge `json:"edges"`
}

type operatorWorkflowCard struct {
	Role string `json:"role"`
	Kind string `json:"kind"`
}

type operatorWorkflowEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func operatorWorkflowCatalogData() operatorWorkflowCatalog {
	return operatorWorkflowCatalog{
		Version: 1,
		Policy: "Integrations are MCP-only: connect an external agent to POST /mcp with an AI agent key. " +
			"Use native canvas runs and canvases.* tools; do not rely on custom site connection types for core ops. " +
			"Publishing, settings changes and other sensitive MCP tools require human approval unless explicitly scoped otherwise.",
		Workflows: []operatorWorkflow{
			{
				ID: "morning_ops",
				Title: map[string]string{
					"en": "Morning operations digest",
					"ar": "ملخص تشغيل صباحي",
				},
				Description: map[string]string{
					"en": "Summarize pending agent approvals, failed jobs and recent content into one private canvas note.",
					"ar": "تلخيص موافقات الوكلاء والمهام الفاشلة وآخر المحتوى في ملاحظة خاصة على اللوحة.",
				},
				AgentKeyScopes: []string{
					"content.view", "canvas.read", "canvas.run", "canvas.append", "system.health",
				},
				MCPTools: []string{
					"get_site_structure", "get_logs", "list_content", "canvases.pending",
					"canvases.context", "canvases.append_result",
				},
				Canvas: operatorWorkflowCanvas{
					Cards: []operatorWorkflowCard{
						{Role: "widgets", Kind: "widget"},
						{Role: "source", Kind: "source"},
						{Role: "prompt", Kind: "prompt"},
						{Role: "agent", Kind: "agent"},
						{Role: "checklist", Kind: "checklist"},
					},
					Edges: []operatorWorkflowEdge{
						{From: "widgets", To: "prompt"},
						{From: "source", To: "prompt"},
						{From: "prompt", To: "agent"},
						{From: "agent", To: "checklist"},
					},
				},
				PromptTemplate: map[string]string{
					"en": "Write a concise morning brief for the site owner: top 3 priorities, blocked approvals, failed jobs, and content gaps. Plain text only; no publishing actions.",
					"ar": "اكتب موجزًا صباحيًا مختصرًا لصاحب الموقع: أهم 3 أولويات، الموافقات المعلّقة، المهام الفاشلة، وثغرات المحتوى. نص عادي فقط؛ بدون أي نشر.",
				},
				AdminSetup: []string{
					"Create a canvas in Workflow view; add widget cards (approvals, jobs) and a source card for latest posts.",
					"Connect prompt → agent; grant the agent key read/append/run on prompt and connected inputs.",
					"Owner reviews and runs the agent card once; optional daily schedule only after explicit enable.",
				},
				MCPPlaybook: []string{
					"Read gorun://operator-workflows and gorun://site.",
					"Poll canvases.pending for waiting_agent runs on the configured board_id.",
					"For each assigned run: canvases.context → work → canvases.append_result with the brief.",
					"Use get_logs for audit tail and list_content for recent drafts; never call publish_content without approval flow.",
				},
				HumanGates: []string{
					"Initial canvas run review", "Optional schedule enable", "Any publish_content or update_settings",
				},
			},
			{
				ID: "lead_draft_reply",
				Title: map[string]string{
					"en": "Lead → draft reply note",
					"ar": "عميل محتمل → مسودة رد",
				},
				Description: map[string]string{
					"en": "Turn a form or intake signal into a reviewed reply draft on the canvas (not sent automatically).",
					"ar": "تحويل إشارة من نموذج أو طلب إلى مسودة رد على اللوحة (لا تُرسل تلقائيًا).",
				},
				AgentKeyScopes: []string{
					"content.view", "canvas.read", "canvas.run", "canvas.append",
				},
				MCPTools: []string{
					"canvases.context", "canvases.append_result", "list_content", "get_content",
				},
				Canvas: operatorWorkflowCanvas{
					Cards: []operatorWorkflowCard{
						{Role: "prompt", Kind: "prompt"},
						{Role: "agent", Kind: "agent"},
						{Role: "checklist", Kind: "checklist"},
					},
					Edges: []operatorWorkflowEdge{
						{From: "prompt", To: "agent"},
						{From: "agent", To: "checklist"},
					},
				},
				PromptTemplate: map[string]string{
					"en": "Draft a short professional reply and a follow-up checklist. Do not send email or change site settings.",
					"ar": "اكتب مسودة رد مهني قصير وقائمة متابعة. لا ترسل بريدًا ولا تغيّر إعدادات الموقع.",
				},
				AdminSetup: []string{
					"Configure incoming events on the agent card if webhooks should enqueue runs.",
					"Paste lead details into the prompt card or connect an allowed source; review before run.",
				},
				MCPPlaybook: []string{
					"On canvases.pending: load context, produce reply text, append_result.",
					"Optional: create_content as draft only if content.create scope is granted and owner wants a CRM-style record.",
				},
				HumanGates: []string{
					"Owner sends the actual reply", "create_content and publish_content if used",
				},
			},
			{
				ID: "weekly_editorial",
				Title: map[string]string{
					"en": "Weekly editorial draft",
					"ar": "مسودة تحريرية أسبوعية",
				},
				Description: map[string]string{
					"en": "Outline and draft a post as native content draft via MCP; publish only after human approval.",
					"ar": "Outline ثم مسودة مقال عبر MCP؛ النشر فقط بعد موافقة بشرية.",
				},
				AgentKeyScopes: []string{
					"content.view", "content.create", "canvas.read", "canvas.run", "canvas.append",
				},
				MCPTools: []string{
					"list_content", "create_content", "canvases.context", "canvases.append_result", "check_approval",
				},
				Canvas: operatorWorkflowCanvas{
					Cards: []operatorWorkflowCard{
						{Role: "source", Kind: "source"},
						{Role: "prompt", Kind: "prompt"},
						{Role: "agent", Kind: "agent"},
						{Role: "note", Kind: "note"},
					},
					Edges: []operatorWorkflowEdge{
						{From: "source", To: "prompt"},
						{From: "prompt", To: "agent"},
						{From: "agent", To: "note"},
					},
				},
				PromptTemplate: map[string]string{
					"en": "Propose one article topic aligned with recent posts, then write a full draft (title + body) suitable for create_content as draft.",
					"ar": "اقترح موضوع مقال واحد متسقًا مع آخر المنشورات، ثم اكتب مسودة كاملة (عنوان + متن) مناسبة لـ create_content كمسودة.",
				},
				AdminSetup: []string{
					"Connect source to recent posts; link agent key with content.create.",
					"Prefer canvas note for brainstorming; use create_content for the actual draft record.",
				},
				MCPPlaybook: []string{
					"list_content for context → append_result with outline on canvas.",
					"create_content with status draft; if publish needed, call publish_content and poll check_approval.",
				},
				HumanGates: []string{
					"publish_content always", "Editing live content via update_content when item is published",
				},
			},
		},
	}
}
