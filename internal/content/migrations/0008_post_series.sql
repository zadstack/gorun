-- Guide series on posts: attach the "series" taxonomy (taxonomy migration
-- 0002) and add an indexed number field, series_order, for a post's place in
-- its series. Each change applies only when absent, so a site's own
-- definitions are kept.
UPDATE content_types
SET taxonomies_json = json_insert(taxonomies_json, '$[#]', 'series'),
    updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
WHERE slug = 'post'
  AND NOT EXISTS (SELECT 1 FROM json_each(content_types.taxonomies_json) WHERE json_each.value = 'series');

UPDATE content_types
SET fields_json = json_insert(fields_json, '$[#]', json('{
  "key": "series_order",
  "label": {"en": "Order in series", "ar": "الترتيب في السلسلة"},
  "type": "number",
  "required": false,
  "indexed": true,
  "multiple": false,
  "default": null,
  "help": {
    "en": "This post''s place in its series (1, 2, 3…). Posts without a number follow the numbered ones, oldest first.",
    "ar": "موضع هذه المقالة في سلسلتها (1، 2، 3…). تأتي المقالات بلا رقم بعد المرقّمة، الأقدم أولًا."
  }
}')),
    updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
WHERE slug = 'post'
  AND NOT EXISTS (
    SELECT 1 FROM json_each(content_types.fields_json)
    WHERE json_extract(json_each.value, '$.key') = 'series_order'
  );
