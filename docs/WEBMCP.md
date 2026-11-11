# WebMCP in the admin

GoRun's admin registers in-page tools with WebMCP, the browser API drafted by
the W3C Web Machine Learning Community Group
([specification](https://webmachinelearning.github.io/webmcp/), draft of
2026-11-10). An AI agent running in the user's browser can then search the
admin, read the open page, move between pages and prepare drafts, through the
user's own signed-in session and permissions.

WebMCP complements the [MCP server](MCP.md): the MCP server is for agents
that connect from outside with an API key; WebMCP is for an agent working
inside the browser tab next to the user. No API key is involved.

## Availability

The script `web/admin/static/webmcp.js` loads on every admin page and does
nothing unless the browser provides `document.modelContext.registerTool`.
WebMCP requires a secure context (HTTPS, or `localhost`) and the `tools`
permissions-policy feature, whose default allowlist is `'self'`: the admin's
own top-level document may register tools, and GoRun never frames the admin
(`X-Frame-Options: DENY`, `frame-ancestors 'none'`). Tools are exposed to the
page's own origin only (no `exposedTo`), and they are unregistered when the
page is left (an `AbortSignal` aborted on `pagehide`); each admin page
registers them again.

## Tools

| Tool | Annotations | What it does |
| --- | --- | --- |
| `search_admin` | `readOnlyHint`, `untrustedContentHint` | `{query}` (2+ characters): content titles, users and admin pages the user may see, with admin links. |
| `list_admin_pages` | `readOnlyHint` | Admin pages in the user's navigation, the content types they may draft, and `can_draft`. |
| `get_current_page` | `readOnlyHint`, `untrustedContentHint` | Path, title, heading, active section and the visible main text of the open page (first 4,000 characters). |
| `open_admin_page` | — | `{href}`: navigates the tab to an admin path. Anything outside `/admin` on this origin is refused. |
| `create_content_draft` | — | `{type, title, body?}`: creates a **draft** as the user and returns its editor link. |

`untrustedContentHint` marks results that can contain text written by other
people (content titles, page text): agents should treat it as data, not
instructions.

## Safety model

- Every tool calls an admin endpoint that runs as the signed-in user with
  the same permission checks as the screens. `create_content_draft` needs
  `content.create` and is refused (`403 forbidden`) otherwise.
- The only write is a draft. The status is always `draft`; unknown fields
  (such as `status`) are rejected. The body is sanitized like any other
  content. Publishing, deleting, settings and approvals stay in the regular
  screens, where the human acts.
- Writes carry the session's CSRF token, so another site cannot call the
  endpoints with the user's cookies.

## Endpoints

These are admin-session endpoints for the script, not part of `/api/v1`:

| Method | Path | Response |
| --- | --- | --- |
| GET | `/admin/x/webmcp/manifest` | `{pages:[{label, href, section}], content_types:[{slug, name}], can_draft}` |
| GET | `/admin/x/webmcp/search?q=` | `{query, too_short, content:[…], users:[…], pages:[…]}`, each hit `{title, detail, href}` |
| POST | `/admin/x/webmcp/drafts` | JSON `{type, title, body}` with `X-CSRF-Token` → `201 {ok, id, status:"draft", href}`; `422 {code:"validation_failed"}`; `403 {code:"forbidden"}` |

Integrations and remote agents should keep using the [REST API](API.md) and
the [MCP server](MCP.md).
