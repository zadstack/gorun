# GoRun core and independent WordPress importer

Status: staged implementation. This PR implements sitemap snapshots, revision
invalidation, background refresh, index/parts, indexed keyset enumeration and
batched permalink/term lookups.
Importer extraction, locale configuration, multilingual migration, reconciliation
and SQLite retry policy remain subsequent changes. The importer is still bundled
and optional in this PR; it is not yet a separately distributed extension.

## Goal

Keep GoRun an independent CMS. Put WordPress-specific discovery, parsing,
conversion and migration reconciliation in an optional, separately distributed
GoRun extension. Imported records must become ordinary GoRun records: rendering,
editing, search and publishing must continue after the importer is removed and
the WordPress source is offline. Generic improvements belong in GoRun regardless
of which importer, API client or editor creates the data.

## Current evidence

- `internal/wpmigrate/module.go`: the migrator already has an optional-module
  activation gate. Disabling it is not the same as removing it from the binary.
- `pkg/cli/modules.go` includes the module, and `pkg/cli/commands.go` imports it
  directly for WXR commands. It depends on internal GoRun packages.
- `internal/kernel/boundaries_test.go` forbids core modules from importing feature
  modules; it explicitly permits the migrator's commerce dependency.
- `internal/commerce/contract.go` and `internal/commerce/products.go` expose
  `WPProduct`, `WPProductID` and `UpsertFromWP`. WordPress conversion therefore
  currently extends beyond the migration module.
- `internal/ext/module.go` and `docs/EXTENSIONS.md` define external-program
  extensions using manifests, scoped REST keys and an HTTP hook bridge. They do
  not provide native dynamically loaded Go plugins.
- `internal/content/translations.go` already implements content translation
  relationships. `internal/taxonomy/contract.go` has no corresponding term locale
  or term translation-group fields.

## Scope

### Required in GoRun

- Sitemap snapshots, batched link resolution, background generation and indexes.
- Configurable public content locales independent of admin catalogs, including
  canonical BCP-47 tags and localized routing.
- Generic translation relationships for content and terms, and localized menus.
- Public operations for content types, taxonomies, terms, media, authors, menus,
  SEO, redirects and translation relationships. Audit existing REST coverage
  before adding anything.
- Bounded, observable SQLite contention handling for safe operations.
- Source-neutral external identity and idempotency support wherever existing
  operations cannot safely resume an import.

### Required in the importer extension

- WordPress REST and WXR readers, WPML/Polylang adapters and source diagnostics.
- Discovery and explicit mapping of custom types and taxonomies, including
  hierarchy and content-type attachments.
- Gutenberg, WordPress metadata and Yoast conversion; localized URL mapping.
- Migration checkpoints, source-to-destination maps, retries and reconciliation.
- Per-type and per-locale counts, featured-image, author, term, translation,
  menu and SEO comparisons. Unavailable source evidence is reported as unknown.
- Optional WooCommerce conversion, calling generic commerce operations rather
  than exposing WordPress types in commerce contracts.

### Excluded

- Building a WordPress site, theme or PHP plugin.
- Requiring a WordPress connection for normal GoRun operation.
- A new native Go plugin loader or a new general plugin framework.
- Removing existing migration history, imported data or legacy identity columns
  as part of extraction.
- Patching the already-present `middleware.GetHead` correction again.

## Stack and architecture

Retain Go and SQLite for GoRun. Use a separately built Go executable for the
importer, integrated through the existing external-extension mechanism and
scoped REST APIs. This keeps the importer out of the GoRun binary and avoids Go
`internal/` package restrictions. Extension packaging and startup must be made
concrete before extraction; the current extension installer is not assumed to
launch a standalone service automatically.

```text
WordPress REST / WXR / WPML / Polylang
                  |
                  v
Independent importer process
  readers -> source model -> mapping -> checkpointed pipeline
                                   -> reconciliation report
                  |
           scoped GoRun REST operations
                  |
                  v
GoRun native records and relationships -> normal site rendering
```

The importer owns source credentials, discovery payloads, migration runs and
source identity maps. GoRun owns imported content, media, users, taxonomy,
translation relationships, menus and redirects. No public request may call the
importer to interpret imported content. Generic bulk endpoints must retain the
same validation, permissions, auditing and event semantics as ordinary writes.

The importer must not declare imported content types as disposable extension
assets: uninstall/purge semantics must never remove the site's migrated data.
Existing extension deactivation leaves its REST key valid; stopping a migration
worker and revoking access need explicit lifecycle handling.

## Dependency-ordered delivery

1. **Baseline and contracts (M).** Inventory reads, writes, jobs, CLI and UI
   registrations; map each to existing public APIs and record gaps. Measure
   sitemap duration/query counts and transaction contention using representative
   data. Add regression fixtures for existing imports before moving code.
2. **Sitemap scalability (L), independently shippable.** Batch settings, parent,
   translation and redirect resolution. Generate versioned snapshots in a
   background worker, publish atomically and prevent duplicate concurrent builds.
   Add invalidation and periodic repair, indexes and bounded-size parts. Specify
   cold-start and private/deleted-content behavior; do not blindly serve a stale
   snapshot after a visibility change. Add HTTP conditional responses.
3. **Public locale model (L).** Separate public language configuration from admin
   catalogs. Preserve full language tags, routing, slug reservations and locale
   defaults. Keep existing Arabic/English sites compatible. Add term translation
   relationships and define translated parent/menu behavior.
4. **Source-neutral import operations (L).** Fill the public API gaps identified
   in step 1. Define source-scoped external identities and conflict policies.
   Move WooCommerce transformation out of commerce; provide generic operations
   while retaining compatibility with existing product records and integrations.
5. **SQLite contention improvements (M), parallel in dependency order.** Measure
   lock wait and transaction duration; shorten transactions and remove external
   I/O inside them. Retry only classified transient failures within a total
   elapsed-time budget, with backoff/jitter and context cancellation. Never
   silently replay callbacks with external side effects or uncertain commits.
6. **Independent importer packaging (L).** Build a separate executable and
   extension manifest, credential setup and explicit start/stop workflow. Use
   public GoRun APIs only. Port WXR/REST, jobs, state and reports; keep the old
   importer available during transition without permitting simultaneous writers
   for the same migration. Provide a migration-history handover/export path.
7. **Custom taxonomy automation (M).** Discover definitions, propose mappings and
   conflicts in preview, create selected taxonomies and attach target types.
   Create terms before resolving parents, then assign terms to content.
8. **Multilingual import (L).** Implement separate WPML and Polylang readers with
   explicit source-capability reports. Create native records in their own
   locales, then link translations and translated parents. Preserve reachable
   old URLs using redirects where native GoRun routes differ.
9. **Reconciliation and cutover (L).** Compare source manifests against native
   records using source identities and selected scope. Verify intentional skips,
   missing records, mismatches and unknown checks separately. Remove direct
   importer inclusion and CLI imports only after parity and history handover.
   Verify operation with the importer absent and the source disconnected.

Steps 7–9 build reconciliation checks incrementally rather than postponing all
verification to cutover. Effort labels are relative, not delivery dates.

## Risks and decisions

- External process lifecycle, authenticated admin entry points and deployment
  packaging need a concrete design during step 6. Default: existing extension
  mechanism plus an explicitly managed companion executable.
- Some WordPress installations do not expose plugin translation data through
  REST or WXR. Report unsupported evidence; never infer translation groups from
  similar slugs or titles.
- Multiple source sites can reuse the same numeric IDs. Identity must include
  source and entity kind, not only the WordPress integer.
- Existing commerce/billing integrations may rely on WordPress-specific fields.
  Preserve compatibility until their callers and stored data are audited.
- Independent distribution requires a supported API compatibility range and
  importer/GoRun version handshake, not imports of private Go packages.
- Large imports need bounded batches, durable checkpoints and meaningful lock
  metrics. Bulk speed must not bypass domain rules or post-commit invalidation.

## Acceptance criteria

- GoRun builds and serves without the importer package/process; public pages,
  admin editing, media, translations, menus and redirects still work.
- Removing the importer does not delete migrated content or require WordPress.
- A fresh install with no importer has no migration routes, workers or source
  credentials. Existing migration history remains recoverable during upgrade.
- The separate importer does not import GoRun `internal/` packages or write
  directly to the GoRun database.
- A sitemap fixture exceeding 50,000 entries is fully represented by index/parts;
  cached requests perform no per-item permalink queries. Regeneration failure
  preserves atomicity and cannot publish partial files.
- French and regional language tags work for public content while the admin
  remains Arabic or English; existing URLs retain their documented behavior.
- WPML and Polylang fixtures preserve native translation groups, translated
  terms, parents and menus; unavailable metadata produces an explicit limitation.
- Selected custom taxonomies are created with hierarchy, type attachments and
  assignments. Import preview explains naming conflicts before writes.
- Resuming an interrupted import creates no duplicate records, media or groups.
- Reconciliation detects wrong authors, missing featured images, translation and
  term mismatches, menu errors, SEO differences and per-type count discrepancies.
- Contention tests prove bounded retries and cancellation without duplicate
  side effects; non-transient errors are returned immediately.

## First three actions

1. Produce the public API gap inventory from the current importer call sites,
   including commerce, migration history and the WXR CLI.
2. Compare the shipped sitemap benchmark and 50,001-record regression against
   representative deployment data, then capture an import baseline.
3. Implement public locale contracts and the API gaps needed by the independent
   importer, followed by extension packaging and the migration-history handover.
