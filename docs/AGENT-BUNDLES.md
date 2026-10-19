# Agent skills and MCP references

Open an owned active agent key under **Agent keys → Skills & MCP connections**, or
follow its bundle link in the Agents drawer. The advanced workspace and current
`api_keys.manage` plus `settings.manage` permissions are required. Other users,
API keys and revoked/expired agent keys cannot manage or export the bundle.

A bundle belongs to one owner and one agent key. Add up to eight named skills.
Each skill contains plain instructions and an optional inline reference label and
text; these are not executable scripts, filesystem paths, remote imports or an
installed skill package. Each instruction/reference is limited to 4,000 UTF-8
bytes, combined skill material to 12,000 bytes, and the complete effective canvas
prompt (preset, skills, references and connected prompt cards) to 16,000 bytes.
Character limits in the browser are advisory; the server enforces byte limits.

Skills become part of the **actual effective prompt** shown in canvas review and
passed to managed text execution or an authorized external client's reviewed run.
They do not grant access to content, alter the model's permissions, or publish
anything. A new bundle version retires existing scheduled/incoming-event consent.
Queued/in-flight results are rejected when bundle authority changes, including
changing instructions and restoring their previous text. Review affected actions
again. Rotating to a different agent key does not copy its old bundle.

Attach up to eight **existing site MCP connection profiles**. This is an owned
reference to a particular profile configuration version, not a separate owner of
the site connection. Profiles must remain valid and the owner must retain current
management permission. Removing/changing a profile blocks prepared bundle actions
until the references are reviewed and saved again. Saving does not contact a
server. Use the existing Connections screen's bounded, SSRF-protected discovery
test; the bundle displays its timestamp and result for the pinned configuration.
“Last discovery passed” does not mean online now, trusted tool behavior, or a
successful agent task. A changed binding is explicitly marked for review.

The download is a JSON manifest with skill text and MCP IDs, configuration versions,
names and discovery snapshots. It excludes credentials, key prefixes, MCP endpoint
URLs (which can themselves contain capability tokens), content context and grants.
It does not enable tools or provision an external runtime. Configure endpoints,
credentials and tool permissions separately in that client; keep it connected and
polling GoRun. Do not put credentials into skill/reference text intended for export.

Attaching MCP references alone does not execute tools. The bundle editor does not run code, provision credentials or grant unattended tool authority. Existing Studio operations keep their exact reviewed bindings.

## Supervised managed tool calls

Named OpenAI-compatible managed text agents can opt into a separate default-disabled tool policy with explicit approval of every exact call. This is manual supervised execution; external clients keep their own runtime configuration. See [Managed MCP tools with approval](MANAGED-MCP-TOOLS.md) for setup, supported schemas and bounds.
