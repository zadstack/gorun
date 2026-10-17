# Connection setup guidance

The presets follow the providers' official documentation and the existing GoRun adapters. The helper only fills an empty AI endpoint after an explicit click. It does not save, test, activate or call a provider. Existing custom URLs remain untouched. No model, credential, bucket, account or region is inferred.

- OpenAI: `https://api.openai.com/v1`, matching GoRun's appended `/chat/completions`. [Official API reference](https://developers.openai.com/api/reference/overview).
- Anthropic: `https://api.anthropic.com/v1`, matching the existing GoRun activation constraint and `/v1/messages` adapter. [Official API overview](https://platform.claude.com/docs/en/api/overview).
- MCP: the operator supplies the exact HTTP endpoint; no universal default or automatic OAuth support is implied. Discovery remains separate from reviewed tool execution. [Official remote server guide](https://modelcontextprotocol.io/docs/develop/connect-remote-servers).
- Cloudflare R2: copy the actual account/jurisdiction S3 endpoint and use region `auto`; the helper never synthesizes the account URL. [Official S3 setup](https://developers.cloudflare.com/r2/get-started/s3/) and [jurisdiction endpoints](https://developers.cloudflare.com/r2/reference/data-location/).
- Amazon S3: use the bucket's actual regional API endpoint and configured credentials; no region is guessed. [Official endpoint/addressing documentation](https://docs.aws.amazon.com/AmazonS3/latest/userguide/VirtualHosting.html).

Storage guidance preserves separate media and encrypted-backup activation. Metricool already has its official setup link. SMTP and Resend are selected in Mail settings with separate credentials; changing the selection does not send mail. Use the explicit test action after saving. SMTP remains the default.

## MCP compatibility

The HTTP connection check and Studio support Streamable HTTP protocol versions `2025-11-25`, `2025-06-18`, and `2025-03-26`. They offer the newest supported version, accept only this explicit set, and retain the negotiated version on every subsequent request. Unsupported versions stop before tool discovery or execution. This does not add legacy HTTP+SSE transport, local stdio process launching, or OAuth login. See the [official version negotiation lifecycle](https://modelcontextprotocol.io/specification/2025-11-25/basic/lifecycle).

A successful connection check means the selected model was listed, MCP discovery succeeded, or the bucket answered its read probe. It does not prove that generation, an arbitrary tool, media writes or backup restoration will succeed. These need a separate explicit operation with configured credentials. Connection secrets remain write-only: blank preserves them and the clear control removes them.
