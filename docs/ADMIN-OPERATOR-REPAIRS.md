# Operator audit repairs

Scope: findings 26, 28–34, 36–48 and 50–53 from the October19 administration audit. This increment changes presentation and read-only view data; it does not change authentication, role permissions, backup verification, provider verification or navigation persistence. English and Arabic share the same behavior.

“Fixed” below means the specific observable defect is repaired, not that every proposed future feature in that finding now exists. “Incremental” explicitly leaves a recommendation open. Explanatory text alone is not counted as a structural fix.

| Finding | Status | Implemented | Still open |
|---|---|---|---|
|26 General settings|Incremental|Site/language sections, dirty indicator and sticky save area; shared identity source explained|Consolidate duplicate identity/theme editing across pages|
|28 Users|Fixed audience/time defects; incremental permissions|Localized relative sign-in time/full timestamp; server-filtered team/non-customer and customer views preserve search/role/pagination|Saved-role grant preview now available per user with ownership/status/recent-auth caveats; a full effective item-by-item access simulator remains out of scope|
|29 Login security|Incremental|Visible-sample counters before history, limitations folded; existing reasons/durations preserved|Aggregate historical metrics beyond the bounded50 failures/512 blocks are not collected here|
|30 Mail|Fixed selected-provider defect|Only selected provider fields shown, common sender retained; save→send→confirm sequence; secrets remain write-only|Actual inbox receipt remains an operator check, not a delivery claim|
|31 AI writing|Incremental|Off hides model/key/base URL; Anthropic hides OpenAI URL|A saved-connection selector replacing legacy settings requires a separate binding/migration design|
|32 Storage|Incremental|Explicit configured/tested/upload/restorable distinction; existing real connection tests retained|Destination quota/usage is unavailable; a stepwise activation wizard remains open|
|33 Webhooks|Incremental|Unambiguous outgoing versus signed incoming explanation|Event grouping and sanitized payload preview|
|34 Extensions|Incremental|Pre-install source/manifest/capability review guidance|Interactive capability review before installation|
|36 Workspace|Incremental|Mode-specific navigation visibility preview; explicitly independent of permission and provider activation|Independent agent/commerce toggles require extending the existing simple/full backend contract|
|37 Availability|Fixed conditional-field defect|Password controls appear only for password mode; visitor impact explained|A fully rendered visitor-page preview remains open|
|38 Page protection|Incremental|Saved policy and locale beside every bounded row; site/platform precedence and translation independence explicit|Effective cascade/source computation and a tabular policy editor|
|39 Report intake setup|Incremental|Pre-enable reviewer/privacy/follow-up checklist, explicitly not automatic verification|Reviewer assignment/readiness verification workflow|
|40 Roles|Fixed wildcard/comparison/review defects|Global wildcard says “All permissions”; exact-permission task matrix; saved-role clone entry points; before-save diff against saved permissions (including validation retries and administrator wildcard)|Dedicated named team bundles beyond existing role starting points remain optional product work|
|41 Health|Incremental|Automatic backup off presented as optional/off, actual warn/fail preserved; friendly core names and folded runtime diagnostics|Contextual optional billing checks and broader repair links|
|42 Errors|Incremental|Technical collector diagnostics folded below actual rows; recorded/retried does not imply resolved|Cross-source actionable default view needs a clear common status contract|
|43 Updates|Incremental|Backup→request→verify sequence and concrete deployment guide|Automatic post-deployment polling is not implemented; accepted request remains distinct from completion|
|44 Jobs|Incremental|Known kinds have human task labels, exact unknown kinds retained, “Attempt N of M”; technical IDs remain in details|Linked source entities and wider kind registry|
|45 Backups|Fixed summary hierarchy|Three independent first-page outcomes: last local verification, upload acknowledgement, restore test evidence unavailable; actual receipts/details preserved|There is no persisted end-to-end restore-test receipt, so the UI never fabricates one|
|46 Audit|Incremental|Known actions readable, raw action/target IDs folded, escaped literal actor-name search|Entity-name links, broader action vocabulary and pagination-scale directory search|
|47 WordPress migration|Fixed list-summary defect|Stage/rollback guidance plus actual per-kind queue totals, done/preview/pending/failed/skipped, failed-item and dry-run labels|Counts describe saved queue rows, not every source item or final validation success|
|48 Ordinary inbox|Fixed hierarchy defect|Messages before collapsed form directory/settings; empty next-step guidance|A contextual direct link to the exact published contact page needs a confirmed association|
|50 Custom navigation|Incremental|Live selected/pinned section and contextual-tab preview using the same authorized server projection as the shell; current form order/search remain independent of permissions|Task presets remain open; combined-tree acceptance must verify the shared three-category Website mapping|
|51 Private reports|Fixed hierarchy defect|Report rows before collapsed form directory; explicit separate reviewer boundary|No permission or private-report access changes|
|52 Metricool|Incremental|Numeric IDs and save→verify→draft guidance alongside real saved verification state|Field-specific illustrated official account-location help|
|53 SEO inventory|Incremental|Actual language/type filtering, priority ordering of field checks, editorial distinction; global totals explicitly stay unfiltered|Persisted filters and server-side report paging|

## Validation

- The full `internal/admin`, `internal/site` and `internal/auth` suites exercise existing authorization, secret nonreflection, settings conflicts, backup and policy behavior.
- New task-comparison regression rejects invented namespace wildcards and checks the exact saved permissions.
- Audit-name search tests include literal `%`/`_`; existing actor-ID/API filtering remains available.
- Real Chromium tests use a fresh synthetic installation: selected mail/AI/access fields, mobile backup summary/inbox hierarchy, navigation preview/search and task comparison, and an actual published page in SEO inventory.
- No live site, backup destination or provider is contacted by the new browser cases. No screenshot or report contains credentials.


## Guided operator follow-up

The account audience views use the saved `customer` role rather than guessing from activity or permissions. The team/other view is its complement and does not assert administration access. Invalid audience values are rejected; the same SQL predicate applies to both rows and pagination totals. API behavior and authentication are unchanged.

Role starting points use the existing clone route and existing grant validation. Administrator wildcard exclusion remains enforced by that route. The permission change panel compares current form fields with the actual saved role, not the last rendered checkbox state, so a validation retry still shows unsaved additions/removals. It is an informational preview; server permissions and recent-authentication checks remain authoritative at save.
