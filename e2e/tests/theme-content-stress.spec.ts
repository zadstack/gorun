import { test, expect } from "@playwright/test";
import { mkdirSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { csrf, db } from "../lib/helpers.ts";

test("five themes preserve bilingual stress content and keep it reachable", async ({
  page,
}, info) => {
  test.setTimeout(180_000);
  const output = resolve(
    process.env.THEME_STRESS_DIR ?? info.outputPath("stress"),
  );
  mkdirSync(output, { recursive: true });
  const database = db();
  const previous = database
    .prepare("SELECT value_json FROM settings WHERE key='site.theme'")
    .get() as { value_json: string } | undefined;
  database.close();
  const records: unknown[] = [];
  const fixtures: {
    locale: string;
    id: string;
    slug: string;
    snapshot: unknown;
  }[] = [];
  try {
    await page.goto("/admin");
    for (const locale of ["ar", "en"]) {
      const phrase =
        locale === "ar"
          ? "محتوى عربي طويل قابل للقراءة "
          : "Long editorial content remains readable ";
      const slug = `stress-${locale}`;
      const body = `<p>${phrase.repeat(100)}</p><p>${(locale === "ar" ? "كلمة" : "Unbroken").repeat(120)}</p><table><tbody><tr>${Array.from({ length: 16 }, (_, i) => `<th>${locale === "ar" ? "العمود" : "Column"} ${i}</th>`).join("")}</tr><tr>${Array.from({ length: 16 }, (_, i) => `<td>${locale === "ar" ? "قيمة" : "Value"} ${i} ${(locale === "ar" ? "معرف" : "word").repeat(12)}</td>`).join("")}</tr></tbody></table><pre>${"long_preformatted_line_".repeat(100)}</pre><img src="/missing-stress-image.png" alt="Missing image description ${locale}">`;
      const result = await page.request.post("/api/v1/content", {
        headers: { "X-CSRF-Token": await csrf(page) },
        data: {
          type: "post",
          locale,
          title: phrase.repeat(18),
          slug,
          status: "published",
          body,
        },
      });
      expect(result.status()).toBe(201);
      const id = (await result.json()).data.id;
      const read = await page.request.get(`/api/v1/content/${id}`);
      expect(read.status()).toBe(200);
      fixtures.push({ locale, id, slug, snapshot: (await read.json()).data });
    }
    for (const theme of [
      "default",
      "business",
      "portfolio",
      "magazine",
      "docs",
    ]) {
      await page.goto("/admin/appearance/themes");
      expect(
        (
          await page.request.post(
            `/admin/appearance/themes/${theme}/activate`,
            { form: { _csrf: await csrf(page) }, maxRedirects: 0 },
          )
        ).status(),
      ).toBe(303);
      for (const f of fixtures) {
        const read = await page.request.get(`/api/v1/content/${f.id}`);
        expect
          .soft((await read.json()).data, `${theme} API preservation`)
          .toEqual(f.snapshot);
        for (const width of [390, 1440]) {
          await page.setViewportSize({ width, height: 900 });
          expect(
            (await page.goto(`/${f.locale}/post/${f.slug}`))?.status(),
          ).toBe(200);
          await page.evaluate(() => document.fonts.ready);
          await expect
            .soft(page.locator("html"))
            .toHaveAttribute("dir", f.locale === "ar" ? "rtl" : "ltr");
          const metrics = await page.evaluate(() => {
            const accessible = (el: HTMLElement | null) => {
              if (!el) return false;
              for (let p: HTMLElement | null = el; p; p = p.parentElement) {
                if (p.scrollWidth > p.clientWidth + 1) {
                  const o = getComputedStyle(p).overflowX;
                  if (o === "hidden" || o === "clip") return false;
                  if (o === "auto" || o === "scroll") return true;
                }
              }
              const r = el.getBoundingClientRect();
              return r.left >= -1 && r.right <= innerWidth + 1;
            };
            const title = document.querySelector("main h1") as HTMLElement;
            const img = document.querySelector(
              'main img[src="/missing-stress-image.png"]',
            ) as HTMLImageElement;
            return {
              documentWidth: document.documentElement.scrollWidth,
              narrowestCellCharacters: Math.min(
                ...Array.from(
                  document.querySelectorAll("main th, main td"),
                  (cell) => {
                    const style = getComputedStyle(cell);
                    const context = document
                      .createElement("canvas")
                      .getContext("2d")!;
                    context.font = style.font;
                    const characterWidth = context.measureText("0").width;
                    return cell.getBoundingClientRect().width / characterWidth;
                  },
                ),
              ),
              tableReachable: accessible(document.querySelector("main table")),
              preReachable: accessible(document.querySelector("main pre")),
              titleUnclipped:
                !!title &&
                (!(
                  getComputedStyle(title).overflowY === "hidden" ||
                  getComputedStyle(title).overflowY === "clip"
                ) ||
                  title.scrollHeight <= title.clientHeight + 1),
              alt: img?.alt,
              missingImage: !!img && img.complete && img.naturalWidth === 0,
              imageWidth: img?.width,
              imageHeight: img?.height,
            };
          });
          const label = `${theme}/${f.locale}/${width}`;
          // Allow one hundredth of a character for subpixel font/layout rounding.
          expect
            .soft(
              metrics.narrowestCellCharacters,
              `${label} readable cell measure`,
            )
            .toBeGreaterThanOrEqual(14 - 0.01);
          const table = page.locator("main table");
          // Chromium exposes overflowing scroll containers in the natural Tab order.
          // Exercise the keyboard rather than injecting focusability in the fixture.
          await page.keyboard.press("Control+Home");
          for (let tab = 0; tab < 60; tab++) {
            await page.keyboard.press("Tab");
            if (await table.evaluate((el) => el === document.activeElement))
              break;
          }
          const keyboardFocused = await table.evaluate(
            (el) => el === document.activeElement,
          );
          expect
            .soft(keyboardFocused, `${label} table keyboard focus`)
            .toBe(true);
          const focusOutline = await table.evaluate((el) => {
            const style = getComputedStyle(el);
            return {
              style: style.outlineStyle,
              width: parseFloat(style.outlineWidth),
              color: style.outlineColor,
            };
          });
          if (keyboardFocused) {
            expect
              .soft(focusOutline.style, `${label} visible keyboard focus`)
              .not.toBe("none");
            expect
              .soft(focusOutline.width, `${label} focus outline width`)
              .toBeGreaterThanOrEqual(2);
            expect
              .soft(focusOutline.color, `${label} focus outline color`)
              .not.toBe("rgba(0, 0, 0, 0)");
          }
          let keyboardScrolled = false;
          if (keyboardFocused) {
            const start = await table.evaluate((el) => el.scrollLeft);
            await page.keyboard.press(
              f.locale === "ar" ? "ArrowLeft" : "ArrowRight",
            );
            await expect
              .poll(() => table.evaluate((el) => el.scrollLeft), {
                message: `${label} keyboard table scroll`,
              })
              .not.toBe(start);
            keyboardScrolled =
              (await table.evaluate((el) => el.scrollLeft)) !== start;
          }
          records.push({
            theme,
            locale: f.locale,
            width,
            ...metrics,
            keyboardFocused,
            focusOutline,
            keyboardScrolled,
          });
          expect
            .soft(metrics.documentWidth, `${label} viewport`)
            .toBeLessThanOrEqual(width + 1);
          expect.soft(metrics.tableReachable, `${label} table`).toBe(true);
          expect.soft(metrics.preReachable, `${label} pre`).toBe(true);
          expect.soft(metrics.titleUnclipped, `${label} title`).toBe(true);
          expect
            .soft(metrics.alt)
            .toBe(`Missing image description ${f.locale}`);
          expect.soft(metrics.missingImage).toBe(true);
          expect
            .soft(metrics.imageWidth, `${label} visible alt width`)
            .toBeGreaterThan(100);
          expect
            .soft(metrics.imageHeight, `${label} visible alt height`)
            .toBeGreaterThan(10);
          await page.screenshot({
            path: resolve(output, `${theme}-${f.locale}-${width}.png`),
            fullPage: true,
          });
        }
      }
    }
  } finally {
    await page.goto("/admin/appearance/themes");
    const token = await csrf(page);
    for (const fixture of fixtures) {
      expect(
        (
          await page.request.delete(`/api/v1/content/${fixture.id}?hard=1`, {
            headers: { "X-CSRF-Token": token },
          })
        ).status(),
      ).toBe(200);
      expect(
        (await page.request.get(`/api/v1/content/${fixture.id}`)).status(),
      ).toBe(404);
    }
    const previousTheme = previous
      ? JSON.parse(previous.value_json)
      : "default";
    expect(
      (
        await page.request.post(
          `/admin/appearance/themes/${previousTheme}/activate`,
          { form: { _csrf: token }, maxRedirects: 0 },
        )
      ).status(),
    ).toBe(303);
    writeFileSync(
      resolve(output, "theme-stress-acceptance.json"),
      JSON.stringify(
        {
          commit: "a75850d",
          records,
          errors: info.errors.map((e) => e.message),
        },
        null,
        2,
      ),
    );
  }
});
