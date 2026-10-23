-- Page purpose: an optional, indexed select on the page type that says what a
-- page is for (about, service, methodology, contact). Themes resolve purpose
-- pages with {{destination "<purpose>"}} and render them with
-- page-<purpose>.html when the theme has one. Added only when the page type
-- has no "purpose" field yet, so a site that defined its own keeps it.
UPDATE content_types
SET fields_json = json_insert(fields_json, '$[#]', json('{
  "key": "purpose",
  "label": {"en": "Page purpose", "ar": "غرض الصفحة"},
  "type": "select",
  "required": false,
  "indexed": true,
  "multiple": false,
  "default": "",
  "help": {
    "en": "What this page is for. Themes link to it by purpose and may give it a matching layout.",
    "ar": "الغرض من هذه الصفحة. تربط القوالب إليها حسب الغرض وقد تعرضها بتخطيط مناسب."
  },
  "options": [
    {"value": "about", "label": {"en": "About", "ar": "من نحن"}},
    {"value": "service", "label": {"en": "Service", "ar": "خدمة"}},
    {"value": "methodology", "label": {"en": "Methodology", "ar": "منهجية العمل"}},
    {"value": "contact", "label": {"en": "Contact", "ar": "تواصل"}}
  ]
}')),
    updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
WHERE slug = 'page'
  AND NOT EXISTS (
    SELECT 1 FROM json_each(content_types.fields_json)
    WHERE json_extract(json_each.value, '$.key') = 'purpose'
  );
