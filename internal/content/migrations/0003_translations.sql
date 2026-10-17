-- Translation groups: items of one type that are the same content in different locales share a
-- group id ('' = not translated). A locale appears at most once per group; trashing an item
-- takes it out of its group.
ALTER TABLE content ADD COLUMN translation_group TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX content_translation_locale ON content (translation_group, locale) WHERE translation_group != '';
