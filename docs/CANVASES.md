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

كل نوع بطاقة له شكل مختلف، ومكتوب عليه اسم النوع كمان. البطاقة بتعرض حالة آخر
تشغيل ليها: «في قائمة الانتظار» أو «قيد التشغيل» أو «بانتظارك» أو «تم» أو «فشل». الخطوط بين البطاقات
بتتحرك وقت التشغيل. لوحة **المهام المجدولة** بتجمع جدولة الكانفاس الحالي حسب
الإيجنت، وبتعرض موعد التشغيل الجاي. وعرض **مسارات** بيرتب البطاقات في أربع
أعمدة ثابتة من غير ما يغيّر أماكنها المحفوظة.

## Views, layout and connection ports

The editor offers Workflow, Lanes, Grid and List views. Changing view stores only a
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

Lanes shows the same cards in four fixed columns: inputs (sources, content,
links, checklists, widgets and unattributed notes), instructions and triggers
(prompts, schedules), execution (agents, providers), and results (result cards
and agent-attributed notes). Within a column, cards keep their saved order.
Positions are computed for display only: Lanes never moves or saves a card, and
dragging is disabled as in Grid and List. Edges and run state draw as in
Workflow. Below 768 pixels Lanes renders as a list ordered by column.

## Node shapes, run state and live edges

Each card kind has its own shape and colour, such as stacked sheets for sources,
a folded corner for prompts, an avatar badge for agents and a clock badge for
schedules. The kind is still written on the card, so shape is never the only
cue. Edit, remove and move controls appear when you hover over or focus a card;
on touch screens and below 768 pixels they stay visible.

A node's run state comes from its latest run in the run history. It reads
`idle`, `queued`, `running`, `waiting`, `done` or `failed`, shown as a dot plus
a word in the card heading. `done` shows for 60 seconds after success, then the
card returns to `idle`. A running agent shows elapsed time, not invented
progress. A waiting node offers its review action inline, labelled with the
card title. State changes are announced once through a polite live region; the
initial page load announces nothing.

Edges show direction with an arrowhead. An edge into a running node animates,
an edge leaving an enabled schedule is dotted, and an edge into a failed node
is dashed red with a × mark. With reduced motion there is no animation; the
stroke pattern and the node's state word still carry the meaning.

While any run on the board is queued or running, the editor refreshes the run
history every 4 seconds. It stops when no run is active and pauses while the
tab is hidden. Refreshing changes only state attributes and keeps keyboard
focus. It adds no endpoint and never starts or changes a run.

## Scheduled tasks panel

When the board has schedule cards, a **Scheduled tasks** panel floats over the
Workflow and Lanes views (in Lanes it sits at the bottom so column headers stay
readable). It starts collapsed, showing the count and the next run. Expanded,
it groups schedules by the agent or provider each one is connected to, with
the interval, status, next run time and a **Show on board** button that
focuses the card. Unconnected schedules are listed under **Not connected**.
Schedules that need review offer **Review and enable**, which opens the same
review as the schedule card; the panel adds no new way to enable a schedule.

The panel covers the current board only. `Escape` collapses it and returns
focus to its toggle. Its open or closed state is a browser-tab preference.
On touch screens and below 768 pixels it sits under the toolbar instead of
floating.

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

## Reviewed workflow suggestions

The canvas list can propose private workflow structures using the configured writing AI. A visible request preview and a separate confirmation precede the provider call; applying a suggestion creates an unbound private board with schedules disabled. See [Reviewed AI canvas suggestions](CANVAS-AI-SUGGESTIONS.md) for the exact context, consent expiry and configuration-change safeguards.

### Reading notes

Notes use literal text with preserved line breaks. Long notes have a bounded preview
and a **Read full note** dialog with the complete text. Opening or closing the
reader does not edit or save the canvas, move cards, or execute an agent. The full
original text remains available in the existing editor.

An attributed-note label and accent follow recorded server provenance. They show
where the note came from, not approval, verified accuracy, or current agent
availability. Personal notes keep a separate visual treatment. No HTML or Markdown
from note bodies is executed or interpreted, and URLs in note text stay plain text.

Card type labels and restrained accents distinguish instructions, content and source
references, executors, checklists, links, indicators, schedules and private results.
Type color is not an availability, approval or trust status. Existing action labels
and checks are unchanged. Footers wrap so editing, removal, movement and available
result actions remain reachable on compact cards.

Connection dots mark the actual endpoints of each drawn edge, with separate ports
for multiple links. They are visual markers, not new clickable connection controls.
Use the existing Connections controls to add or remove an edge. Workflow layout
uses measured card heights; Grid and List provide reading alternatives on narrow
screens. Explicit Arrange cards saves positions without changing content or grants.


## Search, local recovery and edit history

The canvas list and editor include **Search canvases** (`Cmd/Ctrl+K`, including the physical K key with an Arabic keyboard). Search covers your own board titles, card titles, note/prompt bodies and checklist text. Arabic spelling variants, marks, tatweel and Arabic/Persian digits match equivalent queries; stored text is unchanged. Results show original-text excerpts and open the matching card directly. Linked content bodies, provider settings, run documents and grants are excluded. The title filter on the list remains available.

Canvas editing has local **Undo** and **Redo** controls (`Cmd/Ctrl+Z`, `Cmd/Ctrl+Shift+Z`, or `Ctrl+Y` outside text fields). Create/edit/delete, checklist changes, connections, moves and Arrange are logical steps. Automatic collision resolution belongs to the edit that caused it. Undo checks the current server version before applying and uses the same versioned, audited save path. A newer agent/tab write blocks the inverse rather than overwriting that write. History is local to the current editor visit, with up to 100 steps. A generated result can be put back before its deletion is saved; after acknowledgment, the result and its run identity cannot be restored by Undo. The removal confirmation explains this boundary.

Before a debounced save starts, the editor writes a recovery journal in this browser, scoped by origin, signed-in account, board and document lifetime. A crash, failed save or conflict does not automatically replay edits or run anything. Reopening the editor offers **Restore edits**, **Create personal copy**, **Download JSON** and **Discard local copy**. A restore checks the latest saved version; if it changed, keep your text as a personal copy. A personal copy keeps text and layout, converts results and connected tool/reference cards to ordinary notes, and removes connections, run identities and execution settings. No grants, schedules, inbound credentials or provenance are copied. The saved source board stays unchanged. Creating the copy is one atomic, audited server operation.

Journals are retained for up to seven days, with a 256 KiB entry limit and limits of 20 entries / 2 MiB per account and origin. Unexpired copies from other tabs are never silently evicted. If quota, size or browser settings prevent durable storage, the editor keeps the current in-memory text and offers a download; leaving that page can still lose that in-memory copy. Storage is local to the browser and is not an encrypted backup or cross-device sync. A stale save acknowledgment or stale discard action cannot erase a newer revision. Recovery downloads contain document data only and do not import or execute themselves.

### العربية

تتيح قائمة اللوحات والمحرر البحث في اللوحات والكروت عبر `Cmd/Ctrl+K`، مع مطابقة اختلافات الكتابة والتشكيل والأرقام العربية دون تغيير النص الأصلي. يعرض البحث لوحات الحساب الحالي فقط، ولا يفهرس أسرار الأدوات أو سياقات التشغيل أو نصوص المحتوى المرتبط.

التراجع والإعادة خاصان بزيارة المحرر الحالية، ويشملان التحريك والترتيب والتوصيلات وتعديلات الكروت العادية. يتحقق المحرر من نسخة السيرفر قبل تنفيذ التراجع؛ إذا تغيّرت يتوقف لحماية عمل التبويب أو الوكيل الآخر. بعد حفظ حذف نتيجة مُولَّدة، لا يمكن إعادة صلتها بالتشغيل عن طريق التراجع.

تُحفظ التعديلات المحلية في المتصفح قبل بدء الحفظ المؤجل، مع عزل الحساب واللوحة والتبويب. الاستعادة قرار صريح ولا تُشغّل أو تنشر أي شيء. عند اختلاف النسخة المحفوظة يمكن إنشاء نسخة شخصية آمنة أو تنزيل JSON؛ تتحول الأدوات والنتائج والمراجع فيها إلى ملاحظات وتُزال التوصيلات وإعدادات التشغيل. مدة الاحتفاظ تصل إلى ٧ أيام، بحد ٢٥٦ كيلوبايت للنسخة و٢٠ نسخة / ٢ ميجابايت للحساب على هذا الموقع. عند تعذر التخزين تظهر رسالة لتنزيل العمل قبل المغادرة.
