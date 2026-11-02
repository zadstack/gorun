# Shared site UI acceptance

Date: 2026-11-03. Source: GoRun core branch based on main at 6065414. All browser writes used a disposable, synthetic localhost fixture. No production runtime, customer theme or data was changed.

## Result

- Shared published identity subset used by launch, Membership and SaaS. Validated color/font tokens only; draft/custom CSS excluded; safe fallback with theme disabled or missing dependencies.
- Default inherited customer account has a compact guest surface, alternative methods disclosed on demand, and separate signed-in service/order/invoice sections.
- Native directory `compact`, media_text `image_start` and `image_end` available in the editor and accepted/rendered only for supported native kinds. Existing documents and legacy defaults preserved.
- Membership explains missing submission policies. SaaS empty messages remain readable outside wide tables.

## Automated validation

- `go test -count=1 ./...`: passed across the repository after integration fixes.
- `go vet ./...`: passed.
- `make css` and `make css-check`: passed; generated stylesheets committed.
- `go build -trimpath -o /tmp/gorun-core-ui ./cmd/gorun`: passed after final source changes.
- `node --check web/admin/static/builder.js` and `node --check web/launch/launch-polish.js`: passed.
- Final section stylesheet budget regression passed: 32,554 bytes raw and 6,797 gzip, within existing 32KiB/8KiB limits. Budget was preserved rather than increased.
- Membership nonce/ownership regression repeated 10 times after fixing the test to decode HTML attribute entities; passed. This was a test comparison issue with encoded nonce characters, not an application CSP change.
- Focused coverage includes unsafe identity fallback, font allowlists, custom-CSS/draft exclusion, nil/theme-disabled dependencies, mixed optional-module activation, CSRF/next preservation, account feedback visibility, owner isolation, variant roundtrip/sanitization, invalid cross-kind/legacy variants and unchanged default rendering.

## Actual browser verification

Codex in-app browser, localhost fixture on port 8931; default desktop 1280 and mobile 390×844. Final console error log empty. Temporary viewport restored.

1. Opened Arabic account; email-link and registration disclosures opened/closed. No registration or email was submitted.
2. Dark identity initially exposed inherited white input surfaces with white text. Scoped account fields/help/feedback were corrected; computed input background rgb(20,45,54) and text rgb(247,245,237) matched the validated saved pair after correction. Captured final guest screens with the supported dark scheme enabled.
3. Opened English and Arabic launch sign-in, Arabic start and verify covered by Go handlers. Empty start submission was blocked by native required-field validation and focus moved to name; no account created. Custom JavaScript field-error association is source/syntax checked, not claimed as a completed browser success-flow test.
4. Used a temporary fixture-only sign-in link for the synthetic staff account. Signed-in account showed separate empty services/orders and store actions. Membership/SaaS used the same published identity. Anonymous boundaries remain tested by Go integration tests.
5. Final Membership showed a configuration notice instead of an unusable empty submission form. Final SaaS mobile width 390, scrollWidth 390, no empty tables. Populated ownership, quota, CSRF and state controls are covered by Go tests; no billing/real subscription operation was exercised in the browser.
6. Created a synthetic builder page through existing APIs, saved its native document with the current draft token, and published only to localhost. Used a bundled company starter preview as an illustrative image; starter posts supplied the directory.
7. Measured RTL desktop media rows: image_start image at x656 vs copy x104; image_end reversed. Mobile rows used one column and image order0. No page overflow at390.
8. Initial compact CSS lost to the stock theme card's flex declaration. Closed component selectors were corrected; final desktop cards computed grid 96px + text, mobile 64px + text. Directory width stayed within viewport.
9. Directory search for «مراجعة» returned 2 full-text matches and preserved its query. Cleared by normal navigation afterward.
10. Opened real native editor controls, changed compact to default, saved and reloaded; value persisted. Restored compact, changed first media section to image_end, saved/reloaded, then restored image_start. Each save confirmed the published page was unchanged. No subsequent browser publication was performed.
11. Signed out of the synthetic staff session. Left the public branded launch preview as the review tab.

## Evidence

- [Branded launch desktop](launch-branded-en.png), [Arabic launch mobile](launch-ar-mobile.png), [Native required validation](launch-validation-ar-mobile.png).
- [Account desktop](account-ar-desktop.png), [Account mobile](account-ar-mobile.png), [Signed-in account](account-signed-in.png).
- [Membership](membership-branded.png), [SaaS desktop](saas-branded.png), [SaaS mobile](saas-mobile.png).
- [Native variants](native-variants-desktop.png), [Compact desktop](directory-compact-desktop.png), [Compact mobile](directory-compact-mobile.png), [Editor](editor-native-layouts.png).

## Limits and rollout

No merge, production deployment or Fleet rebuild belongs to this core change. Custom `account.html` overrides remain authoritative; custom cards may need their own styling under the compact hook. Membership/SaaS retain their pre-existing Arabic text. Launch uses the published primary media logo; theme-manifest/dark-logo variants are not added. Full WCAG certification, every third-party theme and a real payment/email workflow were not tested. A site-specific theme/content pass can follow the core review; these changes do not rewrite existing custom homepage compositions.
