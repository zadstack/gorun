# Managed MCP tools with approval

GoRun can perform a bounded model → MCP tool → model roundtrip for a **named, OpenAI-compatible managed text agent**. Tool execution starts disabled. This is supervised execution: the owner reviews every exact proposed call before GoRun sends it. It is not an unattended agent loop.

## Set up

1. Create an AI connection using the OpenAI-compatible protocol and an MCP connection using the existing connection settings. Credentials stay encrypted on the server. Use a trusted MCP server with narrowly scoped credentials.
2. Configure an owned agent key for managed text execution with that named model. In **Skills & MCP connections**, save its instructions and attach the required MCP profiles.
3. Open **Reviewed MCP tools** from the bundle editor. Choose **Discover available tools**. Discovery reads tool definitions; it never calls a tool.
4. Select up to eight tools, inspect their descriptions and input formats, and save the reviewed policy. This pins the model configuration, connection versions, tool names, schemas and complete discovered catalog for each server.
5. Connect the agent to saved prompt/context cards in a canvas and review the full initial run. Tool-enabled runs are manual only: incoming events and schedules cannot invoke them.
6. Refresh the run history. A proposed action appears as **Waiting for tool approval**. Open its review to see the actual server name and hostname, original tool name and exact JSON arguments. Confirm your identity if requested, then approve this call once or reject and stop the run.

Approval only queues the call. The worker rechecks current authority and the current MCP catalog, calls the selected tool using its MCP session, and returns its text result to the model. Another proposed call pauses again. A final answer becomes a private canvas note; it does not publish content. External agent clients and Anthropic configurations do not use this managed tool loop.

## Limits and failure behavior

- At most three actual tool attempts and four model turns per run. One call per model turn; parallel calls are rejected.
- Arguments: JSON object, at most 4 KiB. Input schemas: object root, at most 8 KiB, depth 12 and 512 nodes, no external references or schema IDs. Existing local JSON Schema validation checks the actual arguments.
- Discovery: at most 32 definitions per MCP profile and 64 eligible definitions across the attached profiles. A description is limited to 512 bytes. Unsupported or larger catalogs require a narrower MCP profile; unsupported schemas cannot be selected.
- Results: text content only, at most 8 KiB combined. Errors, images, resources and resource links stop the run. GoRun never fetches links found in tool output. Model replies and serialized requests also have size limits.
- Each call review expires after 10 minutes. The whole run expires after 30 minutes, with at most 90 seconds of accumulated active work excluding human review time. Bounds persist across approvals and worker restarts.
- Approval is consumed atomically once. An interrupted in-flight call has an uncertain outcome and is never automatically retried. An external action may already have occurred even when a response is lost, rejected or the run stops. Review the external system before starting a new run.

Every boundary rechecks the owner, current permissions, key, canvas grants, bundle, model, connection and source. A durable authority revision also retires old reviews after permissions are removed and restored. Edits to referenced source items or their content types retire affected board reviews, including edit-and-restore. Changing a source-list type can conservatively retire a board even when the changed item is outside the current selection. Relevant site connection/model/bundle configuration changes conservatively retire existing tool reviews; create a fresh review afterwards. Tool-disabled runs retain their existing behavior.

Descriptions and outputs are untrusted text. They cannot grant tools, introduce new endpoints or replace the approved arguments. Stable internal aliases distinguish identical tool names on different servers; the approval displays the original tool and server identity. Known stored credentials and endpoint values are redacted from the private transcript. Audit entries contain identifiers, counts and outcomes rather than prompts, arguments or tool outputs. Do not place unrelated secrets in prompts or tool results.

Saving the general bundle disables its selected-tool policy. Templates and credential-free manifests do not carry enabled tool authority. Reconfigure and review the tool policy explicitly after changing the bundle.

## Verification

The protocol regression suite uses mocked HTTP transports for real OpenAI-compatible request/response messages and MCP initialization, discovery and `tools/call` messages. It verifies that no tool call occurs before approval, the exact approved call occurs once, actual tool text reaches the next model turn, and only a private note is appended. It also exercises replay, revocation/restoration, catalog drift, argument/schema limits, non-text output, expiry and uncertain attempts. These fixtures verify orchestration and authority boundaries, not model quality or the safety of arbitrary third-party tools.
