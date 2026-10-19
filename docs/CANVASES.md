# Workspace canvases

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
