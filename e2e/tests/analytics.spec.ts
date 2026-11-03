import { expect, type Browser, type BrowserContext, type Page, test } from "@playwright/test";
import { dirname, join } from "node:path";
import { DatabaseSync } from "node:sqlite";
import { db, env, setContentLocale, setLocale } from "../lib/helpers.ts";

// Built-in cookieless analytics end to end. A logged-out visitor context
// browses; the admin dashboard (and analytics.db) must show it. The write
// buffer flushes every 2s, so every assertion on counts polls.
//
// The admin context is signed in, so its own browsing is staff traffic and
// must add nothing.

const stamp = Date.now().toString(36);
const pageSlug = `e2e-an-${stamp}`;
const pagePath = `/en/${pageSlug}`; // English content lives under /en/ (the site locale is Arabic)
const formSlug = `e2e-an-form-${stamp}`;
const saved = /حُفظت|Draft saved/;

function analyticsDb(): DatabaseSync {
  const d = new DatabaseSync(join(dirname(env("E2E_DB")), "analytics.db"));
  d.exec("PRAGMA busy_timeout = 5000");
  return d;
}

// views counts stored view events, optionally for one path prefix.
function views(pathLike = "%"): number {
  const d = analyticsDb();
  try {
    return (d.prepare("SELECT COUNT(*) AS n FROM events WHERE kind = 'view' AND path LIKE ?").get(pathLike) as any).n;
  } finally {
    d.close();
  }
}

async function visitor(browser: Browser, init?: string): Promise<{ context: BrowserContext; page: Page }> {
  const context = await browser.newContext({ storageState: { cookies: [], origins: [] }, locale: "en" });
  if (init) await context.addInitScript(init);
  return { context, page: await context.newPage() };
}

// beacons records every request to /_a/e a page makes.
function trackBeacons(page: Page): { count: () => number } {
  let n = 0;
  page.on("request", (r) => {
    if (new URL(r.url()).pathname === "/_a/e") n++;
  });
  return { count: () => n };
}

const card = (page: Page, name: string) => page.locator(`[data-card="${name}"] [data-value]`);
const num = async (page: Page, name: string) => Number((await card(page, name).innerText()).replace(/[^\d]/g, ""));

async function buildFormPage(page: Page) {
  await page.goto("/admin/content/page/new");
  await setContentLocale(page, "en");
  await page.fill("#title", `E2E ${pageSlug}`);
  await page.fill("#slug", pageSlug);
  await page.locator('button[name="intent"][value="save"]').click();
  await expect(page).toHaveURL(/\/admin\/content\/page\/[0-9A-Z]{26}$/);
  await page.goto(`/admin/builder/${page.url().split("/").pop()}`);

  await page.locator("#builder-kind").selectOption("form");
  await page.locator('[data-action="add"]').click();
  const form = page.locator("#builder-sections > section").last();
  await form.locator('input[data-field="title"]').fill("Analytics form");
  await form.locator('input[data-form="slug"]').fill(formSlug);
  await form.locator('textarea[data-form="success"]').fill("Thanks, analytics");
  const fields = form.locator("fieldset[data-form-field]");
  while (await fields.count()) await fields.first().getByRole("button").filter({ hasText: /Remove field|حذف الحقل/ }).click();
  await form.getByRole("button", { name: /Add field|إضافة حقل/ }).click();
  const field = fields.last();
  await field.locator('input[data-form="label"]').fill("Name");
  await field.locator('input[data-form="name"]').fill("name");
  await field.locator('select[data-form="type"]').selectOption("text");
  await field.locator('input[data-form="required"]').check();

  await page.locator('[data-action="save"]').click();
  await expect(page.locator("#builder-status")).toContainText(saved);
  await expect(page.locator('[data-action="publish"]')).toBeEnabled();
  page.once("dialog", (dialog) => dialog.accept());
  await page.locator('[data-action="publish"]').click();
  await expect(page.locator("#builder-status")).toContainText(/Page published|نُشرت/);
}

test.describe.serial("site analytics", () => {
  test.setTimeout(180_000);

  test("visitor is counted, staff and DNT are not, hash changes do not double count", async ({ page, browser }) => {
    await page.goto("/admin");
    await setLocale(page, "en");
    await buildFormPage(page);
    // Publishing announces the form through a queued hook, so wait for the site table.
    await expect.poll(() => (db().prepare("SELECT COUNT(*) AS n FROM forms WHERE slug = ?").get(formSlug) as any).n, { timeout: 30_000 }).toBe(1);

    // Staff browsing adds nothing.
    const base = views();
    const staffBeacons = trackBeacons(page);
    await page.goto("/");
    await page.goto(pagePath);
    await expect(page.locator(`form[action="/forms/${formSlug}"]`)).toBeVisible();
    expect(await page.locator('script[src="/_a/s.js"]').count()).toBe(0);
    await page.waitForTimeout(4_000);
    expect(staffBeacons.count()).toBe(0);
    expect(views()).toBe(base);

    // Logged-out visitor: three pages, one with a campaign, one from another site.
    const { context, page: visit } = await visitor(browser);
    const beacons = trackBeacons(visit);
    await visit.goto("/?utm_source=nl", { referer: "https://news.example.org/post" });
    await expect(visit.locator('script[src="/_a/s.js"]')).toHaveCount(1);
    await visit.goto(pagePath);
    await visit.locator("a[href]").first().waitFor({ state: "attached" });
    await visit.goto("/");
    await expect.poll(() => beacons.count()).toBe(3);

    // A hash-only change is not a new page view.
    await visit.goto(pagePath);
    await expect.poll(() => beacons.count()).toBe(4);
    await visit.evaluate(() => {
      location.hash = "#x";
    });
    await visit.evaluate(() => {
      location.hash = "#y";
    });
    await visit.waitForTimeout(1_000);
    expect(beacons.count()).toBe(4);
    await context.close();

    await expect.poll(() => views(), { timeout: 20_000 }).toBe(base + 4);

    // Do Not Track: the script sends nothing.
    const dnt = await visitor(browser, "Object.defineProperty(navigator, 'doNotTrack', { get: () => '1' });");
    const dntBeacons = trackBeacons(dnt.page);
    await dnt.page.goto("/");
    await expect(dnt.page.locator('script[src="/_a/s.js"]')).toHaveCount(1);
    await dnt.page.waitForTimeout(2_500);
    expect(dntBeacons.count()).toBe(0);
    await dnt.context.close();
    await new Promise((ok) => setTimeout(ok, 3_000));
    expect(views()).toBe(base + 4);
  });

  test("dashboard shows visitors, views, pages, campaign and source; filters live in the URL", async ({ page }) => {
    await page.goto("/admin/analytics?period=7d");
    await expect(page.locator("html")).toHaveAttribute("lang", "en");
    await expect.poll(async () => { await page.reload(); return num(page, "visitors"); }, { timeout: 20_000 }).toBeGreaterThanOrEqual(1);
    expect(await num(page, "views")).toBeGreaterThanOrEqual(4);
    await expect(page.locator("#t-page")).toContainText(pagePath);
    await expect(page.locator("#t-utm")).toContainText("nl");
    await expect(page.locator("#t-ref")).toContainText("news.example.org");
    await expect(page.locator("svg[role=img]").first()).toBeVisible();

    // Clicking a row filters the dashboard; the chip's remove link clears it.
    await page.locator("#t-page a", { hasText: pagePath }).click();
    await expect(page).toHaveURL(/[?&]page=/);
    await expect(page.locator('[data-chip="page"]')).toBeVisible();
    await page.locator('[data-remove="page"]').click();
    await expect(page).not.toHaveURL(/page=/);
    await expect(page.locator('[data-chip="page"]')).toHaveCount(0);

    // Changing the period keeps the URL in step.
    await page.goto("/admin/analytics?period=30d");
    await expect(page).toHaveURL(/period=30d/);

    // CSV honours the table.
    const csv = await page.request.get("/admin/analytics/export.csv?table=page&period=7d");
    expect(csv.status()).toBe(200);
    expect(csv.headers()["content-type"]).toContain("text/csv");
    expect(await csv.text()).toContain(pagePath);
  });

  test("a form submit shows in Forms", async ({ page, browser }) => {
    const { context, page: visit } = await visitor(browser);
    await visit.goto(pagePath);
    const form = visit.locator(`form[action="/forms/${formSlug}"]`);
    await form.locator(`#sf-${formSlug}-name`).fill("Sam");
    const posted = visit.waitForResponse((r) => r.request().method() === "POST" && new URL(r.url()).pathname === `/forms/${formSlug}`);
    await form.locator('button[type="submit"]').click();
    expect([200, 303]).toContain((await posted).status());
    await expect(visit.locator("body")).toContainText("Thanks, analytics");
    await context.close();

    await expect
      .poll(() => {
        const d = analyticsDb();
        try {
          return (d.prepare("SELECT COUNT(*) AS n FROM events WHERE kind = 'submit' AND form = ?").get(formSlug) as any).n;
        } finally {
          d.close();
        }
      }, { timeout: 30_000 })
      .toBe(1);

    await page.goto("/admin/analytics?period=7d");
    const forms = page.locator("[data-forms]");
    await expect(forms).toContainText(formSlug);
    await expect(forms.locator(`a[href="/admin/forms/${formSlug}"]`)).toBeVisible();
  });

  test("the Arabic dashboard is right to left and draws the chart", async ({ page }) => {
    await page.goto("/admin/analytics");
    await setLocale(page, "ar");
    try {
      await expect(page.locator("html")).toHaveAttribute("dir", "rtl");
      await expect(page.locator("svg[role=img]").first()).toBeVisible();
      await expect(page.locator("table[data-series]")).toHaveCount(1);
    } finally {
      await setLocale(page, "en");
    }
  });
});
