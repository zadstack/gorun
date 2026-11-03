import { expect, type Browser, type BrowserContext, type Page, test } from "@playwright/test";
import { db, setContentLocale, setLocale } from "../lib/helpers.ts";

// Site forms end to end. Two ways in: a builder Form section (published, then
// filled in by a logged-out visitor in each locale) and capture (a plain POST
// to /forms/{slug} with a real CSRF token, which creates the form on first use).
//
// POST /forms/* shares one per-IP limiter (5 a minute), so every visitor POST
// goes through post(), which waits out a 429 instead of failing.

const stamp = Date.now().toString(36);
const saved = /حُفظت|Draft saved/;

async function visitor(browser: Browser, locale: "ar" | "en"): Promise<{ context: BrowserContext; page: Page }> {
  const context = await browser.newContext({ storageState: { cookies: [], origins: [] }, locale });
  return { context, page: await context.newPage() };
}

// buildFormPage creates a published builder page holding one Form section.
async function buildFormPage(page: Page, opts: { pageSlug: string; formSlug: string; title: string; contentLocale: "ar" | "en"; success: string }) {
  await page.goto("/admin/content/page/new");
  await setContentLocale(page, opts.contentLocale);
  await page.fill("#title", `E2E ${opts.pageSlug}`);
  await page.fill("#slug", opts.pageSlug);
  await page.locator('button[name="intent"][value="save"]').click();
  await expect(page).toHaveURL(/\/admin\/content\/page\/[0-9A-Z]{26}$/);
  await page.goto(`/admin/builder/${page.url().split("/").pop()}`);

  await page.locator("#builder-kind").selectOption("form");
  await page.locator('[data-action="add"]').click();
  const form = page.locator("#builder-sections > section").last();
  await form.locator('input[data-field="title"]').fill(opts.title);
  await form.locator('input[data-form="slug"]').fill(opts.formSlug);
  await form.locator('textarea[data-form="success"]').fill(opts.success);

  const fields = form.locator("fieldset[data-form-field]");
  while (await fields.count()) await fields.first().getByRole("button").filter({ hasText: /Remove field|حذف الحقل/ }).click();
  const add = async (label: string, name: string, type: string, required: boolean) => {
    await form.getByRole("button", { name: /Add field|إضافة حقل/ }).click();
    const field = fields.last();
    await field.locator('input[data-form="label"]').fill(label);
    await field.locator('input[data-form="name"]').fill(name);
    await field.locator('select[data-form="type"]').selectOption(type);
    if (required) await field.locator('input[data-form="required"]').check();
    return field;
  };
  await add("Name", "name", "text", true);
  await add("Email", "email", "email", true);
  const plan = await add("Plan", "plan", "choice", false);
  await plan.locator('textarea[data-form="choices"]').fill("basic\npro");
  await add("Notes", "notes", "textarea", false);

  await page.locator('[data-action="save"]').click();
  await expect(page.locator("#builder-status")).toContainText(saved);
  await expect(page.locator('[data-action="publish"]')).toBeEnabled();
  page.once("dialog", (dialog) => dialog.accept());
  await page.locator('[data-action="publish"]').click();
  await expect(page.locator("#builder-status")).toContainText(/Page published|نُشرت/);
}

// post clicks a submit control (or runs a request) and retries after the
// shared rate-limit window when the server answers 429.
// prepare opens the page and fills the form; it runs again after a 429 so no field depends on history restore.
async function submitUI(page: Page, formSlug: string, prepare: () => Promise<void>, click: () => Promise<void>) {
  for (let attempt = 0; attempt < 3; attempt++) {
    if (attempt > 0) await prepare();
    const posted = page.waitForResponse((r) => r.request().method() === "POST" && new URL(r.url()).pathname === `/forms/${formSlug}`);
    await click();
    const response = await posted;
    if (response.status() !== 429) return response;
    await page.waitForTimeout(61_000);
  }
  throw new Error("rate limited three times");
}

async function capturePost(page: Page, slug: string, fields: Record<string, string>): Promise<{ status: number; body: string }> {
  for (let attempt = 0; attempt < 3; attempt++) {
    const result = await page.evaluate(
      async ({ slug, fields }) => {
        const token = document.querySelector<HTMLInputElement>('input[name="_csrf"]')?.value ?? "";
        const response = await fetch(`/forms/${slug}`, {
          method: "POST",
          credentials: "same-origin",
          headers: { "Content-Type": "application/x-www-form-urlencoded" },
          body: new URLSearchParams({ _csrf: token, ...fields }),
        });
        return { status: response.status, body: await response.text() };
      },
      { slug, fields },
    );
    if (result.status !== 429) return result;
    await page.waitForTimeout(61_000);
  }
  throw new Error("rate limited three times");
}

// entries are the rows of the entries table (rows with a date link; the field-settings table also has rows).
const entries = (page: Page) => page.locator("tbody tr:has(time)");

const formSlugAr = `e2e-ar-${stamp}`;
const formSlugEn = `e2e-en-${stamp}`;
const captured = `e2e-quote-${stamp}`;
const pageAr = `e2e-forms-ar-${stamp}`;
const pageEn = `e2e-forms-en-${stamp}`;

test.describe.serial("site forms", () => {
  test.setTimeout(240_000);

  test("builder form: published page, visitor submits in ar and en, admin table and CSV", async ({ page, browser }) => {
    await page.goto("/admin");
    await setLocale(page, "en");
    await buildFormPage(page, { pageSlug: pageAr, formSlug: formSlugAr, title: "طلب موعد", contentLocale: "ar", success: "وصلنا طلبك" });
    await buildFormPage(page, { pageSlug: pageEn, formSlug: formSlugEn, title: "Book a call", contentLocale: "en", success: "We got your request" });

    // Publishing announces the form through a queued hook, so wait for the site tables.
    await expect
      .poll(() => (db().prepare("SELECT slug, source, open FROM forms WHERE slug IN (?, ?) ORDER BY slug").all(formSlugAr, formSlugEn) as any[]).map((r) => [r.source, r.open]), { timeout: 30_000 })
      .toEqual([["builder", 1], ["builder", 1]]);

    // Arabic visitor, logged out.
    {
      const { context, page: visit } = await visitor(browser, "ar");
      await visit.goto(`/${pageAr}`);
      const form = visit.locator(`form[action="/forms/${formSlugAr}"]`);
      await expect(form).toBeVisible();
      await expect(form.locator(".sf-field")).toHaveCount(4);
      await expect(visit.locator("html")).toHaveAttribute("dir", "rtl");

      // Native validation blocks an empty required field and focuses the first invalid control.
      let posts = 0;
      visit.on("request", (r) => {
        if (r.method() === "POST" && r.url().includes("/forms/")) posts++;
      });
      await form.locator('button[type="submit"]').click();
      await expect(form.locator(`#sf-${formSlugAr}-name`)).toBeFocused();
      expect(await form.locator(`#sf-${formSlugAr}-name`).evaluate((el: HTMLInputElement) => el.validity.valueMissing)).toBe(true);
      await form.locator(`#sf-${formSlugAr}-name`).fill("منى");
      await form.locator('button[type="submit"]').click();
      await expect(form.locator(`#sf-${formSlugAr}-email`)).toBeFocused();
      await form.locator(`#sf-${formSlugAr}-email`).fill("not-an-email");
      await form.locator('button[type="submit"]').click();
      await expect(form.locator(`#sf-${formSlugAr}-email`)).toBeFocused();
      expect(posts).toBe(0);

      const fillAr = async () => {
        await form.locator(`#sf-${formSlugAr}-name`).fill("منى");
        await form.locator(`#sf-${formSlugAr}-email`).fill("mona@e2e.test");
        await form.locator(`#sf-${formSlugAr}-plan`).selectOption("pro");
        await form.locator(`#sf-${formSlugAr}-notes`).fill("ملاحظة, بفاصلة");
      };
      await fillAr();
      const response = await submitUI(visit, formSlugAr, async () => { await visit.goto(`/${pageAr}`); await fillAr(); }, () => form.locator('button[type="submit"]').click());
      expect([200, 303]).toContain(response.status());
      await visit.waitForLoadState("load");
      await expect(visit.locator("body")).toContainText("وصلنا طلبك");
      await context.close();
    }

    // English visitor, logged out.
    {
      const { context, page: visit } = await visitor(browser, "en");
      await visit.goto(`/en/${pageEn}`); // English content lives under /en/ (the site locale is Arabic)
      const form = visit.locator(`form[action="/forms/${formSlugEn}"]`);
      await expect(form).toBeVisible();
      await expect(visit.locator("html")).toHaveAttribute("dir", "ltr");
      await expect(form.locator('button[type="submit"]')).toHaveText(/Send/);
      const fillEn = async () => {
        await form.locator(`#sf-${formSlugEn}-name`).fill("Sam");
        await form.locator(`#sf-${formSlugEn}-email`).fill("sam@e2e.test");
        await form.locator(`#sf-${formSlugEn}-plan`).selectOption("basic");
      };
      await fillEn();
      const response = await submitUI(visit, formSlugEn, async () => { await visit.goto(`/en/${pageEn}`); await fillEn(); }, () => form.locator('button[type="submit"]').click());
      expect([200, 303]).toContain(response.status());
      await visit.waitForLoadState("load");
      await expect(visit.locator("body")).toContainText("We got your request");
      await context.close();
    }

    // The entries show up in the admin table.
    await page.goto(`/admin/forms/${formSlugAr}`);
    await expect(entries(page)).toHaveCount(1);
    await expect(page.locator("tbody:has(time)")).toContainText("منى");
    await expect(page.locator("tbody:has(time)")).toContainText("mona@e2e.test");
    await page.goto(`/admin/forms/${formSlugEn}`);
    await expect(entries(page)).toHaveCount(1);
    await expect(page.locator("tbody:has(time)")).toContainText("Sam");

    // The list shows both with a new count.
    await page.goto("/admin/forms");
    await expect(page.locator(`a[href="/admin/forms/${formSlugEn}"]`).first()).toBeVisible();

    // CSV: field headers and values.
    const csv = await page.request.get(`/admin/forms/${formSlugAr}/export.csv`);
    expect(csv.status()).toBe(200);
    expect(csv.headers()["content-type"]).toContain("text/csv");
    const text = await csv.text();
    const [head, ...lines] = text.replace(/^﻿/, "").trim().split(/\r?\n/);
    expect(head.split(",")).toEqual(["id", "created_at", "status", "page", "name", "email", "plan", "notes"]);
    expect(lines).toHaveLength(1);
    expect(lines[0]).toContain("منى");
    expect(lines[0]).toContain("mona@e2e.test");
    expect(lines[0]).toContain("pro");
    expect(lines[0]).toContain('"ملاحظة, بفاصلة"');
  });

  test("capture: a plain POST with a CSRF token creates a form with discovered fields; pinning works", async ({ page, browser }) => {
    // Content sanitizing strips <form>, so a plain HTML form cannot be authored in page content.
    // Instead the first capture submit is a real browser POST to /forms/{slug} that carries the
    // CSRF token read from a public builder form (the same token {{csrf}} would put in a theme form).
    const { context, page: visit } = await visitor(browser, "en");
    await visit.goto(`/en/${pageEn}`);
    await expect(visit.locator('input[name="_csrf"]').first()).toBeAttached();

    expect(db().prepare("SELECT count(*) AS n FROM forms WHERE slug = ?").get(captured)).toMatchObject({ n: 0 });
    // A tokenless POST is refused and creates nothing.
    const noToken = await visit.request.post(`/forms/${captured}`, { form: { name: "A", budget: "500" } });
    expect(noToken.status()).toBe(403);
    expect(db().prepare("SELECT count(*) AS n FROM forms WHERE slug = ?").get(captured)).toMatchObject({ n: 0 });

    // A field name outside [a-z0-9_] rejects the whole submission and creates no form.
    const rejected = await capturePost(visit, captured, { name: "Alice", Bad_Field: "x" });
    expect(rejected.status).toBe(422);
    expect(db().prepare("SELECT count(*) AS n FROM forms WHERE slug = ?").get(captured)).toMatchObject({ n: 0 });

    const first = await capturePost(visit, captured, { name: "Alice", budget: "500" });
    expect(first.status).toBeLessThan(400);
    const row = db().prepare("SELECT source, open, fields_json FROM forms WHERE slug = ?").get(captured) as any;
    expect(row.source).toBe("captured");
    const fields = JSON.parse(row.fields_json) as { name: string; state: string }[];
    expect(fields.map((f) => f.name)).toEqual(expect.arrayContaining(["name", "budget"]));
    expect(fields.every((f) => f.state === "discovered")).toBe(true);
    await context.close();

    await page.goto(`/admin/forms/${captured}`);
    await expect(entries(page)).toHaveCount(1);
    await expect(page.locator('th[data-field="budget"]')).toBeVisible();
    await expect(page.locator("tbody:has(time)")).toContainText("Alice");

    // Pin "budget" as a required number.
    await page.locator("details summary").first().click();
    const pin = page.locator(`form[action="/admin/forms/${captured}/fields/budget/pin"]`);
    await pin.locator('select[name="type"]').selectOption("number");
    await pin.locator('input[name="label"]').fill("Budget");
    await pin.locator('input[name="required"]').check();
    await pin.locator('button[type="submit"]').click();
    await expect(page.locator('th[data-field="budget"]')).toHaveText("Budget");
    const pinned = JSON.parse((db().prepare("SELECT fields_json FROM forms WHERE slug = ?").get(captured) as any).fields_json) as any[];
    expect(pinned.find((f) => f.name === "budget")).toMatchObject({ state: "pinned", type: "number", required: true });

    // After pinning, a non-numeric budget is rejected with 422 and stores nothing.
    const { context: again, page: visit2 } = await visitor(browser, "en");
    await visit2.goto(`/en/${pageEn}`);
    const bad = await capturePost(visit2, captured, { name: "Bob", budget: "lots" });
    expect(bad.status).toBe(422);
    await again.close();
    await page.goto(`/admin/forms/${captured}`);
    await expect(entries(page)).toHaveCount(1);
  });

  test("admin table: status filter and search", async ({ page }) => {
    await page.goto("/admin");
    await setLocale(page, "en");
    const entry = (await db().prepare("SELECT id FROM form_entries WHERE form_id = (SELECT id FROM forms WHERE slug = ?)").get(formSlugEn)) as any;
    expect(entry.id).toBeTruthy();

    // Opening the entry marks it read.
    await page.goto(`/admin/forms/${formSlugEn}/entries/${entry.id}`);
    await page.goto(`/admin/forms/${formSlugEn}?status=new`);
    await expect(entries(page)).toHaveCount(0);
    await page.goto(`/admin/forms/${formSlugEn}?status=read`);
    await expect(entries(page)).toHaveCount(1);

    // The filter form drives the same query.
    await page.goto(`/admin/forms/${formSlugAr}`);
    await page.locator("#f-q").fill("mona@e2e");
    await page.locator("#f-q").press("Enter");
    await expect(page).toHaveURL(/q=mona%40e2e/);
    await expect(entries(page)).toHaveCount(1);
    await page.locator("#f-q").fill("nobody-matches-this");
    await page.locator("#f-q").press("Enter");
    await expect(entries(page)).toHaveCount(0);
    await page.goto(`/admin/forms/${formSlugAr}?status=spam`);
    await expect(entries(page)).toHaveCount(0);
    // The CSV honours the same filter: header only.
    const filtered = await page.request.get(`/admin/forms/${formSlugAr}/export.csv?q=nobody-matches-this`);
    expect((await filtered.text()).trim().split(/\r?\n/)).toHaveLength(1);
    // A future date range excludes today's entries.
    await page.goto(`/admin/forms/${formSlugAr}?from=2999-01-01`);
    await expect(entries(page)).toHaveCount(0);
  });
});
