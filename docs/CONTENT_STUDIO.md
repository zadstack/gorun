# Content studio

The native content editor includes a studio below its form for every saved content type with body support. It reads the current unsaved title and text. Candidates belong to the signed-in editor and content item. Studio actions never save or publish the article. Text insertion/replacement changes only the unsaved editor; replacement requires explicit confirmation. Removing a candidate keeps its imported Media file.

## Providers and declarative skills

Open **Settings → AI → Content studio**, or the studio's Providers & skills link.

- Text reuses the configured encrypted OpenAI-compatible or Anthropic writing provider.
- Native images require an explicit `image_model` and an OpenAI-compatible endpoint that implements `POST /images/generations`. One 1024×1024 image is requested. The endpoint must return base64 image data or a downloadable HTTPS URL.
- Video and render use explicitly configured named MCP connections. There is no assumed generic video API and no deprecated OpenAI Videos adapter.
- Several MCP bindings may be configured for each task. The editor chooses one per generation. Each binding declares a capability, exact discovered tool, JSON argument template and output mapping.
- Presets are editable prompt data, never executable files. They can be added, changed or removed in the configuration. An editor can customize the prompt before each operation.

Save credentials only in encrypted Connections settings. Select an MCP connection and click **Connect & list tools only**. This executes initialize/notification/tools-list, not generation. Review its actual schema, then configure the exact tool name and JSON arguments. The setup screen includes a field reference, not a working provider example. Allow only tools intended to return draft text or media; tool invocation itself may consume provider credits. Never allow payment, publication or messaging tools here.

Template values support `{{prompt}}`, `{{title}}`, `{{text}}` and, for status requests, `{{job_id}}`. Replacement occurs on decoded JSON string values, so quotes cannot inject JSON fields. Constants remain owner-defined. Dot-separated output paths read strings in the tool result, such as `structuredContent.url`. Plain MCP text blocks are supported for text results. The provider validates its own argument schema; unsupported contracts fail explicitly.

Every generation first displays the exact resolved parameters, endpoint and model/tool for review. A review hash prevents unnoticed destination/configuration changes before submission. The candidate snapshots the selected connection endpoint/version and binding. Changing/revoking the binding invalidates queued work. A request ID makes resubmitting the same generation idempotent.

## Asynchronous MCP tools

Only configure asynchronous tools when their real contract is known. Set `job_path`, `status_tool`, `status_arguments`, `status_path`, `ready_value`, `failed_value`, and the final `url_path`. The status tool must also be discovered on the same connection. A pending result retains its job ID privately. **Check provider status** enqueues one explicit call to that exact status tool. No polling endpoint or automatic tool chain is invented. The browser automatically refreshes local queue status, not the external provider.

Creation runs once in the native queue. If a connection drops after a provider accepted the request, the operation is uncertain: check the provider before explicitly generating again. The worker never automatically repeats a potentially chargeable creation. Interrupted running records become failed after five minutes, with no repeat call. Regeneration uses a new reviewed request and creates a separate candidate.

## Media and limits

Imports accept PNG, JPEG, WebP and MP4. Render can return an image or video. HTTPS downloads use the DNS-aware public-network client, reject redirects, URL credentials and fragments, and never forward provider authorization. Signed HTTPS download URLs work; arbitrary extra download headers do not. Responses are bounded (MCP 1 MiB, image JSON 32 MiB, downloaded media 64 MiB), and native site upload limits still apply. Tool calls have a 90-second client limit inside a 100-second job limit. Longer tasks must use an asynchronous provider contract.

Imported files use native `media.Service.Upload` with `GeneratedContentID` and `GeneratedKind`. Storage keys are `sites/<site-id>/content/<content-id>/YYYY/MM/DD/<media-id>.<ext>`, using the selected local/S3 Media destination. Provider download URLs are not copied into media source metadata. Imported files remain in Media even if result persistence is interrupted.

Each content/editor pair retains at most 40 active candidates. All routes require a signed-in editor, native CSRF validation and content access; importing media additionally requires `media.upload`. Author/contributor content ownership is enforced again at worker execution. Configuration requires `settings.manage`. Ordinary responses omit encrypted secrets, provider job IDs and binding internals. Anonymous reports are separate from native content and cannot enter this studio.

## Validation

Local fake-provider tests cover unsaved source, draft preservation, same-request idempotency, text edits, owner isolation, CSRF, disabled capabilities, destination pinning, actual generated image import and retention, MCP discovery versus exact tool calls/status, sanitized errors and private-network rejection. Real provider generation remains unverified until an operator supplies a supported provider connection and explicitly invokes it.
