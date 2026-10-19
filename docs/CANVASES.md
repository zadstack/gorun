# Workspace canvases

The **Agents** rail on the opposite side of the admin navigation opens your saved
agent and model cards across your own canvases. Its read-only drawer distinguishes
the canvas card ID from the selected agent key and shows the effective saved
instructions and model only when the current graph passes preparation checks.
“Configured” still requires review before running; it does not mean an external
client is online. Opening the drawer never runs an action or publishes content.
External clients must remain connected and polling. The drawer does not assemble
or verify Skills or MCP servers. It is hidden in the simple workspace and requires
content-edit permission; source context and credentials are excluded.

Open **Dashboard → Canvases** (`/admin/canvases`). Create a named canvas, then add
notes, checklists, links, article/page references, or operational widgets.
Canvases are private to the signed-in user on the current site.

- Drag a card by its handle, or use its arrow buttons / keyboard arrows.
- On mobile, cards appear as a list; up/down controls change their order.
- Article/page cards use a searchable picker and link to the native editor.
- Widgets show customer sites, service requests, pending agent approvals and
  background jobs, when the corresponding module and permission are available.
  Use Refresh for current figures or open the source screen to take action.
- Changes save automatically. **Save and close** returns to the dashboard;
  reopening the canvas restores its cards and positions. Closing is not deletion.
- A conflict means another tab saved a newer version. The editor keeps your
  unsaved work visible and offers reload with confirmation instead of overwriting.
- If saving fails, remain on the page and retry. A failed close keeps the editor open.

Service-request approval does not itself mean payment was received. Agent widgets
show pending approvals, not online agent status. Widgets do not execute tools,
change billing, publish content, or trigger external services.

Limits: 100 canvases per user, 100 cards per canvas, 30 checklist entries per card,
256 KiB per saved document. Titles allow 120 UTF-8 bytes and note bodies 4,000.
Links accept HTTP(S) or an absolute local path. Content references and widgets
are checked against current permissions on every server request.

## بالعربي

من **لوحة التحكم ← اللوحات** أنشئ كانفاس باسم واضح. أضف ملاحظات وقوائم مهام
وروابط ومقالات وصفحات، ومعها ودجات العملاء وطلبات الخدمة وموافقات الإيجنتس.
الودجات المتاحة تعتمد على صلاحياتك والوحدات الموجودة في الموقع.

حرّك البطاقات بالماوس أو أزرار الاتجاهات. على الموبايل تظهر كقائمة قابلة للترتيب.
الحفظ تلقائي، و«احفظ وأغلق» يرجعك للداشبورد من غير حذف الكانفاس. افتحه من
القائمة لتكمل مكان ما توقفت. كل كانفاس خاص بحساب صاحبه؛ المشاركة بين الفريق
ليست جزءًا من هذه النسخة.

## Views, layout and connection ports

The editor offers Workflow, Grid and List views. Changing view stores only a
browser-tab preference and does not submit a document update. Grid and List
keep editing available and hide movement controls and graph lines. Workflow
keeps the saved connections visible and gives each link a separate port on the
appropriate card side, with orthogonal routing around card boundaries.

Arrange cards explicitly saves a layout based on measured card heights: inputs,
execution nodes, then returned notes and review checklists. Existing overlaps
are separated locally for display; loading or resizing alone does not submit a
save. A subsequent content edit can save those repaired coordinates as part of
that edit. Dragging and keyboard moves do not allow cards to overlap. Mobile
keeps the stacked layout and 44-pixel movement controls. These actions do not
change card content, graph edges, grants or agent authority.

If the bounded router cannot find a clear path, the editor shows a spacing
warning rather than drawing through a card. Arrange cards to provide more room.

## Agent capability scope

GoRun currently supports external named agents and managed text generation with
a linked model. It does not yet host a general agent bundle that loads Agent
Skills and maintains multiple external MCP-server clients. A future visual agent
area must label configuration separately from verified readiness and must not
infer runtime availability from an API key or a drawn connection.

## Review a text result as content

An attributed text result from a completed agent or provider run offers **Create
content draft**. Select a public content type and confirm. GoRun creates a new
draft owned by you; it does not publish, replace an existing article, or change
the source card. Text is escaped as plain text rather than trusted as HTML.
Required fields and normal content validation still apply. Media captions and
unattributed personal notes are not supported by this action.

The content save, revision, audit and source snapshot are committed together.
The snapshot records the board, card, run, actor and text, and survives deletion
of the source board. Repeating the action opens the original item, preserving
any edits made in the content editor. If that item has been deleted permanently,
the action reports an error rather than silently replacing it.

## Reuse a private workflow structure

Open **Your workflow templates** in a board and choose **Save structure as
template**. The safety confirmation explains what is retained: card types,
positions and connections. The template library is also available on the board
list. Each template belongs to its creator; other users cannot list, reuse or
delete it. Deleting a template does not delete boards created from it.

Templates are deliberately structural. Free text (including prompts, notes and
card titles), URLs, content selections, agent/provider/skill bindings, checklist
entries, generated outputs and run identifiers are not copied. This avoids
carrying credentials or customer data embedded in those fields into a new
workflow. Result/link/widget cards become empty notes. Card IDs are regenerated
and connections are remapped on both save and reuse.

**Create board** makes a separate private board. Enter new instructions and
select content, agents and providers explicitly. No run history, sharing grants,
consent records or enabled schedules are inherited. A schedule-shaped card may
remain as part of the structure, but it has no active automation until the new
workflow is configured, reviewed and explicitly enabled.

## Incoming events on agent and provider cards

At the top of each agent/provider card, **Card ID** identifies that particular
canvas node. **Agent ID** identifies the selected agent-key record; it is not the
API key value. **Provider ID** identifies a selected generation binding. A model
name is shown only when the backend supplies an actual configured model.

**Incoming events** configures a signed receiver for that card. Enter a matching
command and review the saved instructions. With one connected prompt card, the
dialog edits that existing card; with multiple prompts, edit them separately.
The card preview uses the backend's effective saved instructions when available,
including preset instructions. A clearly labeled connected-card fallback is
shown while effective instructions are unavailable. The final action review
always comes from the backend and shows the actual destination, instructions
and context.

**Save disabled** never enables automation. **Review action** prepares a review
bound to this rule and revision; the explicit checkbox and **Enable reviewed
action** authorize matching events. Changes to the rule, workflow or authority
can require a new review. **Disable** stops new accepted runs. Rotating the
signing secret disables the rule and invalidates the previous secret. The new
secret appears once, is cleared from the dialog on close, and is never embedded
in the public URL. Reopen settings to inspect the command/status; rotate if the
signing secret was lost.

The sender must implement GoRun's [signed command protocol](CANVAS-INBOUND-WEBHOOKS.md).
A URL or endpoint ID alone cannot authorize a request, and arbitrary webhook
provider payloads are not accepted. Accepted events queue the reviewed canvas
action and its private note result; they do not publish content. An external
named agent requires its MCP client to remain running and poll for work.
**Enabled** describes the event rule, not an online or verified agent.

Structural workflow templates do not copy receivers, signing secrets, enabled
rules or consent. Configure and review a new receiver for each reused workflow.
