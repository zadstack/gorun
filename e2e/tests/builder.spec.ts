import { readFileSync } from "node:fs";
import { expect, test } from "@playwright/test";
import { setLocale } from "../lib/helpers.ts";

test(
  "page builder keeps drafts private, supports history and previews, publishes explicitly, rejects stale edits",
  async ({ page, context }, testInfo) => {
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    await page.goto("/admin/content/page/new");
    await page.fill("#title", "E2E visual builder");
    await page.fill("#slug", "e2e-visual-builder");
    await page.locator('button[name="intent"][value="save"]').click();
    await expect(page).toHaveURL(/\/admin\/content\/page\/[0-9A-Z]{26}$/);
    const id = page.url().split("/").pop()!;
    await page.locator(`a[href="/admin/builder/${id}"]`).click();
    await expect(page.locator("#builder-kind option")).toHaveCount(12);
    await page.locator('[data-action="add"]').click();
    const sections = page.locator("#builder-sections > section");
    await expect(sections).toHaveCount(1);
    await sections.first().locator('input[data-field="title"]').fill(
      "Draft content unique to builder",
    );
    await page.locator('[data-action="save"]').click();
    await expect(page.locator("#builder-status")).toContainText(
      /حُفظت|Draft saved/,
    );
    const before = await page.request.get("/e2e-visual-builder");
    expect(await before.text()).not.toContain(
      "Draft content unique to builder",
    );
    await expect(page.frameLocator("#builder-preview").locator("h1,h2").first())
      .toContainText("Draft content unique to builder");
    await page.locator('[data-action="mobile"]').click();
    await expect(page.locator("#builder-preview")).toHaveCSS(
      "max-width",
      "390px",
    );
    await page.screenshot({
      path: testInfo.outputPath("builder-mobile-preview.png"),
      fullPage: true,
    });
    await sections.first().getByRole("button", { name: /نسخ القسم|Duplicate/ })
      .click();
    await expect(sections).toHaveCount(2);
    await page.locator('[data-action="undo"]').click();
    await expect(sections).toHaveCount(1);
    await page.locator('[data-action="redo"]').click();
    await expect(sections).toHaveCount(2);
    await sections.last().getByRole("button", { name: /نقل لأعلى|Move up/ })
      .click();
    await page.locator('[data-action="save"]').click();
    await expect(page.locator("#builder-status")).toContainText(
      /حُفظت|Draft saved/,
    );
    await page.locator("#builder-kind").selectOption("features");
    await page.locator('[data-action="add"]').click();
    const feature = sections.last();
    await feature.locator('input[data-field="title"][data-item="0"]').fill(
      "Hidden feature proof",
    );
    await feature.getByRole("button", { name: /إضافة عنصر|Add item/ }).click();
    await expect(feature.locator("fieldset")).toHaveCount(4);
    await feature.locator("fieldset").last().getByRole("button", {
      name: /حذف العنصر|Remove item/,
    }).click();
    await expect(feature.locator("fieldset")).toHaveCount(3);
    await feature.getByRole("checkbox", { name: /إخفاء القسم|Hide section/ })
      .check();
    page.once("dialog", (dialog) => dialog.accept());
    await page.locator('[data-action="publish"]').click();
    await expect(page.locator("#builder-status")).toContainText(
      /نُشرت|Page published/,
    );
    const published = await page.request.get("/e2e-visual-builder");
    expect(published.status()).toBe(200);
    const publicBody = await published.text();
    expect(publicBody).toContain("Draft content unique to builder");
    expect(publicBody).not.toContain("Hidden feature proof");
    // A second tab reads an old token and must not overwrite the first tab's draft.
    const stale = await context.newPage();
    await stale.goto(`/admin/builder/${id}`);
    await sections.first().locator('input[data-field="title"]').fill(
      "Latest draft from first tab",
    );
    await page.locator('[data-action="save"]').click();
    await expect(page.locator("#builder-status")).toContainText(
      /حُفظت|Draft saved/,
    );
    await stale.locator('#builder-sections input[data-field="title"]').first()
      .fill("Stale second tab edit");
    await stale.locator('[data-action="save"]').click();
    await expect(stale.locator("#builder-status")).toContainText(
      /تغيّرت|changed elsewhere/,
    );
    await stale.close();
    page.once("dialog", (dialog) => dialog.accept());
    await page.locator('[data-action="reset"]').click();
    await expect(sections).toHaveCount(3);
    await expect(sections.first().locator('input[data-field="title"]'))
      .toHaveValue("Draft content unique to builder");
    await expect(page.locator("#builder-convert")).toHaveCount(0);
    await expect(page.locator('[data-action="publish"]')).toBeEnabled();
    const afterDiscard = await page.request.get("/e2e-visual-builder");
    expect(await afterDiscard.text()).toContain(
      "Draft content unique to builder",
    );
    await setLocale(page, "en");
    await expect(page.locator("html")).toHaveAttribute("dir", "ltr");
    await expect(page.locator("h1")).toContainText("Page builder");
    await setLocale(page, "ar");
    await page.setViewportSize({ width: 390, height: 844 });
    await expect(page.locator("html")).toHaveAttribute("dir", "rtl");
    expect(
      await page.evaluate(() =>
        document.documentElement.scrollWidth <= window.innerWidth
      ),
    ).toBe(true);
    await page.screenshot({
      path: testInfo.outputPath("builder-arabic-phone.png"),
      fullPage: true,
    });
    expect(errors).toEqual([]);
  },
);

test("autosave preserves edits made while an earlier save is in flight", async ({ page }) => {
  await page.goto("/admin/content/page/new");
  await page.fill("#title", "E2E builder in-flight save");
  await page.fill("#slug", "e2e-builder-save-race");
  await page.locator('button[name="intent"][value="save"]').click();
  const id = page.url().split("/").pop()!;
  await page.goto(`/admin/builder/${id}`);
  await page.locator('[data-action="add"]').click();
  const title = page.locator('#builder-sections input[data-field="title"]')
    .first();
  await title.fill("Earlier save snapshot");
  let release!: () => void;
  let captured!: () => void;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  const arrived = new Promise<void>((resolve) => {
    captured = resolve;
  });
  let first = true;
  await page.route(`**/admin/builder/${id}/save`, async (route) => {
    if (first) {
      first = false;
      captured();
      await gate;
    }
    await route.continue();
  });
  await page.locator('[data-action="save"]').click();
  await arrived;
  await title.fill("Newest edit while saving");
  release();
  await expect(page.locator("#builder-status")).toContainText(
    /حُفظت|Draft saved/,
  );
  await page.reload();
  await expect(
    page.locator('#builder-sections input[data-field="title"]').first(),
  ).toHaveValue("Newest edit while saving");
  await page.locator("#builder-kind").selectOption("text");
  await page.locator('[data-action="add"]').click();
  await page.locator('select[data-field="feed"]').selectOption("posts");
  await page.locator('[data-action="save"]').click();
  await expect(page.locator("#builder-status")).toContainText(
    /حُفظت|Draft saved/,
  );
  await page.reload();
  await expect(page.locator('select[data-field="feed"]')).toHaveValue("posts");
  await expect(page.frameLocator("#builder-preview").locator(".gp-feed-posts"))
    .toHaveCount(1);
});

test("legacy page content requires explicit acknowledgement before builder publishing", async ({ page }) => {
  await page.goto("/admin/content/page/new");
  await page.fill("#title", "E2E legacy body acknowledgement");
  await page.locator('[data-field-key="body"] .ql-editor').fill(
    "Existing legacy body must remain intact.",
  );
  await page.locator('button[name="intent"][value="save"]').click();
  const id = page.url().split("/").pop()!;
  await page.goto(`/admin/builder/${id}`);
  await page.locator('[data-action="add"]').click();
  await expect(page.locator("#builder-convert")).toBeVisible();
  await expect(page.locator('[data-action="publish"]')).toBeDisabled();
  await page.locator("#builder-convert").check();
  await expect(page.locator('[data-action="publish"]')).toBeEnabled();
  await page.locator('[data-action="save"]').click();
  await expect(page.locator("#builder-status")).toContainText(
    /حُفظت|Draft saved/,
  );
  const existing = await page.request.get(`/admin/content/page/${id}`);
  expect(await existing.text()).toContain(
    "Existing legacy body must remain intact.",
  );
});

test("layout JSON export and validated import preserve private drafts", async ({ page }) => {
  await page.goto("/admin/content/page/new");
  await page.fill("#title", "E2E portable page");
  await page.fill("#slug", "e2e-portable-page");
  await page.locator('button[name="intent"][value="save"]').click();
  const id = page.url().split("/").pop()!;
  await page.goto(`/admin/builder/${id}`);
  await page.locator('[data-action="add"]').click();
  const title = page.locator('#builder-sections input[data-field="title"]')
    .first();
  await title.fill("Original portable section");
  await page.locator('[data-action="save"]').click();
  await expect(page.locator("#builder-status")).toContainText(
    /حُفظت|Draft saved/,
  );
  const pendingDownload = page.waitForEvent("download");
  await page.locator('[data-action="export"]').locator("..").locator("summary").click();
  await page.locator('[data-action="export"]').click();
  const downloaded = await pendingDownload;
  const portable = JSON.parse(readFileSync((await downloaded.path())!, "utf8"));
  expect(Object.keys(portable).sort()).toEqual(["sections", "version"]);
  expect(portable.version).toBe(1);
  expect(portable.sections[0].title).toBe("Original portable section");
  portable.sections[0].title = "Imported private layout";
  const upload = async (content: string) =>
    page.locator("#builder-import").setInputFiles({
      name: "layout.json",
      mimeType: "application/json",
      buffer: Buffer.from(content),
    });
  await upload(JSON.stringify(portable));
  await expect(title).toHaveValue("Imported private layout");
  await expect(page.locator("#builder-status")).toContainText(
    /حُفظت|Draft saved/,
  );
  const publicPage = await page.request.get("/e2e-portable-page");
  expect(await publicPage.text()).not.toContain("Imported private layout");
  for (
    const invalid of [
      "{malformed",
      JSON.stringify({ ...portable, unknown: true }),
      JSON.stringify({
        ...portable,
        sections: [{ ...portable.sections[0], link: "javascript:alert(1)" }],
      }),
      " ".repeat((1 << 20) + 1),
    ]
  ) {
    await upload(invalid);
    await expect(page.locator("#builder-status")).toContainText(
      /تعذر استيراد|Could not import/,
    );
    await expect(title).toHaveValue("Imported private layout");
  }
  await page.reload();
  await expect(
    page.locator('#builder-sections input[data-field="title"]').first(),
  ).toHaveValue("Imported private layout");
});

test("builder drag preserves section fields, undo, persisted order and keyboard fallback", async ({ page }) => {
  await page.goto("/admin/content/page/new");
  await page.fill("#title", "E2E reorder handles");
  await page.fill("#slug", "e2e-reorder-handles");
  await page.locator('button[name="intent"][value="save"]').click();
  const id = page.url().split("/").pop()!;
  await page.goto(`/admin/builder/${id}`);
  const sections = page.locator("#builder-sections > section");
  for (const name of ["First section", "Second section"]) {
    await page.locator('[data-action="add"]').click();
    await sections.last().locator('input[data-field="title"]').fill(name);
  }
  // Drag as a person does: both sections on screen (each is taller than the
  // default viewport), press the handle, move in steps (one jump never starts a
  // native drag) and drop inside the next section.
  await page.setViewportSize({ width: 1280, height: 2200 });
  const grip = sections.first().locator(".builder-drag");
  await grip.scrollIntoViewIfNeeded();
  const from = (await grip.boundingBox())!;
  const to = (await sections.last().boundingBox())!;
  await page.mouse.move(from.x + from.width / 2, from.y + from.height / 2);
  await page.mouse.down();
  await page.mouse.move(to.x + 24, to.y + to.height / 2, { steps: 12 });
  await page.mouse.up();
  await expect(sections.first().locator('input[data-field="title"]')).toHaveValue("Second section");
  await page.locator('[data-action="undo"]').click();
  await expect(sections.first().locator('input[data-field="title"]')).toHaveValue("First section");
  await page.locator('[data-action="redo"]').click();
  await page.locator('[data-action="save"]').click();
  await expect(page.locator("#builder-status")).toContainText(/حُفظت|Draft saved/);
  await page.reload();
  await expect(sections.first().locator('input[data-field="title"]')).toHaveValue("Second section");
  await expect(sections.last().locator('input[data-field="title"]')).toHaveValue("First section");
  const up = sections.last().getByRole("button", { name: /نقل لأعلى|Move up/ });
  await up.focus();
  await page.keyboard.press("Enter");
  await expect(sections.first().locator('input[data-field="title"]')).toHaveValue("First section");
});
