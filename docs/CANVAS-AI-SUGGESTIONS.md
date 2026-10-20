# Reviewed AI canvas suggestions

The canvas list can ask the existing site writing AI to propose up to three private workflow structures. This uses the current writing settings; a saved connection alone is not selected or activated automatically. The existing canvas search, status, built-in presets and private template library remain available.

1. Choose an unbound agent or text-provider destination for the proposed structure, then **Preview suggestions request**.
2. Inspect the displayed recipient provider, model and host, and the exact system instructions, user prompt and site context. Endpoint paths and credentials are omitted from the display because paths can contain capability secrets. The request includes the selected locale, destination kind, bounded site title and up to twelve public content-type slugs, when authorized; it includes no content bodies, keys or private connection catalog.
3. Check the approval box and separately confirm generation. The preview itself makes no upstream request. Changing the target clears approval; a changed site context or writing configuration requires a fresh preview.
4. Inspect a returned title and summary, then create its private board. Creation does not run anything. Choose actual content, an agent/model/provider and any required permissions in that board, then use the existing execution review. Schedules start disabled, and no grants, webhook credentials, tool policies or model-generated identifiers are imported.

The server stores a random, owner-bound review token for ten minutes. It can be consumed once. The exact writing configuration and monotonic revision are read in one SQLite snapshot; editing and restoring a setting cannot revive an old review. The confirmed call uses that same reviewed immutable configuration, without loading a different destination afterward. Writing permission and active user state are rechecked before accepting output. If context or configuration changes while the provider responds, its output is discarded.

Preview and suggestion caches are bounded and process-local. A restart or expiry requires another review/generation. Only the newest preview per owner is retained. A failed or uncertain generation is not retried automatically; review a new request. Cached suggestions can still be used after changing writing settings because applying creates only an unbound, disabled structure and makes no provider call. Repeating Apply for the same cached suggestion returns the same created board, rather than creating duplicates. Removing that board does not turn a repeated Apply into another creation.

Endpoints under `/admin/x/canvases/ai-suggestions` are browser-session, current-writing-permission operations. POSTs retain the existing CSRF protection; returned metadata is private/no-store. GET only reports configured state and that owner's cached titles/summaries. It does not probe providers. Upstream failures are returned/logged as a generic stage failure, without provider text, endpoint paths or keys.

## Contribution provenance

This implementation substantially revises the proposal in [PR #78](https://github.com/zadstack/gorun/pull/78), original commit `ad4d0047ca2935438a50c6731a9bc512c5fe07f5` by Cursor Agent. The original ref and authorship are preserved separately; this repair does not add a sign-off on that author's behalf. Contributions remain under AGPL-3.0-only.
