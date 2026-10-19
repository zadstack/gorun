# Content and website audit follow-up

Scope: administrative audit steps 1, 3–20 and 55–59 from the 2026-10-19 local audit. These changes preserve existing server permissions, edit tokens, CSRF, draft/publication behavior and explicit section-layout publication. They are a useful first increment, not a claim that every proposed redesign has shipped.

| Step | Implemented in this change | Remaining recommendation |
| --- | --- | --- |
| 1 | In-place English/Arabic labels and layout, including returned field errors; account and site sections. Changing language keeps entered fields and does not store credentials. | None for the audited language/section issue. |
| 3, 59 | Language/author filters, My drafts/For review, row language/author, selection-only bulk bar, compact mobile filters. Author restrictions remain enforced by the server. | Further mobile contextual-navigation changes belong to the shared shell. |
| 4 | One explicit update action for published content, selected-status labels, unsaved notice, sticky publishing panel, optional SEO/studio sections. Changing a published item to Draft explicitly says it unpublishes. | Full editor tabs and rich-text toolbar accessibility naming audit. |
| 5 | Visible parent title in page rows. | Complete collapsible tree, cross-page hierarchy handling, dedicated preview shortcut and locale-specific new-page grammar. |
| 6 | Grid/list switching; clearer empty guidance. Existing upload progress remains. | Server-side type/size filters, usage graph and metadata drawer. |
| 7 | Empty-state moderation guidance. | Article context snippets and inline moderation redesign. |
| 8 | Permission-filtered direct setup link when Metricool is not ready. Existing explicit destination review/no automatic publication remains. | Rich composed-post preview. |
| 9 | “Custom fields” distinguishes extra fields from built-in editor fields. | Type presets and generated-editor preview. |
| 10 | Advanced taxonomy creation is collapsed and automatically opens on returned errors. | Full category tree management redesign. |
| 11 | New authorized website overview grouped into pages/navigation, design/identity and search/readiness. | Shared sidebar destination/group wiring is owned by the shell change; redirect path testing is separate. |
| 12 | Choose → introduce → review wizard, optional details, real bundled preview assets independent of installed theme files and descriptive failure fallback. Draft creation and later explicit activation remain separate. | None for this wizard/thumbnail increment. |
| 13 | Local bilingual identity sample updates title, tagline, colors and font without saving; inheritance caveat is explicit. | Curated palettes and per-field effective-source display. |
| 14 | Failures first, then other unresolved checks, passed checks folded. Existing evidence and fix URLs stay intact. | More specific per-check action labels. |
| 15 | Human header/footer location labels. | Visual menu placement preview and new tree editor. |
| 16 | Existing separate theme preview/activation controls retained. | No additional theme-catalog redesign in this increment. |
| 17 | Existing site-reference behavior retained. | Searchable single-preview workspace and source-state badges. |
| 18 | Removed storage-key jargon; clarified theme overrides versus shared identity. | Full header/footer/typography tabs, live preview and per-value inheritance/reset controls. |
| 19 | Advanced title, filename search and page/part grouping over existing template controls. | New original-versus-override comparison UI; existing native draft/publication guards remain. |
| 20 | Local title/description search sample, crawler rules folded, explicit warning for root-path blocking. | Dedicated social-card preview. |
| 55 | Explains text versus sections, separate section draft, public-body replacement and saving before switching. | Persistent layout-state badges across both editors. |
| 56 | Import/export and layout history moved into explicit advanced/optional sections; existing section order and preview remain. | Full section tree, central preview and properties drawer plus starter patterns. |
| 57 | Owned by the shared shell/SaaS change; no SaaS files changed here. | Verify combined integration. |
| 58 | Image fields can use the real media picker only with media permission; design/scene controls are optional and keep their open state. | Click-to-select properties from the sandboxed preview. |

Browser fixtures exercise actual local admin screens with synthetic content. No production site is modified. PNG starter previews are copied from the repository’s existing theme screenshots, rather than generated mock interfaces.
