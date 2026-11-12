-- Term translations: a term belongs to one public content locale, and terms of one taxonomy
-- that are the same category/tag in different locales share a translation group ('' = not
-- translated). A locale appears at most once per group. Existing terms get the site's default
-- locale at boot (backfillLocales in store.go), so monolingual sites keep working unchanged.
-- Slugs stay unique per taxonomy across locales.
ALTER TABLE terms ADD COLUMN locale TEXT NOT NULL DEFAULT '';
ALTER TABLE terms ADD COLUMN translation_group TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX terms_translation_locale ON terms (translation_group, locale) WHERE translation_group != '';
CREATE INDEX terms_locale ON terms (taxonomy, locale);
