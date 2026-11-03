# Public UI composition (phase one)

These presentation components use the active theme's tokens. They add no public mutation endpoints, business services or permission grants. Commerce, membership and other domain flows remain optional modules with their own server authorization.

A theme opts in with `"public-ui"` in its own `theme.json` `supports` array. The inherited default head then loads `public-ui.css`; declarations are not inherited by children. The default theme opts in and uses `public-state` for an empty search result. Custom heads can load the asset explicitly with `{{asset "public-ui.css"}}`. Existing template/component overrides retain precedence.

## Navigation action

```gotemplate
{{component "public-action" (dict "label" "View details" "url" "/record")}}
{{component "public-action" (dict "label" "Open application" "state" "requires_setup" "reason" "The application is not connected.")}}
```

The descriptor accepts `label`, `url`, `state`, `reason`. States are `available` (default), `disabled`, `unavailable`, `requires_setup`. Non-available or invalid descriptors never render an actionable link. Missing labels render nothing; missing inactive reasons get a localized unavailable message. Text is escaped. Available actions are navigation GET links only, not a means to invoke POST commands.

Only explicit HTTP(S), root-relative URLs and same-document fragments are accepted; protocol-relative URLs, credentials, controls, backslashes and unsafe schemes are rejected. Existing `url` localization applies only to known locale routes. Resolve translated content through the content linker or `destination`; the helper does not invent translated pages or infer permission from a URL.

## Public details and contextual navigation

`{{publicDetail .Content}}` returns a typed summary (title, excerpt, destination, actual locale, fallback flag) only for published, publicly readable content. Drafts, private content and missing items return nil, including staff previews. It does not expose fields, owners, credentials, full private records or capability state.

```gotemplate
{{component "public-navigation" (publicNavigation "primary")}}
{{component "public-detail" (dict "View" (publicDetail .Content)
  "Back" (publicReturn (index .Query "back"))
  "SecondaryTitle" "Before continuing"
  "SecondaryText" "Review the published scope before sending a request."
  "Action" (dict "label" "Read the full details" "url" "#full-details"))}}
```

`publicNavigation` uses the existing resolved locale menu and shows its top-level safe links. Full nested site navigation remains the existing menu component's responsibility. Exact current path is marked with `aria-current=page` only for a local/same-site link. Secondary detail text uses native `details/summary`, so disclosure needs no JavaScript. It is plain escaped text, not an HTML slot. Fallback summaries disclose their actual language and use its text direction.

The explicit default page template `public-detail` demonstrates the composition, with summary, full public body and a fragment action. Its `back` query value must be a safe root-relative URL; query parameters and fragment are preserved. Missing/external/unsafe values fall back to localized search. Callers should put the encoded discovery URL in `back` when linking to a selected record. No automatic history interception, JavaScript inspector or database-backed selection is implied.

## Feedback

```gotemplate
{{component "public-state" (dict "Kind" "empty" "Title" "No matches" "Text" "Try a different search." "Action" (dict "label" "Clear search" "url" "/search"))}}
```

Kinds `empty`, `error` and `unavailable` describe actual caller-known states. Unknown kinds fall back to unavailable. Error uses an alert role; other feedback uses status. This component does not manufacture success or decide whether a domain action is permitted. The caller supplies truthful domain data and recovery actions.

Styles are scoped to the new classes, wrap long strings, use logical spacing and visible keyboard focus, and switch detail actions to full-width controls on small screens. No sticky overlay covers the last field. Content/body sanitization retains the existing theme contract. The library does not add a generic state machine, public admin routes or new required module dependencies.
