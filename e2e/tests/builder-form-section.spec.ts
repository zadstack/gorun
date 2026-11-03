import { expect, type Page, test } from "@playwright/test";
import { setLocale } from "../lib/helpers.ts";

// newBuilderPage creates a draft page and opens its builder.
async function newBuilderPage(page: Page, slug: string): Promise<string> {
  await page.goto("/admin/content/page/new");
  await page.fill("#title", `E2E ${slug}`);
  await page.fill("#slug", slug);
  await page.locator('button[name="intent"][value="save"]').click();
  await expect(page).toHaveURL(/\/admin\/content\/page\/[0-9A-Z]{26}$/);
  const id = page.url().split("/").pop()!;
  await page.goto(`/admin/builder/${id}`);
  return id;
}

async function addForm(page: Page) {
  await page.locator("#builder-kind").selectOption("form");
  await page.locator('[data-action="add"]').click();
  return page.locator("#builder-sections > section").last();
}

const saved = /حُفظت|Draft saved/;

test("form section: slug from title, field add/reorder/remove, persisted, disabled preview", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.goto("/admin");
  await setLocale(page, "en");
  await newBuilderPage(page, "e2e-builder-form");
  const form = await addForm(page);
  const slug = form.locator('input[data-form="slug"]');
  await expect(slug).toHaveValue("form");
  await form.locator('input[data-field="title"]').fill("Book a call");
  await expect(slug).toHaveValue("book-a-call");
  // The slug stays editable, and once edited it no longer follows the title.
  await slug.fill("book-a-call-now");
  await form.locator('input[data-field="title"]').fill("Book a call today");
  await expect(slug).toHaveValue("book-a-call-now");
  await slug.fill("book-a-call");

  // Start from no fields, then add name, email and plan.
  const fields = form.locator("fieldset[data-form-field]");
  await expect(fields).toHaveCount(3);
  while (await fields.count()) {
    await fields.first().getByRole("button", { name: "Remove field" }).click();
  }
  await expect(form.locator('[data-form-hint="fields"]')).toBeVisible();
  await expect(page.locator('[data-action="save"]')).toBeDisabled();
  const addField = form.getByRole("button", { name: "Add field" });
  const add = async (label: string, type: string, required: boolean) => {
    await addField.click();
    const field = fields.last();
    await field.locator('input[data-form="label"]').fill(label);
    await field.locator('select[data-form="type"]').selectOption(type);
    if (required) await fields.last().locator('input[data-form="required"]').check();
    return fields.last();
  };
  await add("Name", "text", true);
  await expect(fields.last().locator('input[data-form="name"]')).toHaveValue("name");
  await add("Email", "email", true);
  const plan = await add("Plan", "choice", false);
  await expect(plan.locator('[data-form-hint="choices"]')).toBeVisible();
  await plan.locator('textarea[data-form="choices"]').fill("basic\npro");
  await expect(fields.last().locator('[data-form-hint="choices"]')).toBeHidden();
  // A file field is offered once per form.
  await add("Temporary note", "file", false);
  await expect(fields.first().locator('select[data-form="type"] option[value="file"]')).toHaveJSProperty("disabled", true);
  await expect(fields.last().locator('input[data-form="name"]')).toHaveValue("temporary_note");

  const names = () => fields.locator('input[data-form="name"]').evaluateAll((nodes) => nodes.map((n) => (n as HTMLInputElement).value));
  expect(await names()).toEqual(["name", "email", "plan", "temporary_note"]);
  await fields.nth(2).getByRole("button", { name: "Move field down" }).click();
  expect(await names()).toEqual(["name", "email", "temporary_note", "plan"]);
  // Focus stays with the moved field.
  await expect(fields.nth(3).getByRole("button", { name: "Move field up" })).toBeFocused();
  await page.keyboard.press("Enter");
  expect(await names()).toEqual(["name", "email", "plan", "temporary_note"]);
  await expect(fields.first().getByRole("button", { name: "Move field up" })).toBeDisabled();
  await expect(fields.last().getByRole("button", { name: "Move field down" })).toBeDisabled();
  await fields.nth(3).getByRole("button", { name: "Remove field" }).click();
  expect(await names()).toEqual(["name", "email", "plan"]);
  await expect(form).toBeFocused();

  await expect(page.locator('[data-action="save"]')).toBeEnabled();
  await page.locator('[data-action="save"]').click();
  await expect(page.locator("#builder-status")).toContainText(saved);
  await page.reload();
  const reloaded = page.locator("#builder-sections > section").last();
  await expect(reloaded.locator('input[data-form="slug"]')).toHaveValue("book-a-call");
  const after = reloaded.locator("fieldset[data-form-field]");
  expect(await after.locator('input[data-form="name"]').evaluateAll((nodes) => nodes.map((n) => (n as HTMLInputElement).value))).toEqual(["name", "email", "plan"]);
  await expect(after.nth(0).locator('input[data-form="required"]')).toBeChecked();
  await expect(after.nth(1).locator('select[data-form="type"]')).toHaveValue("email");
  await expect(after.nth(2).locator('select[data-form="type"]')).toHaveValue("choice");
  await expect(after.nth(2).locator('textarea[data-form="choices"]')).toHaveValue("basic\npro");
  await expect(after.nth(2).locator('input[data-form="required"]')).not.toBeChecked();

  // The draft-only form previews with its fields, every control disabled.
  const preview = page.frameLocator("#builder-preview");
  await expect(preview.locator(".gp-form .sf-field")).toHaveCount(3);
  await expect(preview.locator(".gp-form select option")).toHaveText(["Choose…", "basic", "pro"]);
  await expect(preview.locator(".gp-form label").first()).toHaveText("Name");
  for (const control of await preview.locator(".gp-form input, .gp-form select, .gp-form textarea, .gp-form button").all()) {
    await expect(control).toBeDisabled();
  }
  expect(errors).toEqual([]);
});

test("form section: an Arabic title leaves the slug empty and blocks saving until one is set", async ({ page }) => {
  await page.goto("/admin");
  await setLocale(page, "ar");
  await newBuilderPage(page, "e2e-builder-form-ar");
  const form = await addForm(page);
  const slug = form.locator('input[data-form="slug"]');
  await form.locator('input[data-field="title"]').fill("احجز موعدًا");
  await expect(slug).toHaveValue("");
  const hint = form.locator('[data-form-hint="slug"]');
  await expect(hint).toBeVisible();
  await expect(hint).toContainText("booking");
  await expect(slug).toHaveAttribute("aria-invalid", "true");
  await expect(page.locator('[data-action="save"]')).toBeDisabled();
  await expect(page.locator("#builder-status")).toContainText("صحّح إعدادات النموذج");
  // An Arabic field label gives an empty name with the same kind of hint.
  await form.getByRole("button", { name: "إضافة حقل" }).click();
  const field = form.locator("fieldset[data-form-field]").last();
  await field.locator('input[data-form="label"]').fill("رقم الهاتف");
  await expect(field.locator('input[data-form="name"]')).toHaveValue("");
  await expect(field.locator('[data-form-hint="name"]')).toBeVisible();
  await field.locator('input[data-form="name"]').fill("phone");
  await expect(field.locator('[data-form-hint="name"]')).toBeHidden();
  await expect(page.locator('[data-action="save"]')).toBeDisabled();
  await slug.fill("booking");
  await expect(hint).toBeHidden();
  await expect(page.locator('[data-action="save"]')).toBeEnabled();
  await page.locator('[data-action="save"]').click();
  await expect(page.locator("#builder-status")).toContainText(saved);
  await page.reload();
  await expect(page.locator('#builder-sections input[data-form="slug"]')).toHaveValue("booking");
});

test("form section: publishing a slug another page uses names the slug", async ({ page }) => {
  await page.goto("/admin");
  await setLocale(page, "en");
  for (const [index, path] of ["e2e-form-taken-a", "e2e-form-taken-b"].entries()) {
    await newBuilderPage(page, path);
    const form = await addForm(page);
    await form.locator('input[data-field="title"]').fill("Taken form");
    await expect(form.locator('input[data-form="slug"]')).toHaveValue("taken-form");
    page.once("dialog", (dialog) => dialog.accept());
    await page.locator('[data-action="publish"]').click();
    if (index === 0) {
      await expect(page.locator("#builder-status")).toContainText("Page published");
    } else {
      await expect(page.locator("#builder-status")).toContainText("“taken-form”");
      await expect(page.locator("#builder-status")).toHaveAttribute("role", "alert");
      await expect(form.locator('input[data-form="slug"]')).toBeFocused();
    }
  }
});

test.describe("coarse pointer", () => {
  test.use({ hasTouch: true, isMobile: true });
  for (const locale of ["ar", "en"] as const) {
    test(`form section controls are labelled and buttons are 44px · ${locale}`, async ({ page }) => {
      await page.goto("/admin");
      await setLocale(page, locale);
      expect(await page.evaluate(() => matchMedia("(pointer:coarse)").matches)).toBe(true);
      await newBuilderPage(page, `e2e-builder-form-touch-${locale}`);
      const form = await addForm(page);
      await form.locator('fieldset[data-form-field] select[data-form="type"]').first().selectOption("choice");
      const editor = form.locator("fieldset.builder-form");
      const unlabelled = await editor.locator("input, select, textarea").evaluateAll((nodes) =>
        nodes.filter((n) => {
          const control = n as HTMLInputElement;
          return !(control.labels && [...control.labels].some((l) => l.textContent!.trim())) && !control.getAttribute("aria-label");
        }).map((n) => n.outerHTML)
      );
      expect(unlabelled).toEqual([]);
      const buttons = await editor.locator("button").all();
      expect(buttons.length).toBeGreaterThan(5);
      for (const button of buttons) {
        const box = (await button.boundingBox())!;
        expect(box.height, await button.textContent() ?? "").toBeGreaterThanOrEqual(44);
        expect(box.width, await button.textContent() ?? "").toBeGreaterThanOrEqual(44);
      }
    });
  }
});
