# Optional MCP connections for Studio

GoRun contains a provider-neutral MCP client. Remote integrations are opt-in connection records, not libraries or server binaries bundled into the CMS. Creating a connection does not register a Studio action, execute a tool, or activate a provider on other sites.

## Setup

1. Open Settings → Connections. Add an MCP endpoint and credential; secrets are encrypted server-side.
2. Test the connection. This initializes the session and discovers the complete tool catalog only.
3. Open Settings → Studio and discover that connection. Discovery uses the same catalog reader as the connection test.
4. Choose exact tool names and argument/output mappings for the intended Studio task. Save only approved bindings. Never put credentials in argument templates.
5. Preview the request before execution. Discovery is not permission to execute every advertised tool. Publishing, billing, messaging, host or desktop controls should not be configured as media-generation actions.
6. To disable an integration, remove its Studio bindings before removing its connection. No remote service uninstall or site content deletion is required.

## Complete catalog contract

Discovery follows opaque `nextCursor` values and preserves the negotiated protocol, authentication and session. Missing, empty or null terminal cursors are accepted. Probe counts and Studio tools include all successfully fetched pages.

Limits: 16 pages, 200 tools, 4 MiB cumulative decoded catalog results, the existing 1 MiB response limit, and 4096-byte cursors. A later-page failure, repeated cursor, duplicate tool name, invalid object input schema, cancellation or exceeded limit rejects the whole discovery. It must not present a partial catalog as successful. Providers exceeding the limits should expose a narrower catalog; raising limits needs explicit performance review.

Credentials never enter frontend configuration. Existing endpoint validation, private-network restrictions, redirect restrictions, per-request deadlines and Studio approval/review behavior remain in place. Tool annotations are advisory, not a substitute for the server enforcing permissions.

## Keep responsibilities separate

- Core: transport, discovery, schemas, encrypted connection configuration, exact tool bindings, request review and execution state.
- Optional site integrations: image optimization, transcription, brand assets and rendering, selected per site and supported by the provider's actual schema.
- Operator integrations: desktop/browser control, source-code review and server/container administration.

No third-party server package, GPU runtime or SDK is required to use these HTTP connections. The owner supplies and operates their selected services. Discovery proves connectivity and catalog compatibility; it does not certify every provider operation or imply a subscription.
