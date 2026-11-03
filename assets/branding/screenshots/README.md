# GoRun product screenshots

The canonical PNGs were last refreshed for 0.18 (from main at 0.18.x, after #195) from the actual GoRun
application, using fictional Atlas Studio content and the sample
editor Amira Hassan. One fresh local fixture supplies the editor, content list,
site design, role catalog, canvas story, navigation, agent drawer, approvals,
public theme, AI-suggestion/extension-method views, the canvas Lanes and schedules views, and site forms. Existing user or
production data is never opened or changed. These local images do not establish
which application version runs on GoCloud or any other hosted site.

From `e2e/`, with a supported Node runtime and Go available (the `setup` project signs in the first admin):

```sh
npm ci
npx playwright install chromium
README_SCREENSHOTS=1 npx playwright test tests/global.setup.ts tests/readme-screenshots.spec.ts --project=setup --project=chromium
```

The nine `researcher-*.png` images come from the Updates Researcher story test. Run it, then
copy its output: `recipe` becomes `setup`, `recipe-review` becomes `review`, `recipe-result`
becomes `result`; `-en` stays, `-ar` (390 px) becomes `-ar-mobile` and `-ar-desktop` becomes `-ar`.

```sh
npx playwright test tests/global.setup.ts tests/agent-recipes.spec.ts --project=setup --project=chromium -g "Updates Researcher recipe EN/AR|first admin"
```

The READMEs show desktop screenshots two per row (HTML tables at 50% width, each image linking
to its full size); mobile captures are linked rather than embedded.

The opt-in capture boots a fresh temporary server, uploads repository-owned SVG
artwork, creates English/Arabic articles and a real builder page, and generates
scoped REST/agent keys. It pins Canvases, Media and Site design for the same
personal Quick access view throughout. The four site-design and three role
screens are captured in this fixture too, rather than copied from another run.
The recipe verifies that every canonical PNG was refreshed.

The three canvas story frames show selected input, actual outbound-context review
and a separate private result. A local external test client supplies the
illustrative bilingual response through the real grant/request/context/append
MCP workflow. The note keeps that recorded provenance; it is not a live model or
model-quality benchmark. Opening the drawer is checked to create no extra runs;
configured state is never relabelled online. One-time key and webhook signing
secrets stay out of screenshots. The recipe checks known plaintext keys against
page HTML before every capture and requires all one-time reveal panels absent.

The four new 0.10 views are:

- `canvas-ai-suggestions-en.png` and `canvas-ai-suggestions-ar-mobile.png`: three
  illustrative suggestions returned by a scripted local HTTP provider. The real
  application shows the exact destination/context preview, makes zero provider
  calls at preview, then sends that exact payload only after a separate checked
  confirmation. Exact-review evidence is saved to Playwright's test output, not
  added as an oversized README image. No actual model/provider availability is
  claimed, and no suggestion is applied or run during this capture.
- `extensions-methods-en.png` and `extensions-methods-ar-mobile.png`: fictional
  installed records showing server-directory, uploaded-ZIP and legacy-unknown
  methods. These capture-only records are not claims of official origin, package
  trust, endpoint reachability or successful external-service integration.

An actual pending publishing approval is generated for the approvals screenshot.
Public theme previews retain the same Atlas Studio name and synthetic artwork.
Review every generated image before committing it: locale, clipped text, mobile
scrolling, canvas overlap, media loading and credential leakage. Images are
ordinary browser screenshots, not composited UI, performance measurements or
pixel-perfect test baselines. The capture does not deploy software or publish a
site.

Five screens were added for 0.18. All are seeded into the same fresh fixture, with synthetic data only:

- `canvas-lanes-en.png`: the Atlas Studio campaign board in the Lanes view. The capture checks that
  switching to Lanes leaves the saved board version unchanged.
- `canvas-schedules-en.png`: a separate "Weekly editorial brief" board from the periodic-brief preset,
  in the Workflow view. One `running` row (with `started_at`) is inserted into `admin_canvas_runs` and
  one enabled row into `admin_canvas_schedules`, the way the canvas specs seed them, so the agent shows
  its live state and the edges flow, with the edge legend and the expanded Scheduled tasks panel. Nothing
  is run and no provider is called; the schedule and note cards are moved below the floating panel.
- `forms-en.png` and `forms-ar.png`: two forms are built and published through the page builder (an English
  "Book a call" and an Arabic "طلب موعد"), then five fictional entries each (two unread) are inserted into
  `form_entries`. This avoids the shared per-IP limit on public `/forms/*` posts; no visitor submits anything.
- `builder-form-en.png`: the page builder with the English Form section open, scrolled to the form
  settings and field editor. The live preview uses the fixture theme.
