import { test, expect, type BrowserContext, type Page } from "@playwright/test";
import { randomUUID } from "node:crypto";
import { csrf, db, setLocale, submitAndLand } from "../lib/helpers.ts";
import { tinyPNG } from "../fixtures/wp-fixture-server.ts";

test.describe.configure({ mode: "serial" });
test.use({ actionTimeout: 5000 });
const themes = ["default", "business", "magazine", "portfolio", "docs"];
const kinds = [
  "hero",
  "features",
  "steps",
  "comparison",
  "faq",
  "product_preview",
];
let context: BrowserContext,
  fixture: Page,
  mediaID = "",
  mediaURL = "";
const pages = new Map<
  string,
  { id: string; slug: string; path: string; document?: any }
>();
let savedSettings: any[] = [],
  savedUser: any;
const settingsFilter = "key LIKE 'site.%' OR key LIKE 'theme.%'";
function captureSettings() {
  const d = db();
  try {
    return d
      .prepare(`SELECT * FROM settings WHERE ${settingsFilter} ORDER BY key`)
      .all();
  } finally {
    d.close();
  }
}
function restoreSettings() {
  const d = db();
  try {
    d.exec("BEGIN IMMEDIATE");
    d.prepare(`DELETE FROM settings WHERE ${settingsFilter}`).run();
    const q = d.prepare(
      "INSERT INTO settings(key,value_json,updated_at) VALUES(?,?,?)",
    );
    for (const row of savedSettings)
      q.run(row.key, row.value_json, row.updated_at);
    if (savedUser)
      d.prepare("UPDATE users SET locale=?,updated_at=? WHERE id=?").run(
        savedUser.locale,
        savedUser.updated_at,
        savedUser.id,
      );
    d.exec("COMMIT");
  } finally {
    d.close();
  }
}
async function activate(page: Page, theme: string) {
  await page.goto("/admin/settings/general");
  await page.selectOption("#s-theme", theme);
  await submitAndLand(
    page,
    page.locator('form[action="/admin/settings/general"] button[type=submit]'),
    "/admin/settings/general",
  );
  await expect(page.locator("#s-theme")).toHaveValue(theme);
}
async function saved(page: Page) {
  await page.locator("[data-action=save]").click();
  await expect(page.locator("#builder-status")).toContainText(
    /حُفظت|Draft saved/,
  );
}
async function noOverflow(page: Page) {
  const g = await page.evaluate(() => ({
    actual: document.documentElement.scrollWidth,
    width: innerWidth,
  }));
  expect(g.actual).toBeLessThanOrEqual(g.width + 1);
}

test.beforeAll(async ({ browser }) => {
  context = await browser.newContext({ storageState: ".auth/admin.json" });
  fixture = await context.newPage();
  await fixture.goto("/admin");
  savedSettings = captureSettings();
  const d = db();
  try {
    savedUser = d
      .prepare(
        "SELECT id,locale,updated_at FROM users WHERE email='admin@e2e.test'",
      )
      .get();
  } finally {
    d.close();
  }
  const headers = { "X-CSRF-Token": await csrf(fixture) };
  const upload = await fixture.request.post("/api/v1/media", {
    headers,
    multipart: {
      file: {
        name: `native-${randomUUID()}.png`,
        mimeType: "image/png",
        buffer: tinyPNG(320, 180, [25, 90, 130, 255]),
      },
    },
  });
  expect(upload.status()).toBe(201);
  const media = (await upload.json()).data;
  mediaID = media.id;
  mediaURL = media.url;
  for (const locale of ["ar", "en"]) {
    const slug = `native-builder-${locale}-${randomUUID().slice(0, 8)}`;
    const response = await fixture.request.post("/api/v1/content", {
      headers,
      data: {
        type: "page",
        locale,
        slug,
        title: `Native builder ${locale}`,
        status: "published",
        body: `<p>Legacy baseline ${locale}</p>`,
      },
    });
    expect(response.status()).toBe(201);
    const id = (await response.json()).data.id;
    pages.set(locale, {
      id,
      slug,
      path: locale === "ar" ? `/${slug}` : `/en/${slug}`,
    });
  }
});

test.afterAll(async () => {
  try {
    if (fixture) {
      await fixture.goto("/admin");
      const headers = { "X-CSRF-Token": await csrf(fixture) };
      try {
        for (const p of pages.values()) {
          const response = await fixture.request.delete(
            `/api/v1/content/${p.id}?hard=1`,
            { headers },
          );
          expect(response.ok()).toBeTruthy();
          expect(
            (await fixture.request.get(`/api/v1/content/${p.id}`)).status(),
          ).toBe(404);
        }
      } finally {
        if (mediaID) {
          const response = await fixture.request.delete(
            `/api/v1/media/${mediaID}`,
            { headers },
          );
          expect(response.ok()).toBeTruthy();
          expect(
            (await fixture.request.get(`/api/v1/media/${mediaID}`)).status(),
          ).toBe(404);
        }
      }
    }
  } finally {
    try {
      restoreSettings();
    } finally {
      await context?.close();
    }
  }
});

for (const locale of ["ar", "en"] as const)
  test(`native editor ${locale}: author six section types through controls then publish`, async ({
    page,
  }, info) => {
    const p = pages.get(locale)!;
    await activate(page, "default");
    await page.goto(`/admin/builder/${p.id}`);
    await setLocale(page, locale);
    await page.screenshot({
      path: info.outputPath(`editor-${locale}-before.png`),
      fullPage: true,
    });
    await expect(page.locator("#builder-kind option")).toHaveCount(15);
    const labels = JSON.parse(
      (await page.locator("#page-builder").getAttribute("data-state"))!,
    ).labels;
    for (const [index, kind] of kinds.entries()) {
      await page.selectOption("#builder-kind", kind);
      await page.locator("[data-action=add]").click();
      const section = page.locator("#builder-sections > section").nth(index);
      await expect(section).toBeVisible();
      if (["hero", "features", "faq"].includes(kind))
        await section
          .locator("select[data-field=library]")
          .selectOption("sections");
      await expect(section.locator("select[data-field=library]")).toHaveValue(
        "sections",
      );
      await section
        .locator("input[data-field=title]:not([data-item])")
        .fill(`Native ${locale} ${kind}`);
      await section
        .locator("select[data-field=background]")
        .selectOption("dots");
      if (kind === "hero") {
        await section
          .locator("input[data-field=image]:not([data-item])")
          .fill(mediaURL);
        await section
          .locator("input[data-field=image_alt]:not([data-item])")
          .fill("Synthetic native image");
        await section
          .locator("input[data-field=caption]")
          .fill("Synthetic image, not a product claim");
        await section
          .locator("input[data-field=link]:not([data-item])")
          .fill("#native-end");
        await section
          .locator("input[data-field=link_label]")
          .fill(locale === "ar" ? "ابدأ الآن" : "Start now");
        await section.locator("input[data-field=secondary_link]").fill("#main");
        await section
          .locator("input[data-field=secondary_label]")
          .fill(locale === "ar" ? "التفاصيل" : "Details");
      }
      if (kind === "hero") {
        await section
          .getByRole("button", { name: labels.add_fact, exact: true })
          .click();
        await section
          .locator('input[data-field=label][data-item="0"]')
          .fill(locale === "ar" ? "الثيمات" : "Themes");
        await section
          .locator('input[data-field=value][data-item="0"]')
          .fill("5");
      }
      if (kind === "comparison") {
        await section
          .getByRole("button", { name: labels.add_column, exact: true })
          .click();
        await expect(section.locator('input[data-field="3"]')).toHaveCount(1);
        await section
          .getByRole("button", { name: labels.remove_column, exact: true })
          .last()
          .click();
        await expect(section.locator('input[data-field="3"]')).toHaveCount(0);
        await section
          .getByRole("button", { name: labels.add_row, exact: true })
          .click();
        await section
          .locator("input[data-field=label]")
          .last()
          .fill(locale === "ar" ? "الخصوصية" : "Privacy");
      }
      if (kind === "faq") {
        await section
          .getByRole("button", { name: labels.add_item, exact: true })
          .click();
        await expect(
          section.locator("input[data-field=title][data-item]"),
        ).toHaveCount(4);
      }
      if (kind === "features") {
        await section
          .locator("select[data-field=icon]")
          .first()
          .selectOption("bolt");
        await section.locator("input[data-field=featured]").first().check();
      }
    }
    await saved(page);
    const before = await page.request.get(p.path);
    expect(before.status()).toBe(200);
    expect(await before.text()).toContain(`Legacy baseline ${locale}`);
    expect(await before.text()).not.toContain(`Native ${locale} hero`);
    await expect(
      page.frameLocator("#builder-preview").locator(".gp-s-hero"),
    ).toBeVisible();
    await expect(
      page.frameLocator("#builder-preview").locator(".gp-s-compare"),
    ).toBeVisible();
    await page.reload();
    const state = JSON.parse(
      (await page.locator("#page-builder").getAttribute("data-state")) ?? "{}",
    );
    p.document = state.draft.document;
    expect(p.document.sections.map((s: any) => s.kind)).toEqual(kinds);
    expect(
      p.document.sections.every((s: any) => s.library === "sections"),
    ).toBe(true);
    expect(p.document.sections[1].items[0].icon).toBe("bolt");
    expect(p.document.sections[1].items[0].featured).toBe(true);
    expect(p.document.sections[0].native.facts[0].value).toBe("5");
    expect(p.document.sections[3].native.columns).toHaveLength(3);
    expect(p.document.sections[3].native.rows).toHaveLength(2);
    expect(p.document.sections[4].items).toHaveLength(4);
    expect(p.document.sections[4].items[3].title.trim().length).toBeGreaterThan(
      0,
    );
    await page.locator("#builder-convert").check();
    page.once("dialog", (dialog) => dialog.accept());
    await page.locator("[data-action=publish]").click();
    await expect(page.locator("#builder-status")).toContainText(
      /نُشرت|Page published/,
    );
    const published = await page.request.get(p.path);
    expect(published.status()).toBe(200);
    expect(await published.text()).toContain(`Native ${locale} hero`);
    expect(await published.text()).not.toContain(`Legacy baseline ${locale}`);
    await page.setViewportSize({ width: 375, height: 900 });
    await noOverflow(page);
    await page.screenshot({
      path: info.outputPath(`editor-${locale}-phone.png`),
      fullPage: true,
    });
  });

for (const theme of themes)
  for (const locale of ["ar", "en"] as const)
    for (const width of [375, 1280])
      test(`${theme} ${locale} ${width}: native sections render and interact`, async ({
        page,
      }, info) => {
        const p = pages.get(locale)!;
        const errors: string[] = [];
        page.on("pageerror", (e) => errors.push(e.message));
        await activate(page, theme);
        await page.setViewportSize({ width, height: 900 });
        const response = await page.goto(p.path);
        expect(response?.status()).toBe(200);
        await page.screenshot({
          path: info.outputPath(`${theme}-${locale}-${width}-before.png`),
          fullPage: true,
        });
        await expect(page.locator("html")).toHaveAttribute("lang", locale);
        await expect(page.locator("html")).toHaveAttribute(
          "dir",
          locale === "ar" ? "rtl" : "ltr",
        );
        await noOverflow(page);
        expect(await page.content()).not.toContain("<!-- gorun:500 -->");
        for (const s of p.document.sections)
          await expect(page.locator(`#gp-${s.id}`)).toBeVisible();
        await expect(
          page.locator(".gp-s-card-featured .gp-s-icon-bolt"),
        ).toHaveCount(1);
        await expect(page.locator(".gp-s-steps > li")).toHaveCount(3);
        await expect(page.locator(".gp-s-compare thead th")).toHaveCount(3);
        await expect(page.locator(".gp-s-preview-ui")).toHaveCount(1);
        const image = page.locator(".gp-s-hero img");
        await expect
          .poll(() =>
            image.evaluate(
              (el: HTMLImageElement) => el.complete && el.naturalWidth,
            ),
          )
          .toBe(320);
        const comparison = page.locator(".gp-s-compare-wrap");
        await expect(comparison).toHaveAttribute("tabindex", "0");
        await expect(comparison).toHaveAttribute("role", "region");
        await expect(comparison).toHaveAttribute(
          "aria-label",
          `Native ${locale} comparison`,
        );
        await comparison.focus();
        await page.keyboard.press("Shift+Tab");
        await page.keyboard.press("Tab");
        await expect(comparison).toBeFocused();
        await page.keyboard.press("ArrowRight");
        for (const header of await page.locator(".gp-s-compare thead th").all())
          await expect(header).toHaveAttribute("scope", "col");
        for (const header of await page.locator(".gp-s-compare tbody th").all())
          await expect(header).toHaveAttribute("scope", "row");
        const faq = page.locator(".gp-s-faq details").first();
        await faq.locator("summary").focus();
        await page.keyboard.press("Enter");
        await expect(faq).toHaveAttribute("open", "");
        await page.keyboard.press("Enter");
        await expect(faq).not.toHaveAttribute("open", "");
        const ghost = page.locator(".gp-s-btn-ghost");
        const ghostColor = await ghost.evaluate(
          (el) => getComputedStyle(el).color,
        );
        expect(ghostColor).toBe(
          await page
            .locator("body")
            .evaluate((el) => getComputedStyle(el).color),
        );
        await ghost.hover();
        await expect(ghost).toHaveCSS("color", ghostColor);
        await page.mouse.move(0, 0);
        const styles = await page.evaluate(() => {
          const hero = document.querySelector(".gp-s-hero")!;
          const mask = getComputedStyle(hero, "::before").maskImage;
          const th = (n: number) =>
            getComputedStyle(
              document.querySelector(`.gp-s-compare thead th:nth-child(${n})`)!,
            ).backgroundColor;
          const luminance = (color: string) => {
            const values = (color.match(/[\d.]+/g) || [])
              .slice(0, 3)
              .map(Number)
              .map((value) => {
                const linear = value / 255;
                return linear <= 0.04045
                  ? linear / 12.92
                  : ((linear + 0.055) / 1.055) ** 2.4;
              });
            return values[0] * 0.2126 + values[1] * 0.7152 + values[2] * 0.0722;
          };
          const contrast = [
            ...document.querySelectorAll(".gp-s-btn:not(.gp-s-btn-ghost)"),
          ].map((button) => {
            const style = getComputedStyle(button);
            const [hi, lo] = [
              luminance(style.color),
              luminance(style.backgroundColor),
            ].sort((a, b) => b - a);
            return {
              ratio: (hi + 0.05) / (lo + 0.05),
              color: style.color,
              background: style.backgroundColor,
              html: button.outerHTML,
            };
          });
          return {
            contrast,
            mask,
            highlight: th(2),
            plain: th(3),
            steps: getComputedStyle(document.querySelector(".gp-s-steps")!)
              .listStyleType,
          };
        });
        expect(styles.mask).toContain("gradient");
        expect(styles.highlight).not.toBe(styles.plain);
        expect(styles.steps).toBe("none");
        expect(styles.contrast.length).toBeGreaterThan(0);
        await info.attach("native-computed-styles", {
          body: JSON.stringify(styles, null, 2),
          contentType: "application/json",
        });
        for (const button of styles.contrast)
          expect(button.ratio, JSON.stringify(button)).toBeGreaterThanOrEqual(
            4.5,
          );
        expect(errors).toEqual([]);
      });
