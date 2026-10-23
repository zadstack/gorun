import { test, expect, type Page, type Frame } from "@playwright/test";
import { readFileSync, writeFileSync, mkdirSync } from "node:fs";
import { resolve } from "node:path";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { csrf, db, MARKER_500 } from "../lib/helpers.ts";

const themes = ["default", "business", "magazine", "portfolio", "docs"];
const locales = ["ar", "en"];
const widths = [375, 1280];

async function probe(surface: Page | Frame, kind: "pilot" | "builder") {
  return surface.evaluate((kind) => {
    const selector = kind === "pilot" ? ".gp-s-hero" : ".gp-hero";
    const hero = document.querySelector(selector)!;
    const card = document.querySelector(
      kind === "pilot" ? ".gp-s-card" : ".gp-features .gp-entry",
    )!;
    const grid = document.querySelector(
      kind === "pilot" ? ".gp-s-grid" : ".gp-features .gp-grid",
    )!;
    const button = document.querySelector(
      kind === "pilot" ? ".gp-s-btn" : ".gp-hero .gp-button",
    )!;
    const title = hero.querySelector("h1")!;
    const style = (el: Element) => {
      const s = getComputedStyle(el),
        r = el.getBoundingClientRect();
      return {
        color: s.color,
        background: s.backgroundColor,
        fontFamily: s.fontFamily,
        fontSize: s.fontSize,
        fontWeight: s.fontWeight,
        lineHeight: s.lineHeight,
        padding: s.padding,
        borderRadius: s.borderRadius,
        display: s.display,
        width: r.width,
        height: r.height,
      };
    };
    const summary = document.querySelector(
      kind === "pilot" ? ".gp-s-faq summary" : ".gp-faq summary",
    )!;
    const layer = getComputedStyle(hero, "::before");
    const root = getComputedStyle(document.documentElement);
    const overflowing = Array.from(document.querySelectorAll("body *"))
      .map((el) => ({ el, r: el.getBoundingClientRect() }))
      .filter(
        ({ r }) => r.width > 0 && (r.left < -1 || r.right > innerWidth + 1),
      )
      .slice(0, 12)
      .map(({ el, r }) => {
        const p = el.parentElement!,
          pr = p.getBoundingClientRect(),
          cs = getComputedStyle(el);
        return {
          selector:
            el.tagName.toLowerCase() + "." + Array.from(el.classList).join("."),
          left: r.left,
          right: r.right,
          width: r.width,
          parent:
            p.tagName.toLowerCase() + "." + Array.from(p.classList).join("."),
          parentLeft: pr.left,
          parentWidth: pr.width,
          marginInline: cs.marginInline,
          widthCSS: cs.width,
        };
      });
    return {
      rootTokens: Object.fromEntries(
        [
          "--gp-accent",
          "--gp-text",
          "--gp-bg",
          "--gp-width",
          "--accent",
          "--ink",
          "--green",
        ].map((key) => [key, root.getPropertyValue(key).trim()]),
      ),
      styles: Array.from(
        document.head.querySelectorAll("link[rel=stylesheet],style"),
      ).map((el, index) => ({
        index,
        href: el.getAttribute("href"),
        builderIdentity: el.hasAttribute("data-gorun-builder-identity"),
        tokens:
          el.textContent?.match(/--gp-(?:accent|text|bg|width)[^;{}]*/g) ?? [],
      })),
      overflowing,
      headingAlign: getComputedStyle(hero.querySelector(".gp-heading") ?? hero)
        .alignItems,
      theme: document.documentElement.dataset.theme,
      lang: document.documentElement.lang,
      dir: document.documentElement.dir,
      viewport: innerWidth,
      viewportHeight: innerHeight,
      documentWidth: document.documentElement.scrollWidth,
      hero: style(hero),
      card: style(card),
      button: style(button),
      title: style(title),
      gridColumns: getComputedStyle(grid).gridTemplateColumns,
      summary: summary.textContent?.trim(),
      mask: layer.maskImage,
      sectionsStylesheets: Array.from(
        document.querySelectorAll("link[rel=stylesheet]"),
      )
        .map((el) => el.getAttribute("href"))
        .filter((h) => h?.includes("sections.css")),
      pilotSections: document.querySelectorAll('[class*="gp-s-"]').length,
      builderSections: document.querySelectorAll(".gp-section").length,
    };
  }, kind);
}

function documentFixture(locale: string) {
  const ar = locale === "ar";
  return {
    version: 1,
    sections: [
      {
        id: "audit-hero",
        kind: "hero",
        variant: "split",
        title: ar
          ? "دليل تشغيل واضح لفريقك"
          : "A clear operating guide for your team",
        text: ar
          ? "محتوى تجريبي للتحقق من المعاينة والنشر في الثيم المحدد."
          : "Synthetic content for checking preview and publishing in the selected theme.",
        link: "#gp-audit-faq",
        link_label: ar ? "الأسئلة الشائعة" : "Common questions",
      },
      {
        id: "audit-features",
        kind: "features",
        variant: "default",
        title: ar ? "مميزات قابلة للقراءة" : "Readable features",
        items: Array.from({ length: 3 }, (_, i) => ({
          title: ar ? `ميزة ${i + 1}` : `Feature ${i + 1}`,
          text: ar
            ? "تفاصيل واضحة وخطوة عملية تالية."
            : "Clear details and a practical next step.",
        })),
      },
      {
        id: "audit-faq",
        kind: "faq",
        variant: "default",
        title: ar ? "أسئلة شائعة" : "Common questions",
        items: [
          {
            title: ar ? "كيف يعمل؟" : "How does it work?",
            text: ar
              ? "يُحفظ المحتوى بشكل مستقل عن الثيم."
              : "Content remains separate from the active theme.",
          },
        ],
      },
    ],
  };
}

test("pilot sections and native builder previews across five active themes", async ({
  page,
}, info) => {
  test.setTimeout(240_000);
  const output = resolve(
    process.env.PILOT_ACCEPTANCE_DIR ?? info.outputPath("cross-theme"),
  );
  mkdirSync(output, { recursive: true });
  const records: unknown[] = [],
    fixtures: {
      id: string;
      kind: "pilot" | "builder";
      locale: string;
      path: string;
      snapshot: unknown;
    }[] = [];
  const database = db();
  const previous = database
    .prepare(
      "SELECT value_json,updated_at FROM settings WHERE key='site.theme'",
    )
    .get() as { value_json: string; updated_at: string } | undefined;
  database.close();
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  const publicPage = await page.context().newPage();
  publicPage.on("pageerror", (e) => errors.push(e.message));
  let mainError: string | undefined;
  try {
    await page.goto("/admin");
    for (const locale of locales)
      for (const kind of ["pilot", "builder"] as const) {
        const body =
          kind === "pilot"
            ? readFileSync(
                resolve(
                  `../internal/theme/testdata/sections-pilot.${locale}.html`,
                ),
                "utf8",
              )
            : "<p>Private builder fixture</p>";
        const slug = `cross-theme-${kind}-${locale}`;
        const created = await page.request.post("/api/v1/content", {
          headers: { "X-CSRF-Token": await csrf(page) },
          data: {
            type: "page",
            title: `Acceptance ${kind} ${locale}`,
            slug,
            locale,
            status: "published",
            body,
          },
        });
        expect(created.status()).toBe(201);
        const id = (await created.json()).data.id;
        fixtures.push({
          id,
          kind,
          locale,
          path: `${locale === "ar" ? "" : "/en"}/${slug}`,
          snapshot: null,
        });
        if (kind === "builder") {
          const draft = (
            await (await page.request.get(`/api/v1/builder/${id}`)).json()
          ).data;
          const saved = await page.request.put(`/api/v1/builder/${id}/draft`, {
            headers: { "X-CSRF-Token": await csrf(page) },
            data: {
              document: documentFixture(locale),
              base_updated_at: draft.updated_at,
            },
          });
          expect(saved.status()).toBe(200);
          const published = await page.request.post(
            `/api/v1/builder/${id}/publish`,
            {
              headers: { "X-CSRF-Token": await csrf(page) },
              data: { updated_at: (await saved.json()).data.updated_at },
            },
          );
          expect(published.status()).toBe(200);
        }
        fixtures.at(-1)!.snapshot = (
          await (await page.request.get(`/api/v1/content/${id}`)).json()
        ).data;
      }
    for (const theme of themes) {
      await page.goto("/admin/appearance/themes");
      expect(
        (
          await page.request.post(
            `/admin/appearance/themes/${theme}/activate`,
            { form: { _csrf: await csrf(page) }, maxRedirects: 0 },
          )
        ).status(),
      ).toBe(303);
      for (const fixture of fixtures) {
        const read = await page.request.get(`/api/v1/content/${fixture.id}`);
        expect(read.status()).toBe(200);
        expect((await read.json()).data).toEqual(fixture.snapshot);
        for (const width of widths) {
          await publicPage.setViewportSize({ width, height: 900 });
          const response = await publicPage.goto(fixture.path);
          expect(response?.status()).toBe(200);
          expect(
            await publicPage.content(),
            "public page must not be the theme failure document",
          ).not.toContain(MARKER_500);
          await expect(publicPage.locator("body")).not.toContainText(
            "Internal Server Error",
          );
          await publicPage.evaluate(() => document.fonts.ready);
          const published = await probe(publicPage, fixture.kind);
          await publicPage.screenshot({
            path: resolve(
              output,
              `${fixture.kind}-${theme}-${fixture.locale}-${width}-public.png`,
            ),
            fullPage: true,
          });
          let preview = null;
          if (fixture.kind === "builder") {
            // The real editor requests /preview and assigns its HTML to this sandboxed iframe.
            await page.setViewportSize({ width: 1600, height: 1100 });
            await page.goto(`/admin/builder/${fixture.id}`);
            const iframe = page.locator("#builder-preview");
            await iframe.waitFor();
            await expect(
              page.frameLocator("#builder-preview").locator(".gp-hero"),
            ).toBeVisible();
            // Harness viewport only: set the iframe's CSS width to match the public viewport.
            await iframe.evaluate((el, width) => {
              el.style.width = `${width}px`;
              el.style.maxWidth = `${width}px`;
              el.style.minWidth = `${width}px`;
              el.style.height = "900px";
              el.style.border = "0";
              el.style.boxSizing = "content-box";
            }, width);
            const frame = await iframe
              .elementHandle()
              .then((el) => el!.contentFrame());
            if (!frame) throw Error("builder frame unavailable");
            await frame.evaluate(() => document.fonts.ready);
            expect(
              await frame.content(),
              "preview must not be the theme failure document",
            ).not.toContain(MARKER_500);
            await expect(frame.locator("body")).not.toContainText(
              "Internal Server Error",
            );
            preview = await probe(frame, "builder");
            await iframe.screenshot({
              path: resolve(
                output,
                `builder-${theme}-${fixture.locale}-${width}-preview.png`,
              ),
            });
          }
          expect.soft(published.theme).toBe(theme);
          expect.soft(published.lang).toBe(fixture.locale);
          expect
            .soft(published.dir)
            .toBe(fixture.locale === "ar" ? "rtl" : "ltr");
          expect
            .soft(
              published.documentWidth,
              `${theme}/${fixture.kind}/${fixture.locale}/${width} viewport overflow`,
            )
            .toBeLessThanOrEqual(width + 1);
          if (preview) {
            expect.soft(preview.theme).toBe(theme);
            expect.soft(preview.viewport).toBe(width);
            expect.soft(preview.viewportHeight).toBe(published.viewportHeight);
            expect
              .soft(
                preview.button.background,
                `${theme}/${fixture.locale}/${width} preview CTA palette`,
              )
              .toBe(published.button.background);
            expect
              .soft(
                preview.title.color,
                `${theme}/${fixture.locale}/${width} preview title color`,
              )
              .toBe(published.title.color);
            expect
              .soft(
                preview.headingAlign,
                `${theme}/${fixture.locale}/${width} preview CTA alignment`,
              )
              .toBe(published.headingAlign);
          }
          expect
            .soft(published.sectionsStylesheets)
            .toHaveLength(fixture.kind === "pilot" ? 1 : 0);
          if (fixture.kind === "pilot") {
            expect.soft(published.mask).toContain("gradient");
            const faq = publicPage.locator(".gp-s-faq details").first();
            await faq.locator("summary").focus();
            await faq.locator("summary").press("Enter");
            await expect.soft(faq).toHaveAttribute("open", "");
            await faq.locator("summary").press("Space");
            await expect.soft(faq).not.toHaveAttribute("open", "");
          }
          if (preview) {
            expect.soft(preview.lang).toBe(fixture.locale);
            expect.soft(preview.dir).toBe(published.dir);
            expect.soft(preview.documentWidth).toBeLessThanOrEqual(width + 1);
            for (const selector of [
              "hero",
              "card",
              "button",
              "title",
            ] as const) {
              const {
                width: publicWidth,
                height: publicHeight,
                ...publicStyle
              } = published[selector];
              const {
                width: previewWidth,
                height: previewHeight,
                ...previewStyle
              } = preview[selector];
              expect
                .soft(
                  previewStyle,
                  `${theme}/${fixture.locale}/${width} ${selector} preview cascade`,
                )
                .toEqual(publicStyle);
              expect
                .soft(Math.abs(previewWidth - publicWidth))
                .toBeLessThanOrEqual(1);
              expect
                .soft(Math.abs(previewHeight - publicHeight))
                .toBeLessThanOrEqual(1);
            }
          }
          records.push({
            selectedTheme: theme,
            locale: fixture.locale,
            width,
            kind: fixture.kind,
            published,
            preview,
          });
        }
      }
    }
  } catch (error) {
    mainError = error instanceof Error ? error.message : String(error);
    throw error;
  } finally {
    const cleanupErrors: string[] = [];
    const attempt = async (label: string, action: () => Promise<void>) => {
      try {
        await action();
      } catch (error) {
        cleanupErrors.push(
          `${label}: ${error instanceof Error ? error.message : String(error)}`,
        );
      }
    };
    let token = "";
    await attempt("load cleanup CSRF", async () => {
      await page.goto("/admin/appearance/themes");
      token = await csrf(page);
    });
    for (const fixture of fixtures) {
      await attempt(`delete ${fixture.id}`, async () => {
        expect(
          (
            await page.request.delete(`/api/v1/content/${fixture.id}?hard=1`, {
              headers: { "X-CSRF-Token": token },
            })
          ).status(),
        ).toBe(200);
      });
      await attempt(`verify deletion ${fixture.id}`, async () => {
        expect(
          (await page.request.get(`/api/v1/content/${fixture.id}`)).status(),
        ).toBe(404);
      });
    }
    // Restore the exact fixture-owned row independently of HTTP/CSRF failures.
    // Foreign SQLite writes invalidate settings.Store through settings_revision;
    // the real public GET below also verifies the running theme sees the value.
    await attempt("restore original theme setting", async () => {
      const restore = db();
      try {
        if (previous)
          restore
            .prepare(
              "INSERT INTO settings(key,value_json,updated_at) VALUES('site.theme',?,?) ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json,updated_at=excluded.updated_at",
            )
            .run(previous.value_json, previous.updated_at);
        else
          restore.prepare("DELETE FROM settings WHERE key='site.theme'").run();
        expect(
          restore
            .prepare(
              "SELECT value_json,updated_at FROM settings WHERE key='site.theme'",
            )
            .get(),
        ).toEqual(previous);
      } finally {
        restore.close();
      }
    });
    await attempt("verify restored active theme", async () => {
      expect((await publicPage.goto("/"))?.status()).toBe(200);
      await expect(publicPage.locator("html")).toHaveAttribute(
        "data-theme",
        previous ? JSON.parse(previous.value_json) : "default",
      );
    });
    await attempt("close public page", async () => {
      await publicPage.close();
    });
    expect.soft(errors, "editor/public JavaScript errors").toEqual([]);
    await attempt("write acceptance report", async () => {
      writeFileSync(
        resolve(output, "pilot-cross-theme-acceptance.json"),
        JSON.stringify(
          {
            testedHead: execFileSync("git", ["rev-parse", "HEAD"], {
              encoding: "utf8",
            }).trim(),
            trackedDirty:
              execFileSync(
                "git",
                ["status", "--porcelain", "--untracked-files=no"],
                { encoding: "utf8" },
              ).trim() !== "",
            sources: Object.fromEntries(
              [
                "internal/theme/builder_preview.go",
                "internal/builder/starter_preview.go",
                "themes/docs/assets/theme.css",
                "e2e/tests/pilot-cross-theme.spec.ts",
              ].map((file) => [
                file,
                createHash("sha256")
                  .update(readFileSync(resolve("..", file)))
                  .digest("hex"),
              ]),
            ),
            productionChanges: execFileSync(
              "git",
              [
                "diff",
                "--name-only",
                "--",
                "themes",
                "internal/builder",
                "internal/theme",
              ],
              { encoding: "utf8" },
            ).trim(),
            fixtureSHA256: Object.fromEntries(
              locales.map((l) => [
                l,
                createHash("sha256")
                  .update(
                    readFileSync(
                      resolve(
                        `../internal/theme/testdata/sections-pilot.${l}.html`,
                      ),
                    ),
                  )
                  .digest("hex"),
              ]),
            ),
            records,
            pageErrors: errors,
            mainError,
            cleanupErrors,
            assertionErrors: info.errors.map((e) => e.message),
          },
          null,
          2,
        ),
      );
    });
    await attempt("attach cleanup results", async () => {
      await info.attach("pilot-cleanup-results", {
        body: JSON.stringify({ mainError, cleanupErrors }, null, 2),
        contentType: "application/json",
      });
    });
    expect
      .soft(cleanupErrors, "all cleanup and restoration steps must succeed")
      .toEqual([]);
  }
});
