-- Guide series: an optional, non-hierarchical public taxonomy on posts whose
-- terms are series (a post's place in it is the post's series_order field,
-- content migration 0008). {{series .}} reads it; /series/<slug> lists a
-- series. Added only when absent.
INSERT OR IGNORE INTO taxonomies (slug, name_json, singular_json, hierarchical, types_json, public, system)
VALUES ('series', '{"en":"Series","ar":"السلاسل"}', '{"en":"Series","ar":"سلسلة"}', 0, '["post"]', 1, 0);
