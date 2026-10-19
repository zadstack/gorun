# Solo operator workflows (MCP-only)

> **بالعربية.** شغّل الشركة كفرد واحد عبر **GoRun MCP** و**Canvases** فقط. لا تعتمد على custom connections أو تكاملات Studio إضافية للتشغيل اليومي: الوكيل الخارجي (Cursor، Claude Desktop، …) يتصل بـ `POST /mcp` بمفتاح **وكيل ذكاء اصطناعي** وينفّذ الـ playbook. النشر والإعدادات الحساسة تبقى behind **الموافقات**.

## Principle

| Do | Avoid |
| --- | --- |
| AI agent key with minimal scopes | `*` scope on production keys |
| `canvases.*` + `get_site_structure` / `list_content` / `get_logs` | New custom connection types for core ops |
| Canvas **Workflow** view + explicit grants | Assuming a key can run without board grants |
| Human approval for publish/settings | Unattended `publish_content` |

## Discover starters

MCP resource (requires `content.view`):

```text
gorun://operator-workflows
```

It lists three starters: **morning_ops**, **lead_draft_reply**, **weekly_editorial**. Each entry includes:

- Suggested canvas card kinds and edges (roles, not live IDs)
- Minimum agent key scopes
- MCP tools to use
- `admin_setup` (human in the admin UI)
- `mcp_playbook` (external agent steps)
- `human_gates` (what must stay manual)

Also read `gorun://site` before changing content.

## Minimal agent keys (examples)

| Workflow | Scopes |
| --- | --- |
| Morning digest | `content.view`, `canvas.read`, `canvas.run`, `canvas.append`, `system.health` |
| Lead reply note | `content.view`, `canvas.read`, `canvas.run`, `canvas.append` |
| Weekly editorial | above + `content.create` (publish still needs approval) |

Create keys under **AI Agents → API keys**. Configure the MCP client with `Authorization: Bearer gp_…` to `https://<site>/mcp`.

## Human steps (once per workflow)

1. **Dashboard → Canvases** — new board, Workflow view, match the skeleton from the resource.
2. Fill **prompt** text (use `prompt_template` from the resource as a starting point).
3. Select **agent** card → your agent key; **Operations → Grants** for read/append/run on prompt and inputs.
4. **Preview → Run** the agent card; only then consider **Schedule** if you accept recurring outbound work.
5. Sensitive MCP calls: decide under **AI Agents → Approvals**; agent polls `check_approval`.

## External agent loop (canvas)

1. `canvases.pending` with `board_id`
2. For each run: `canvases.context` → produce text → `canvases.append_result` with `context_token`
3. Never publish unless the owner explicitly approved via the approvals UI or a separate approver key

Details: [MCP.md](MCP.md), [CANVASES.md](CANVASES.md), [AGENT-MODELS.md](AGENT-MODELS.md).

## العربية — ملخص سريع

- **كل التكامل عبر MCP** على موقع GoRun؛ بدون custom connections للتشغيل الأساسي.
- اقرأ **`gorun://operator-workflows`** لاقتراحات flows جاهزة (هيكل لوحة + scopes + خطوات).
- **أنت** تنشئ اللوحة وتمنح الصلاحيات وتفعّل الجدولة؛ **الوكيل** ينفّذ `canvases.*` ويقترح مسودات.
- **النشر والإعدادات** = موافقة بشرية دائمًا.
