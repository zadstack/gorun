---
name: Migration report / تقرير ترحيل
about: A WordPress migration that imported something wrongly or not at all
title: "migration: "
labels: wpmigrate
---

<!-- Do not paste application passwords, customer data or private content. -->

**GoRun version** (`gorun version`):

**Source**
- WordPress version:
- Read through: REST (anonymous / application password) or WXR file (`gorun import-wxr` or wizard upload)
- Relevant plugins (Yoast, WooCommerce, ACF, page builders…):

**Migration status** (from the wizard or `GET /api/v1/migrations/{id}`):

**Validation report**: attach or paste the relevant part of `/admin/migrations/{id}/report.md`
(counts per kind, broken links, missing media, render failures, shortcodes).

**What went wrong**: which item (WordPress type, id or slug) and what you expected to see in GoRun.

**Is it documented as not migrated?** Check "What does not move" in docs/MIGRATION.md first.

**Smallest reproduction**: if possible, a minimal WXR export or the item's REST JSON with private data removed.
