# Admin Lucide subset

GoRun ships the committed `lucide-core-1.55.0.js` on admin pages instead of
loading every icon. The existing full vendor and its license are unchanged.
There is no new runtime dependency or product build step.

The maintainer-only extractor uses the pinned local vendor's original renderer
and icon-node arrays. It includes all exact known icon-name string literals in
core Go, admin HTML/JS and JSON, including navigation registries. This is a
conservative superset (149 icons at introduction), not a list guessed from one
screen. Upstream changes fail the extraction until the pinned source is reviewed.

```sh
node tools/admin-icons/update.mjs          # intentional update; review generated diff
node tools/admin-icons/update.mjs --check # verify committed output is current
node --test tools/admin-icons/subset.test.mjs
node tools/admin-icons/browser.mjs        # existing e2e/node_modules required
```

The Go build embeds the resulting files directly. Normal GoRun users do not run
these commands or install Node. The existing embedded-vendor Go test verifies
HTTP delivery, integrity and license presence for both assets.

## Dynamic icons

`lucide.createIcons()` still renders synchronously for core names and preserves
`root`, `nameAttr`, `attrs`, `inTemplates` and explicit `icons` collections.
HTMX after-swap and Alpine's existing menu call require no changes.

An arbitrary extension/content-type `data-lucide` name outside the subset loads
the full same-origin pinned vendor once, with SRI and the current script nonce.
Pending calls are replayed, including scoped roots/options. Invalid names keep
upstream behavior (placeholder and warning). A failed request retains the
placeholder and permits the next explicit render to retry. Rendering known
icons does not wait on that request. No name is used to construct a URL.

Only the included named icon exports are initially present; code that directly
needs an arbitrary `lucide.SomeIcon`/`lucide.icons.SomeIcon` value must explicitly
load the original full vendor, or supply its own icon nodes to `createIcons`.
No existing core caller uses those arbitrary synchronous exports. Data-driven
extension/navigation names continue to work through the lazy fallback.

## Evidence and limits

The byte budget and geometry parity tests compare against the actual pinned
vendor. Chromium checks cover DOMContentLoaded, HTMX refresh, the API invoked by
Alpine, custom nodes, attributes, templates, lazy fallback, load failure/retry,
and a caller retaining the original wrapper after fallback finishes.

Native before/after acceptance uses separate synthetic GoRun instances, real
embedded assets and native CSP/SRI. No production account/data/settings change
is needed. Reducing bytes is proven; whole-page timing can still be dominated
by other scripts, CPU and network variance. Do not advertise this as a proven
fix for an intermittent hang without reproducing that hang.

Official API reference: https://lucide.dev/guide/lucide/getting-started
