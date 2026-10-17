-- Arabic-normalised full-text search (roadmap 1.4). Same columns and tokenizer as
-- content_fts, but Go writes the text folded by i18n.NormalizeArabic (أإآٱ→ا, ى→ي, ة→ه,
-- ؤ→و, ئ→ي, no tatweel or harakat) and folds every query the same way, so أحمد/احمد and
-- مُحَمَّد/محمد match. Additive: content_fts is still written for one release so an older
-- binary keeps working on this database; it is dropped in a later, separate migration.
-- The service backfills this table on first boot (ensureSearch) and `gorun reindex`.
CREATE VIRTUAL TABLE content_fts_v2 USING fts5(content_id UNINDEXED, title, excerpt, body, tokenize = 'trigram');
