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
      const body = `<p>${phrase.repeat(100)}</p><p>${(locale === "ar" ? "كلمة" : "Unbroken").repeat(120)}</p><table><tbody><tr>${Array.from({ length: 16 }, (_, i) => `<th>Column ${i}</th>`).join("")}</tr><tr>${Array.from({ length: 16 }, (_, i) => `<td>Value ${i} ${"word".repeat(12)}</td>`).join("")}</tr></tbody></table><pre>${"long_preformatted_line_".repeat(100)}</pre><img src="/missing-stress-image.png" alt="Missing image description ${locale}">`;
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
          records.push({ theme, locale: f.locale, width, ...metrics });
          const label = `${theme}/${f.locale}/${width}`;
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
    const restore = db();
    if (previous)
      restore
        .prepare("UPDATE settings SET value_json=? WHERE key='site.theme'")
        .run(previous.value_json);
    else restore.prepare("DELETE FROM settings WHERE key='site.theme'").run();
    restore.close();
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
