# Canvas: node shapes, live edges, schedules panel

Status: design, awaiting review. Direction A of the mockups
(https://claude.ai/artifact/QXFyF7soXjPUPi7zit9Bbq, private).

## Goal

On the admin canvas (`/admin/canvases/{id}`, flow view), a person should tell at a glance:

1. what each node is (its kind), without reading a badge;
2. what each node is doing (idle, queued, running, waiting for review, done, failed);
3. where data is flowing right now;
4. which scheduled tasks this board runs, for which agent or provider, and when they run next.

Out of scope: grid and list views (they keep today's cards), mobile flow (edges already hide below 768px),
a schedules view across all boards, a time of day for daily schedules, and changes to the canvas document format
or run engine.

## What the user decided

- Direction A: a distinct shape for each kind.
- The schedules panel covers **this board only**.
- "scatweld" means scheduled tasks.
- From direction C, add a **Lanes view**: a fourth view button next to flow, grid and list. It shows A's shapes
  arranged in four fixed columns and never changes saved positions.

## Today (0.18.6)

- Every kind renders the same 300px `article.canvas-card`. Only the kind badge and the top border hue differ.
  Several hues are close: prompt, result and attributed note are purple; provider, widget and source are teal or blue.
- Every card footer carries edit, remove and four move arrows, which visually outweigh the content.
- Agent and provider bodies show setup text instead of state.
- Edges are static polylines (`CanvasLayout.route`) drawn by `drawEdges` in `canvas-operations.js`, with no direction or state.
- Run state appears only in the run history list (`GET .../runs`). Schedule state appears only on the schedule node.

## Design

### 1. Node shapes (CSS only)

Each kind keeps its current DOM (`article.canvas-card[data-kind]`) and its footprint (`CanvasLayout.WIDTH` = 300,
measured height). The shape is drawn inside that box, so `resolve`, `arrange`, `ports` and `route` need no change.

| kind | shape | notes |
|---|---|---|
| source | stacked sheets (two offset layers through `::before`/`::after`) | blue |
| content | single sheet with one offset layer | blue |
| prompt | document with a folded corner (`clip-path`, the fold on the inline-start top corner) | purple |
| note | sticky note with a tape strip; attributed notes keep the violet variant | amber |
| checklist | card whose heading shows a checkbox glyph | green |
| link | narrow capsule | brown |
| widget | card with a small bar-chart glyph | teal |
| agent | larger radius, thicker border, a round avatar badge overlapping the top edge | indigo |
| provider | capsule heading with a plug badge; the generation kind (text, image, video) as a chip | teal, moved off widget's hue |
| schedule | round clock badge in the heading, round-cornered body | pink |
| result | frame with a status seal in the corner | violet |

Rules:

- The kind is always also written as text: the existing `small` label stays, and is visually smaller.
- Each kind keeps its `--card-accent`, `--card-soft` and `--card-label` tokens. Hues that collide today are separated
  by lightness as well as hue. All text passes 4.5:1.
- `clip-path` applies only to a decorative layer (`::before`), never to the element that holds focus outlines.
- RTL: corners and badges use logical properties (`inset-inline-start`), and the fold sits on the start corner in both
  directions.

### 2. Node state

`cardView` sets `data-state` on each card from the latest run whose `node_id` is that card:
`idle | queued | running | waiting | done | failed`.

| `admin_canvas_runs.status` | `data-state` |
|---|---|
| none | `idle` |
| `requested`, `queued` | `queued` |
| `running`, `waiting_agent` | `running` |
| `waiting`, `waiting_tool` | `waiting` |
| `succeeded`, within the last 60s | `done` (then `idle`) |
| `failed`, `uncertain` | `failed` |

- The state renders as a dot plus a word in the card heading (`role="status"` is not used per card).
- One visually hidden `aria-live="polite"` region announces transitions, at most once per card per transition.
- The `agent` card also shows a progress line while running, but only if the run exposes steps. If it does not,
  it shows elapsed time instead. No invented progress.
- `waiting` shows the existing "Tool review" or "Review run" action inline, so the next step is one click away.

### 3. Card actions

- The footer (edit, remove, move arrows) moves into a toolbar shown on `:hover` and `:focus-within` of the card.
  The handle stays visible.
- The DOM order stays the same, so keyboard order and existing selectors in the e2e specs still work.
- On `(pointer: coarse)` and below 768px the footer stays always visible, as today.

### 4. Live edges

`drawEdges` adds `data-state` to each `path`: `idle`, `scheduled` (the edge leaves an enabled schedule node),
`flowing` (its target card is `running`) and `failed` (its target's latest run failed).

- `flowing`: an animated `stroke-dashoffset` plus one packet dot per edge, a `circle` animated with CSS
  `offset-path` set to the path's `d`.
- `scheduled`: a dotted stroke. `failed`: a red dashed stroke plus an × marker at the midpoint. `idle`: today's stroke.
- A direction arrowhead (`marker-end`) is added to every edge. It flips correctly in RTL because the path itself is drawn
  in page coordinates.
- `prefers-reduced-motion: reduce`: no dash animation and no packets. The state still shows through the stroke pattern,
  color and the node's state word.
- The edge SVG stays `aria-hidden`. State is conveyed on the nodes.

### 5. Run polling

- A new `CanvasRuns` helper inside `canvas-operations.js` reuses the existing `json('/runs')`.
- It polls every 4s only while some run on the board is in `queued`, `running` or `waiting_agent`. It stops when none
  are, and pauses while `document.hidden`.
- It starts after a run is requested and on page load if an active run exists. It re-renders only state attributes,
  not the whole board.
- No new endpoint. If `/runs` grows expensive later, a `?active=1` filter is a follow-up.

### 6. Schedules panel (this board)

A floating, collapsible panel at the top inline-end of the flow view: `aside.canvas-schedules` in `editor.html`,
logic in a new `web/admin/static/canvas-schedules.js`, styles in `canvas-schedules.css`.

Data, all client-side and already loaded:

- schedule nodes: `documentState.cards` with `kind === 'schedule'` (title, `interval`);
- state: `documentState.schedules[node]` → `{enabled, next_run_at, status}` (`enabled | disabled | needs_review`);
- target: the single outgoing edge of the schedule node (`documentState.edges`) → an agent or provider card
  (title, kind).

Layout:

- The collapsed state is a button: clock icon, "Scheduled tasks · N", and "next in 42 min" when some schedule is enabled.
- Expanded, rows are grouped by target card (agent or provider). Each row shows: schedule title, interval, a status chip,
  the next run as relative time plus a `<time datetime>` whose title holds the full date, and a "Show on board" button.
  "Show on board" scrolls to the node and focuses its handle, reusing the `#card-<id>` reveal logic.
- `needs_review` rows show the reason and a "Review and enable" button that calls the existing enable flow
  (`preview(target, schedule)`). Enable and disable reuse the node's existing actions. There is no new write path.
- Schedules with no target are listed under "Not connected".
- Empty board: the panel is not rendered.

Behaviour:

- It is a disclosure (`button[aria-expanded]` controlling a region), not a dialog. `Escape` inside it collapses it
  and returns focus to the toggle.
- The expanded or collapsed state is kept in `sessionStorage`, as `gorun.canvas.view` already does.
- It refreshes on `saved()` and after enable or disable.
- Below 768px or with a coarse pointer it docks under the toolbar as a full-width block instead of floating.
- Relative times use `Intl.RelativeTimeFormat(document.documentElement.lang)`, so they read "بعد ٤٢ دقيقة" in Arabic.
  Titles are wrapped in `bdi`.

### 7. Lanes view

A fourth `data-canvas-view="lanes"` button ("Lanes" / "مسارات"). The view is stored in `sessionStorage` like the others.

- **Columns**, in reading order (right to left in `ar`, left to right in `en`):
  1. Inputs: `source`, `content`, `link`, `checklist`, `widget`, and `note` without provenance.
  2. Instructions and triggers: `prompt`, `schedule`.
  3. Execution: `agent`, `provider`.
  4. Results: `result`, and `note` with provenance (agent output).
- **Positions** are computed, never saved:
  - Column pitch 336px (as `CanvasLayout`), card width 300, a 48px header band, 24px gap.
  - Within a column, cards keep their saved order: by `y`, then `x`.
  - Moving and dragging are disabled, as in grid and list. `changed()` is never called by this view.
- **Column bands**: each column gets a background band and a header ("1 · Inputs (2)"), drawn as decorative elements
  in the surface (`aria-hidden`). Each card's `aria-label` already carries its kind.
- **Edges and state**: edges, edge states and node states draw exactly as in flow (`drawEdges` runs for `flow` and
  `lanes`). The schedules panel floats as in flow.
- **Below 768px** the view renders like list, ordered by column and then by saved order.

### 8. Labels

New label keys go through the existing `data-label` mechanism in `editor.html`, with `ar` and `en` strings in
`internal/admin/catalog/canvasops.{ar,en}.json`: the state words, the panel strings and "Not connected".
Run-status words that already exist (`queued`, `running`, `failed`, …) are reused.

## Files

| file | change |
|---|---|
| `web/admin/static/canvas-cards.css` | shapes, state dot, hover toolbar, separated tokens |
| `web/admin/static/canvas.js` | `data-state` on cards, footer becomes a toolbar container |
| `web/admin/static/canvas-operations.js` | edge `data-state`, arrowheads, packets, run polling |
| `web/admin/static/canvas-state.js` (new, UMD like `canvas-graph.js`) | pure functions: `nodeStates(runs, now)`, `edgeStates(cards, edges, nodeStates, schedules)`, `scheduleGroups(cards, edges, schedules)`, `lanes(cards, heights, provenance, dir)` |
| `web/admin/static/canvas.js` | lanes view: computed placement, column bands, drag disabled |
| `web/admin/static/canvas-schedules.js`, `canvas-schedules.css` (new) | panel |
| `web/admin/templates/pages/canvases/editor.html` | panel mount, script tags, labels |
| `internal/admin/catalog/canvasops.ar.json`, `canvasops.en.json` | new keys (the only Go-side files touched, data only) |

No Go code, migrations or routes. `docs/BOUNDARIES.md` is unaffected: everything stays inside the admin module's
static assets.

## Testing

- `web/admin/static/canvas_state_test.cjs` (node, like `canvas_graph_test.cjs`): run-status mapping including the 60s
  `done` window, edge states, schedule grouping (no target, two schedules on one agent, disabled, needs_review).
- New Playwright specs:
  - `e2e/tests/canvas-node-shapes.spec.ts`: each kind has a visible kind label; synthetic runs inserted the way
    `canvas-lifecycle.spec.ts` does set `data-state` on the card and edge; under `page.emulateMedia({reducedMotion:'reduce'})`
    the edge animation name computes to `none` and no packet is visible.
  - `e2e/tests/canvas-schedules-panel.spec.ts`: RTL (default `ar`) and `en`; it lists a schedule from the
    `periodic-brief` preset grouped under its agent; collapse and expand persist; `Escape` returns focus;
    "Show on board" focuses the node; a `needs_review` row shows its action; there is no panel on a board without
    schedule nodes.
- `e2e/tests/canvas-lanes.spec.ts`:
  - The agent sits in column 3, and the attributed note in column 4.
  - Column order is mirrored between `ar` and `en`.
  - Switching to lanes and back leaves the saved document unchanged (same `version`, no save request).
  - Edges draw in lanes.
- Existing specs that must stay green: `canvas-keyboard-overflow`, `canvas-lifecycle`, `canvas-note-styles`,
  `canvas-inbound-ui`.
- Manual check: axe in both locales, and 200% zoom.

## Delivery

Four PRs, each releasable on its own:

1. Shapes, tokens, `data-state` on cards and the hover toolbar (no polling yet; state from one `/runs` fetch on load).
2. Live edges and run polling.
3. The schedules panel.
4. The lanes view.

## Risks

- `clip-path` and pseudo-element shapes can cut off focus outlines. This is mitigated by keeping them on decorative layers.
- Polling load. It is limited to boards with an active run and paused when hidden.
- The hover toolbar hides actions from people who did not know they exist. The handle stays visible, and the toolbar
  also opens on focus.
- `offset-path` on SVG elements is newer. If unsupported, packets simply do not render, and the dash animation still
  shows flow.
