# Canvas Node Shapes, Live Edges and Schedules Panel Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** On the admin canvas flow view, each node shows its kind by shape and its run state, edges show live data flow,
a floating panel lists this board's scheduled tasks grouped by agent or provider, and a fourth "Lanes" view
arranges nodes in four fixed columns without changing saved positions.

**Architecture:**
- The work is client-side only, in `web/admin/static`. A new pure UMD module, `canvas-state.js`, maps runs, edges and
  schedules to states and groups.
- `canvas-operations.js` owns runs. It applies states to existing card elements as attributes (no re-render), draws edge
  states and polls `/runs` only while a run is active.
- A new `canvas-schedules.js` renders the panel from data `canvas.js` already holds.
- Shapes and the hover toolbar are CSS in `canvas-cards.css`.

**Tech Stack:** plain JS (IIFE/UMD, no build step), CSS, Go html/template, `node --test` for `.cjs` unit tests, Playwright (`e2e/`).

**Spec:** `docs/superpowers/specs/2026-11-03-canvas-node-shapes-design.md`

## Global Constraints

- No Go code, migrations or routes. The only server-side files touched are `internal/admin/catalog/canvasops.{ar,en}.json`.
- Every new label has both `ar` and `en` strings, and is exposed through a `<span data-label=…>` in `editor.html`.
- The kind stays written as text on every card (the existing `.canvas-card-heading small`). Shape and color are never
  the only signal.
- Text contrast is at least 4.5:1. Touch targets are at least 44px under `(max-width:767px), (pointer:coarse)`.
- RTL is the default (`ar`). Use logical properties (`inset-inline-*`, `margin-inline-*`). Wrap titles in `bdi`.
- `prefers-reduced-motion: reduce`: no dash animation and no packets. The state stays visible.
- Card footprint stays `CanvasLayout.WIDTH` (300) wide. `canvas-layout.js` is not modified.
- Grid and list views keep today's look. Shapes, edge states and the floating panel apply to the `[data-view=flow]` desktop
  layout. The panel is docked (not floating) in other views and on mobile.
- Commits use `git -c user.name=ahmedvnabil -c user.email=vacrom414@gmail.com commit -s` on a feature branch, never `main`.
- Delivery is 4 PRs:
  - PR1: Tasks 1–3 (shapes and node state).
  - PR2: Tasks 4–5 (live edges and polling).
  - PR3: Task 6 (schedules panel).
  - PR4: Task 7 (lanes view).
  - Each PR adds one line under `## [Unreleased]` in `CHANGELOG.md`.

## Review Focus

1. A schedule node whose outgoing edge was removed, or whose target card was deleted, must be listed under
   "Not connected", not dropped or thrown on. → Task 1 unit test `scheduleGroups: unconnected`.
2. A `next_run_at` already in the past (the tick has not run yet) must read "due now", not "−3 minutes" or "3 minutes ago".
   → Task 1 unit test `nextRunLabel: past is due`.
3. Polling must never re-render cards. A person dragging or typing on a card while a run is active must not lose the
   element or its focus. → Task 5 e2e `card element survives polling`.
4. The hover toolbar must be reachable by keyboard alone. Tabbing to "Edit" makes it visible, and the card at the top edge
   of the surface does not clip it. → Task 2 e2e `toolbar visible on focus`.
5. Long Arabic or mixed-direction schedule and agent titles must not widen the panel past the viewport.
   → Task 6 e2e `panel has no horizontal overflow` with a 120-byte title.

---

### Task 1: Pure state module

**Files:**
- Create: `web/admin/static/canvas-state.js` (UMD, same wrapper as `canvas-graph.js`, global `CanvasState`)
- Test: `web/admin/static/canvas_state_test.cjs`

**Interfaces:**
- Produces:
  - `nodeStates(runs: Run[], now: number) -> {[nodeId]: 'queued'|'running'|'waiting'|'done'|'failed'}`.
    - `Run` is the `/runs` JSON: `{id,node_id,status,created_at,finished_at}`.
    - `runs` arrive newest first. Only the first run per `node_id` counts.
    - Status mapping:
      - `requested`, `queued` → `queued`
      - `running`, `waiting_agent` → `running`
      - `waiting`, `waiting_tool` → `waiting`
      - `succeeded` with `finished_at` within 60 000 ms of `now` → `done`
      - `failed`, `uncertain` → `failed`
    - Anything else (`reviewed`, `cancelled`, an older `succeeded`) is omitted, which means idle.
  - `doneExpiry(runs, now) -> number|null`: the earliest ms timestamp at which a `done` state lapses, or `null`.
  - `isActive(runs) -> boolean`: true if any latest-per-node run is `queued`, `running` or `waiting_agent`.
  - `edgeStates(cards, edges, states, schedules) -> Array<'idle'|'scheduled'|'flowing'|'failed'>`, aligned with
    `edges`. Precedence:
    1. `flowing` if `states[edge.to]==='running'`.
    2. Else `failed` if `states[edge.to]==='failed'`.
    3. Else `scheduled` if the source card is `kind:'schedule'` and `schedules[edge.from]?.enabled`.
    4. Else `idle`.
  - `scheduleGroups(cards, edges, schedules) -> Array<{target: Card|null, items: Array<{card, state}>}>`.
    - One group per target card, in the order targets first appear in `cards`. The `target:null` group comes last.
    - `state` is `schedules[card.id] || {enabled:false,status:'disabled'}`.
  - `nextRunLabel(iso, now, lang) -> {kind:'in', text}|{kind:'due'}|null`.
    - Returns `null` for an empty or invalid `iso`, and `{kind:'due'}` when `iso <= now`.
    - Otherwise `text` comes from `Intl.RelativeTimeFormat(lang,{numeric:'auto'})`, in the largest unit among
      minute, hour and day.

- [ ] **Step 1: Write failing tests** in `canvas_state_test.cjs` (style of `canvas_graph_test.cjs`):
  - `nodeStates maps every run status`: one run per status → the expected map. `reviewed` and `cancelled` are absent.
  - `nodeStates uses only the newest run per node`: `[{node_id:'a',status:'failed'},{node_id:'a',status:'running'}]` → `{a:'failed'}`.
  - `done lapses after 60s`: `succeeded` finished at `now-59_000` → `done`. At `now-61_000` → absent.
    `doneExpiry` → `finished+60_000`.
  - `isActive`: true for `waiting_agent`; false for `waiting_tool` and `requested`.
  - `edgeStates precedence`: running target beats an enabled schedule source → `flowing`. A disabled schedule → `idle`.
  - `scheduleGroups: unconnected`: a schedule with no edge, and one whose edge targets a missing card id → both in the
    `target:null` group.
  - `scheduleGroups groups two schedules on one agent`, and keeps card order.
  - `nextRunLabel: past is due`: `{kind:'due'}` for `now-1`. `null` for `''` and `'nope'`.
    `'in'` for `now+42*60_000` with `lang:'en'` → text `'in 42 minutes'`.
- [ ] **Step 2: Run** `node --test web/admin/static/canvas_state_test.cjs`. Expected: FAIL (`Cannot find module './canvas-state.js'`).
- [ ] **Step 3: Implement** `canvas-state.js` with the signatures above. `Object.freeze` the export, like `canvas-graph.js`.
- [ ] **Step 4: Run** `node --test web/admin/static/canvas_*_test.cjs`. Expected: all PASS. CI already globs this pattern.
- [ ] **Step 5: Commit** `feat(admin): add canvas run and schedule state model`.

### Task 2: Node shapes and hover toolbar (CSS)

**Files:**
- Modify: `web/admin/static/canvas-cards.css` (append one section)
- Modify: `web/admin/templates/pages/canvases/editor.html:7` (add `<script defer src="{{asset "canvas-state.js"}}"></script>`
  before `canvas-operations.js`)
- Test: `e2e/tests/canvas-node-shapes.spec.ts` (new)

**Interfaces:**
- Consumes: the existing DOM, `article.canvas-card[data-kind]` > `header.canvas-card-heading` / `.canvas-card-body` /
  `footer.canvas-card-footer`.
- Produces: CSS hooks used by Task 3, `.canvas-card[data-state]` and `.canvas-node-state` (dot plus word, in the heading).

- [ ] **Step 1: Write failing e2e tests** (default `ar` locale; board created from the `periodic-brief` preset with an
  agent target, as in `canvas-lifecycle.spec.ts`):
  - `every kind keeps a text label`: for each card, `.canvas-card-heading small` is visible and non-empty.
  - `kinds differ by shape`: the computed `border-radius` of the `agent` card ≠ that of the `source` card. The `prompt`
    card's `::before` has a `clip-path` other than `none`. The `schedule` heading badge has `border-radius: 50%`.
  - `toolbar visible on focus`: in flow view at desktop size, the footer of a card at `y=0` has computed `opacity: 0`.
    After keyboard-focusing its Edit button, it is `1`. Its bounding box is inside the surface's.
  - `grid view keeps the footer`: in `[data-view=grid]` the footer opacity is `1` without focus.
- [ ] **Step 2: Run** `cd e2e && npx playwright test tests/canvas-node-shapes.spec.ts`. Expected: FAIL on the shape and
  opacity assertions.
- [ ] **Step 3: Implement the shapes** in `canvas-cards.css`, scoped to `.canvas-workspace[data-view=flow]`, one rule
  block per kind, following the spec §1 table:
  - Decorative layers go on `::before` and `::after` only. `clip-path` is never on the card itself, so focus outlines
    are not cut off.
  - Retune the colliding tokens: give `provider` and `widget` different lightness, and `prompt` and `result` likewise.
    Check each `--card-label` against `--card-soft` at 4.5:1.
- [ ] **Step 4: Implement the hover toolbar**:
  - Scope: `@media (min-width:768px) and (pointer:fine)` and flow view.
  - `.canvas-card-footer` becomes `position:absolute; inset-inline:0; bottom:0`, `opacity:0`, shown on
    `.canvas-card:hover` and `:focus-within`, with a 120ms opacity transition (none under reduced motion).
  - `.canvas-card` gets `padding-block-end` equal to the footer height, so nothing overlaps content at rest.
  - The DOM is unchanged.
- [ ] **Step 5: Run** the new spec, then the guard specs `canvas-keyboard-overflow`, `canvas-note-styles` and
  `canvas-inbound-ui`. Expected: all PASS.
- [ ] **Step 6: Commit** `feat(admin): give each canvas node kind its own shape`.

### Task 3: Run state on nodes

**Files:**
- Modify: `web/admin/static/canvas-operations.js`
- Modify: `internal/admin/catalog/canvasops.ar.json`, `canvasops.en.json`
- Modify: `web/admin/templates/pages/canvases/editor.html:16` (labels)
- Modify: `web/admin/static/canvas-cards.css` (state dot styles)
- Test: `e2e/tests/canvas-node-shapes.spec.ts` (extend)

**Interfaces:**
- Consumes: `CanvasState.nodeStates`, `CanvasState.doneExpiry` (Task 1). The `runs` array that `refreshHistory()`
  already fills.
- Produces: `applyRunStates()` inside `create()`:
  - Sets `data-state` on each `[data-card-id]` element (removes it when idle).
  - Upserts a `span.canvas-node-state` as the last child of `.canvas-card-heading`, holding the state word.
  - Announces changes in one visually hidden `div.canvas-state-live[aria-live=polite]`, appended once to `root`:
    one message per card per transition, `"<title>: <state word>"`.
  - Called at the end of `refreshHistory()` and of `render()`.
  - Also returned from `create()`, so `canvas.js` can call `operations?.applyRunStates()` after its own `render()`.

Labels (key → en / ar):
- `state_done` → "Done" / "تم"
- `state_waiting` → "Needs you" / "بانتظارك"
- The existing `queued`, `running` and `failed` are reused.

- [ ] **Step 1: Write failing e2e test** `running agent shows its state`:
  - Insert a synthetic `admin_canvas_runs` row (`status:'running'`, `node_id` = the agent card) with `db()`, as
    `canvas-lifecycle.spec.ts` does, then reload.
  - Expect the agent card to have `data-state="running"` and `.canvas-node-state` to have text matching `/^قيد التشغيل · \d+:\d{2}/`.
  - Update the row to `failed`, reload, and expect `data-state="failed"`.
- [ ] **Step 2: Run** it. Expected: FAIL (no `data-state`).
- [ ] **Step 3: Add the labels** to both catalogs and to `editor.html`.
- [ ] **Step 4: Implement** `applyRunStates()`.
  - Running cards append the elapsed time since the run's `created_at` as ` · m:ss`. `/runs` exposes no step count,
    so no progress bar is drawn (spec §2).
  - Waiting cards get the existing "Tool review" or "Review run" action
  appended to the state span, reusing the handlers from `renderHistory()`. Extract them into a shared function
  `runAction(run) -> HTMLButtonElement|null`.
- [ ] **Step 5: Run** the spec and the guard specs from Task 2. Expected: PASS.
- [ ] **Step 6: Commit** `feat(admin): show run state on canvas nodes`.

### Task 4: Live edges

**Files:**
- Modify: `web/admin/static/canvas-operations.js` (`drawEdges`)
- Modify: `web/admin/static/canvas-operations.css` (edge state styles, keyframes, reduced motion)
- Test: `e2e/tests/canvas-node-shapes.spec.ts` (extend)

**Interfaces:**
- Consumes: `CanvasState.edgeStates` (Task 1). `nodeStates` as computed in Task 3: keep the last result in a `let
  states` inside `create()`.
- Produces: each edge is `path[data-state]`, plus `path.canvas-edge-packet` for flowing edges only. The packet is a
  `circle` with `style.offsetPath = \`path('${d}')\``.

- [ ] **Step 1: Write failing e2e tests**:
  - `edge into a running agent flows`: with the synthetic running run from Task 3, every `.canvas-edges path` whose
    `data-to` is the agent has `data-state="flowing"`. A `.canvas-edge-packet` exists. The edge from the schedule card
    also has `data-state="flowing"`, because flowing takes precedence.
  - `reduced motion stops edges`: after `page.emulateMedia({reducedMotion:'reduce'})`, the flowing path's computed
    `animationName` is `none`, and `.canvas-edge-packet` is not visible.
  - `every edge has an arrowhead`: each path has `marker-end` set to `url(#canvas-edge-arrow)`.
- [ ] **Step 2: Run**. Expected: FAIL.
- [ ] **Step 3: Implement** in `drawEdges`:
  - After `svg.replaceChildren()`, re-add a `<defs>` with `marker#canvas-edge-arrow`, oriented `auto-start-reverse`.
  - Build `d` once per edge and set `data-state` from `edgeStates`, matching edges by `from`/`to` against `state.edges`.
- [ ] **Step 4: Implement the styles** in `canvas-operations.css`:
  - `flowing`: `stroke-dasharray:7 9`, with a keyframe animating `stroke-dashoffset` to `-32` over 1.1s.
  - `scheduled`: `stroke-dasharray:2 7`.
  - `failed`: `stroke:#b42318; stroke-dasharray:4 5`.
  - Packet: animate `offset-distance` from 0 to 100% over 2.4s.
  - Under `@media (prefers-reduced-motion: reduce)`, set `animation:none` and hide `.canvas-edge-packet`.
- [ ] **Step 5: Run** the spec and the guard specs. Expected: PASS.
- [ ] **Step 6: Commit** `feat(admin): show live run state on canvas edges`.

### Task 5: Run polling

**Files:**
- Modify: `web/admin/static/canvas-operations.js`
- Test: `e2e/tests/canvas-node-shapes.spec.ts` (extend)

**Interfaces:**
- Consumes: `CanvasState.isActive`, `CanvasState.doneExpiry`, `refreshHistory()`.
- Produces: `schedulePoll()`, called at the end of `refreshHistory()`:
  - While `isActive(runs)` and `!document.hidden`, set one `setTimeout(refreshHistory, 4000)`. Clear any pending timer
    first.
  - Else, if `doneExpiry(runs, Date.now())` is set, use one timeout at that time that only calls `applyRunStates()`
    and `drawEdges()`.
  - A `visibilitychange` listener calls `refreshHistory()` when the page becomes visible and a run was active.
  - Polling errors are swallowed and retried on the next tick. They never call `announce`.

- [ ] **Step 1: Write failing e2e tests**:
  - `state updates without reload`: with a running synthetic run, open the board, then set the row to `succeeded` with
    `finished_at=now` in the DB. Expect `data-state="done"` within 10s.
  - `card element survives polling`: tag the agent element with `el.__probe=1` and focus its handle. After a poll
    (`waitForResponse` on `/operations/runs`), the same element still has `__probe` and `document.activeElement` is
    still the handle.
  - `polling stops when idle`: after the row is `succeeded`, count `/operations/runs` requests over 9s. Expect ≤ 1.
- [ ] **Step 2: Run**. Expected: FAIL (no update without reload).
- [ ] **Step 3: Implement** `schedulePoll()` as specified. `refreshHistory` must update only the history list,
  `applyRunStates()` and `drawEdges()`, never `api.render()`.
- [ ] **Step 4: Run** the spec and the guard specs. Expected: PASS.
- [ ] **Step 5: Commit** `feat(admin): poll canvas runs only while one is active`.

### Task 6: Schedules panel (this board)

**Files:**
- Create: `web/admin/static/canvas-schedules.js` (IIFE, `window.CanvasSchedules = {create}`), `web/admin/static/canvas-schedules.css`
- Modify: `web/admin/templates/pages/canvases/editor.html`:
  - Load the CSS and the JS (before `canvas.js`).
  - Wrap `.canvas-viewport` in `<div class="canvas-stage">`.
  - Add `<aside class="canvas-schedules" data-canvas-schedules hidden aria-labelledby="canvas-schedules-title"></aside>`
    as the stage's first child.
  - Add the labels.
- Modify: `web/admin/static/canvas-operations.js`:
  - Extract the schedule-node button handler into `toggleSchedule(card)`.
  - Return it from `create()`, and use it in `cardBody`.
- Modify: `web/admin/static/canvas.js`:
  - Create the panel after `operations`:
    `schedules = window.CanvasSchedules?.create({root,t,element,button,document:()=>documentState,toggle:card=>operations?.toggleSchedule(card)})`.
  - Call `schedules?.render()` at the end of `render()` and after the `save()` success path.
- Modify: `internal/admin/catalog/canvasops.{ar,en}.json`
- Test: `e2e/tests/canvas-schedules-panel.spec.ts` (new)

**Interfaces:**
- Consumes: `CanvasState.scheduleGroups` and `CanvasState.nextRunLabel` (Task 1). `toggleSchedule(card)` from
  operations. The existing `#card-<id>` hash reveal in `canvas.js` (setting `location.hash` focuses the card's first
  button, which is its handle).
- Produces: `CanvasSchedules.create(api) -> {render()}`.

Behaviour (spec §6):
- `hidden` when the board has no `schedule` card.
- The toggle is `button[aria-expanded][aria-controls]` with the title "Scheduled tasks · N" and, if the soonest enabled
  label is `in`, " · next <text>" after it.
- The expanded region holds, per group, an `h3` with the target's kind label and `bdi` title (or "Not connected"),
  then one `li` per schedule with:
  - `bdi` title and the interval label (the existing `hourly`, `daily` or `weekly` keys);
  - a status chip (the existing `enabled`, `disabled` or `needs_review` keys);
  - `<time datetime=next_run_at title=<full toLocaleString>>` holding the relative text, or "due now";
  - "Show on board", which sets `location.hash='card-'+id`;
  - for `needs_review`, "Review and enable", which calls `api.toggle(card)`.
- `Escape` inside the region collapses it and focuses the toggle. The expanded state is stored in `sessionStorage`
  under the key `gorun.canvas.schedules`, wrapped in try/catch.
- CSS:
  - Flow view at `(min-width:768px) and (pointer:fine)`: `position:absolute; inset-inline-end:.75rem; top:.75rem;
    width:min(26rem,100% - 1.5rem); z-index:5`, scrollable at a `max-height` of 60% of the stage.
  - Otherwise it is static, full width, above the viewport.
  - `overflow-wrap:anywhere` on titles.

Labels (key → en / ar):
- `schedules_title` → "Scheduled tasks" / "المهام المجدولة"
- `schedules_next` → "next" / "التالي"
- `schedules_due` → "due now" / "مستحق الآن"
- `schedules_show` → "Show on board" / "اعرض على اللوح"
- `schedules_review` → "Review and enable" / "راجع وفعّل"
- `schedules_unconnected` → "Not connected" / "غير موصول"

- [ ] **Step 1: Write failing e2e tests** (board from the `periodic-brief` preset with an agent target):
  - `lists the schedule under its agent`: the panel is visible. One group heading contains the agent card's title, and
    its row contains the schedule card's title and the interval label `يوميًا`.
  - `no panel without schedules`: on a blank board, `[data-canvas-schedules]` is hidden.
  - `collapse persists and Escape returns focus`: collapse, reload, and it is still collapsed. Expand, press `Escape`
    inside, and the toggle is focused with `aria-expanded="false"`.
  - `show on board focuses the node`: click "اعرض على اللوح". The schedule card's handle is focused and in the viewport.
  - `needs_review row offers review`: insert an `admin_canvas_schedules` row with `enabled=0, status='needs_review'` via
    `db()` and reload. The row shows `راجع وفعّل`, and clicking it opens the existing review dialog
    (`.canvas-review[open]`).
  - `unconnected schedule`: delete the schedule's edge through the UI's Disconnect button. The row moves under
    `غير موصول`.
  - `panel has no horizontal overflow`: rename the agent to a 120-byte mixed Arabic and Latin title. The panel's
    `scrollWidth` ≤ its `clientWidth`, and its right edge ≤ the viewport width, at 1280px and at 375px.
  - `en locale`: after `setLocale(page,'en')`, the toggle reads `Scheduled tasks · 1`.
- [ ] **Step 2: Run** `npx playwright test tests/canvas-schedules-panel.spec.ts`. Expected: FAIL (no panel).
- [ ] **Step 3: Add the labels, the template mount, the wrapper and the script/CSS tags.**
- [ ] **Step 4: Implement** `canvas-schedules.js` and `canvas-schedules.css` as specified. Extract `toggleSchedule`
  in operations.
- [ ] **Step 5: Run** the new spec, plus `canvas-lifecycle`, `canvas-keyboard-overflow`, `canvas-inbound-ui` and
  `canvas-activity`. Expected: PASS. Confirm that `.canvas-viewport` selectors still match after wrapping.
- [ ] **Step 6: Commit** `feat(admin): list this board's scheduled tasks in a floating panel`.

### Task 7: Lanes view

**Files:**
- Modify: `web/admin/static/canvas-state.js` (add `lanes`), `web/admin/static/canvas_state_test.cjs`
- Modify: `web/admin/static/canvas.js`:
  - Add `'lanes'` to the allowed stored views.
  - In `render()`/`place()`, when `view==='lanes'` and not mobile, position from `lanes()` instead of `card.x`/`card.y`.
  - `move()` and `drag()` return early, as for grid and list.
  - `normalizePositions()` skips the view.
  - Append the column bands to the surface.
- Modify: `web/admin/static/canvas-operations.js:126`: `drawEdges` runs when `root.dataset.view` is `flow` or `lanes`.
- Modify: `web/admin/templates/pages/canvases/editor.html` (the fourth view button, labels)
- Modify: `web/admin/static/canvas-cards.css`:
  - Shape, toolbar and panel rules scoped to `[data-view=flow]` become `:is([data-view=flow],[data-view=lanes])`.
  - Add `.canvas-lane` band styles.
  - The handle and move controls are hidden in lanes, as in grid.
- Modify: `internal/admin/catalog/canvasops.{ar,en}.json`
- Test: `web/admin/static/canvas_state_test.cjs` (extend), `e2e/tests/canvas-lanes.spec.ts` (new)

**Interfaces:**
- Consumes: `CanvasLayout.WIDTH` (300). Card heights from `heights()` in `canvas.js`. `documentState.provenance`.
- Produces: `CanvasState.lanes(cards, heights, provenance, dir: 'rtl'|'ltr') -> {positions: {[id]:{x,y}}, columns: Array<{lane: 0|1|2|3, x, count}>}`.
  - `laneOf(card, provenance)`:
    - `source`, `content`, `link`, `checklist`, `widget`, and `note` without provenance → 0
    - `prompt`, `schedule` → 1
    - `agent`, `provider` → 2
    - `result`, and `note` with provenance → 3
    - an unknown kind → 0
  - Column `x`: for `ltr` it is `24 + lane*336`; for `rtl` it is `24 + (3-lane)*336`.
  - Card `y` starts at 72 (a 48px band plus 24) and stacks with `height + 24`. Within a lane, cards are ordered by
    saved `y`, then `x`, then `id`.

Labels (key → en / ar):
- `view_lanes` → "Lanes" / "مسارات"
- `lane_inputs` → "Inputs" / "المدخلات"
- `lane_instructions` → "Instructions and triggers" / "التعليمات والمشغّلات"
- `lane_execution` → "Execution" / "التنفيذ"
- `lane_results` → "Results" / "النتائج"

- [ ] **Step 1: Write failing unit tests**:
  - `lanes assigns kinds`: one card per kind, plus an attributed note → the expected lanes.
  - `lanes mirrors in rtl`: an `agent` card has `x=24+2*336` in `ltr` and `x=24+1*336` in `rtl`.
  - `lanes keeps saved order and stacks by height`: two prompts with saved `y` 500 and 100 and heights 200 and 120 →
    y values 72 and 72+120+24, in saved-`y` order.
  - `lanes does not mutate cards`: a deep-equal check before and after.
- [ ] **Step 2: Run** `node --test web/admin/static/canvas_state_test.cjs`. Expected: FAIL (`lanes is not a function`).
- [ ] **Step 3: Implement** `lanes` in `canvas-state.js`.
- [ ] **Step 4: Write failing e2e tests** in `canvas-lanes.spec.ts` (board from the `periodic-brief` preset with an
  agent target, plus one attributed note inserted the way `canvas-lifecycle.spec.ts` does):
  - `columns hold the right kinds`: after clicking `[data-canvas-view=lanes]`, the agent card's `offsetLeft` equals
    column 3's band `offsetLeft` + its padding, and the attributed note's equals column 4's.
  - `rtl mirrors en`: the agent's column index from the inline start is the same in `ar` and `en`, and its `offsetLeft`
    differs.
  - `lanes never saves`: record the document `version` and count POSTs to `/document`. Switch flow → lanes → grid → flow.
    The version is unchanged and there are 0 POSTs.
  - `edges draw in lanes`: `.canvas-edges path` count in lanes equals the count in flow.
  - `mobile lanes is a list`: at 375px, the cards' `offsetTop` values increase in lane order.
- [ ] **Step 5: Run** it. Expected: FAIL (no lanes button).
- [ ] **Step 6: Implement** the `canvas.js`, `canvas-operations.js`, template, CSS and label changes listed under
  **Files**.
- [ ] **Step 7: Run** `canvas-lanes`, `canvas-node-shapes`, `canvas-schedules-panel`, `canvas-lifecycle` and
  `canvas-keyboard-overflow`. Expected: PASS.
- [ ] **Step 8: Commit** `feat(admin): add a lanes view to the canvas`.

### Task 8: Per-PR verification

- [ ] Run `node --test web/admin/static/canvas_*_test.cjs`, `go test ./internal/admin/...` (the catalogs are embedded,
  and key parity tests live there) and `go vet ./...`. Expected: all PASS.
- [ ] Run `make e2e` (full browser suite, about 27 minutes in CI, so run it in the background). Expected: PASS.
- [ ] Take manual screenshots of the flow view at 1440px and 375px in `ar` and `en`, with an axe scan in both locales,
  and attach them to the PR.
- [ ] Add a CHANGELOG `## [Unreleased]` line, commit, push, and open the PR. The required checks are `sign-off`, `test`
  and `browser`.
