# Default theme and editable examples

The default Classic theme uses the saved site name, description, logo,
menus and button settings. Its home page features a real published article,
shows the remaining articles once, and links to up to three published pages in
the current locale. Empty sites show an honest empty state. It does not create
services, testimonials, customer logos, translations or performance claims.

## Try the bundled sample on an empty local site

```sh
gorun starter --data ./example-site
gorun start --data ./example-site --local-only
```

`starter` imports five Arabic example articles and four pages. Every body clearly
identifies itself as editable example content. The About page also demonstrates
the section library. `starter` skips a site that already contains non-trash
content; updating the executable or theme does not replace existing writing.
`start` keeps its normal account/data behavior and does not automatically import
this sample. The theme's English and Arabic interface labels are localized;
authored Arabic articles are not automatically translated into English.

The examples use general writing, project-story and review scenarios, with no
independent business brands, fabricated results or claims that payment/AI
providers are already configured. Delete or replace them before sharing your site.

## Reproduce browser evidence

From `e2e/`, after installing its locked dependencies and Chromium:

```sh
npx playwright test tests/default-theme-refresh.spec.ts --project chromium
```

This creates and removes its own temporary site, imports the real WXR through the
CLI, verifies routes, exact article counts, repeat-import preservation, RTL/mobile
layout, button contrast and English empty-state behavior. It never edits the
shared test database or a running installation. For synthetic public screenshots,
set `DEFAULT_THEME_CAPTURE_DIR` to an output folder. Set
`DEFAULT_THEME_SCREENSHOT=1` only when intentionally refreshing the bundled
`themes/default/screenshot.png` from that actual browser view. The screenshot
uses a deliberately assigned preview name and description, not customer data.
