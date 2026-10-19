# Product screenshots

Captured from the actual GoRun application with fictional Atlas Studio content.
The opt-in Playwright capture builds a fresh local server and uses isolated data;
no production site, account, token or customer information is involved.

From the repository root:

```sh
npm ci --prefix e2e
npx --prefix e2e playwright install chromium
README_SCREENSHOTS=1 npm --prefix e2e test -- readme-screenshots.spec.ts --project=chromium
```

The fixture uploads its own SVG, creates English and Arabic articles, opens the
real editor and builder, creates scoped REST/agent keys, and runs the canvas
review/grant/request/context/append workflow. A local test client supplies an
illustrative bilingual response through MCP; no model output or provider
connection is fabricated. The resulting note keeps its recorded provenance.
One-time credentials are cleared before capturing the API-key page. An actual
pending publishing approval is generated for the approval-list screenshot.

The three canvas story frames show selected input, outbound-context review,
and the separate returned draft. Additional Arabic and mobile frames support
manual layout inspection. Review every generated image before committing it,
including text clipping, card overlap, locale, media loading and secret leakage.
These are documentation images, not pixel snapshots or performance evidence.
