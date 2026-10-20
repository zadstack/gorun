# Editable agent recipes

Open **Canvas & agents → Agent role templates → Build an Updates Researcher recipe**
(`/admin/agents/recipes`). The first recipe creates a manual research brief as a
private, editable canvas. It is separate from the six teaching role templates.

1. Choose your own active agent key. A settings and agent-key manager can inspect
   its current model assignment, bound MCP profile names and already reviewed
   managed-tool allowlist. Ordinary content editors see the example instructions
   without other users’ keys or connection metadata.
2. Edit the canvas title, task instructions, supplied topic/source excerpts/date
   range and agent task label. URLs pasted as context are **not fetched**. The
   example asks for source URLs, publication/event dates, uncertainty and a human
   review checklist; it cannot guarantee current or accurate research.
3. Preview the recipe, then acknowledge and save. Both steps are local: no model
   request, tool discovery, tool call, grant, schedule or publication occurs.
4. The saved canvas appears among your normal canvases. Its prompt/context cards
   remain editable. The result destination is fixed to a private note in that
   new canvas; the task-label field does not select an external destination.
5. Use the canvas’s existing agent access controls to grant the chosen key the
   relevant context nodes and read/append/run permissions. Key scopes and owner
   permissions must also allow the operation. Saving the recipe grants nothing.
6. Review the exact assembled prompt, context, model and tools using the normal
   agent-card run review. This includes any agent-level skills beyond the recipe
   text. Confirm the manual run separately. Managed MCP proposals still pause
   for approval of each exact destination and arguments. An external-client task
   stays `waiting_agent` until a running client takes it.

The recipe uses the selected agent’s **whole existing reviewed policy**, not a
per-recipe subset. Connection/skill/model/tool editor links change agent-wide
settings and affect its other canvases. Use a dedicated agent when appropriate;
this screen neither creates a key nor changes those settings. After editing
configuration, reload the recipe page. A stale form is rejected before saving.
An unchanged retry creates only one canvas and one creation audit event.

Saved-profile metadata is not a live availability check. AI model profiles are
shown separately from MCP source/tool profiles. Generic API/storage profiles are
not research execution bindings. No tool is classified as read-only or safe from
its name. Opening the recipe never probes a profile or decrypts a secret.

There is no daily polling, autonomous news feed, recurring managed MCP execution,
automatic publication or built-in quality guarantee. Structural canvas templates
continue to strip agent bindings and free text; they do not carry recipe
credentials, context or execution consent.

See [agent bundles](AGENT-BUNDLES.md), [managed MCP tools](MANAGED-MCP-TOOLS.md)
and the canvas’s existing grant/review controls for execution boundaries.

## Verification

```sh
go test -race ./internal/admin -run '^TestAgentRecipe' -count=1
cd e2e
npm ci
npx playwright install chromium
npx playwright test tests/agent-recipes.spec.ts --project chromium
```

The browser fixture uses a separately running external test client and a scripted
local HTTP provider after normal grant and run review. It checks exact reviewed
payload equality and private append in English and Arabic/mobile. It does not
weaken named-connection SSRF restrictions or claim a real research service was
queried. Managed-tool protocol tests separately exercise the existing allowlist
and individual approval flow using the real runtime with a test transport.
