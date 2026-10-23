import { test, expect, type BrowserContext, type Page } from "@playwright/test";
import { csrf, db, setLocale, submitAndLand } from "../lib/helpers.ts";
import { randomUUID } from "node:crypto";

test.use({ actionTimeout: 5000 });
const themes = ["default", "business", "magazine", "portfolio", "docs"];
let fixtureContext: BrowserContext,
  fixturePage: Page,
  mediaID = "",
  menuID = "";
const posts: { id: string; locale: string; slug: string; snapshot: unknown }[] =
  [];
const settingsSQL = "key LIKE 'site.%' OR key LIKE 'theme.%'";
function settings() {
  const d = db();
  try {
    return d
      .prepare(
        `SELECT key,value_json,updated_at FROM settings WHERE ${settingsSQL}`,
      )
      .all() as { key: string; value_json: string; updated_at: string }[];
  } finally {
    d.close();
  }
}
function restore(rows: ReturnType<typeof settings>) {
  const d = db();
  try {
    d.exec("BEGIN IMMEDIATE");
    d.prepare(`DELETE FROM settings WHERE ${settingsSQL}`).run();
    for (const row of rows)
      d.prepare(
        "INSERT INTO settings(key,value_json,updated_at) VALUES(?,?,?)",
      ).run(row.key, row.value_json, row.updated_at);
    d.exec("COMMIT");
  } finally {
    d.close();
  }
}
function setting(key: string, value: unknown) {
  const d = db();
  try {
    d.prepare(
      "INSERT INTO settings(key,value_json,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json,updated_at=excluded.updated_at",
    ).run(key, JSON.stringify(value), new Date().toISOString());
  } finally {
    d.close();
  }
}
function value(key: string) {
  const d = db();
  try {
    const row = d
      .prepare("SELECT value_json FROM settings WHERE key=?")
      .get(key) as { value_json: string } | undefined;
    return row ? JSON.parse(row.value_json) : undefined;
  } finally {
    d.close();
  }
}
function preserved() {
  const d = db();
  try {
    return ["content", "media", "menus", "menu_items"].map((table) => ({
      table,
      rows: d.prepare(`SELECT * FROM ${table} ORDER BY rowid`).all(),
    }));
  } finally {
    d.close();
  }
}

test.beforeAll(async ({ browser }) => {
  fixtureContext = await browser.newContext({
    storageState: ".auth/admin.json",
  });
  fixturePage = await fixtureContext.newPage();
  await fixturePage.goto("/admin");
  const headers = { "X-CSRF-Token": await csrf(fixturePage) };
  const upload = await fixturePage.request.post("/api/v1/media", {
    headers,
    multipart: {
      file: {
        name: `identity-${randomUUID()}.svg`,
        mimeType: "image/svg+xml",
        buffer: Buffer.from(
          '<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64"><rect width="64" height="64" fill="#6941a5"/></svg>',
        ),
      },
    },
  });
  expect(upload.status()).toBe(201);
  mediaID = (await upload.json()).data.id;
  for (const locale of ["ar", "en"]) {
    const slug = `identity-${locale}-${randomUUID().slice(0, 8)}`;
    const created = await fixturePage.request.post("/api/v1/content", {
      headers,
      data: {
        type: "post",
        locale,
        slug,
        title: `Identity fixture ${locale}`,
        status: "published",
        body: "<p>Preserved identity acceptance content.</p>",
        featured_media_id: mediaID,
      },
    });
    expect(created.status()).toBe(201);
    const id = (await created.json()).data.id;
    const read = await fixturePage.request.get(`/api/v1/content/${id}`);
    expect(read.status()).toBe(200);
    posts.push({ id, locale, slug, snapshot: (await read.json()).data });
  }
  const menu = await fixturePage.request.post("/api/v1/menus", {
    headers,
    data: {
      slug: `identity-${randomUUID().slice(0, 8)}`,
      name: "Identity preserved menu",
      location: "",
      items: [
        {
          label: "Preserved link",
          kind: "custom",
          url: "/preserved-navigation",
        },
      ],
    },
  });
  expect(menu.status()).toBe(201);
  menuID = (await menu.json()).data.id;
});

test.afterAll(async () => {
  if (!fixturePage) return;
  try {
    await fixturePage.goto("/admin");
    const headers = { "X-CSRF-Token": await csrf(fixturePage) };
    for (const post of posts) {
      expect(
        (
          await fixturePage.request.delete(
            `/api/v1/content/${post.id}?hard=1`,
            { headers },
          )
        ).status(),
      ).toBe(200);
      expect(
        (await fixturePage.request.get(`/api/v1/content/${post.id}`)).status(),
      ).toBe(404);
    }
    if (menuID) {
      expect(
        (
          await fixturePage.request.delete(`/api/v1/menus/${menuID}`, {
            headers,
          })
        ).status(),
      ).toBe(200);
      expect(
        (await fixturePage.request.get(`/api/v1/menus/${menuID}`)).status(),
      ).toBe(404);
    }
    if (mediaID) {
      expect(
        (
          await fixturePage.request.delete(`/api/v1/media/${mediaID}`, {
            headers,
          })
        ).status(),
      ).toBe(200);
      expect(
        (await fixturePage.request.get(`/api/v1/media/${mediaID}`)).status(),
      ).toBe(404);
    }
  } finally {
    await fixtureContext.close();
  }
});

for (const theme of themes)
  for (const locale of ["ar", "en"] as const)
    for (const width of [375, 1280]) {
      test(`${theme} ${locale} ${width}: draft preview apply revert preserves site data`, async ({
        page,
      }, info) => {
        const saved = settings();
        const usersDB = db();
        const userLocales = usersDB
          .prepare("SELECT id,locale,updated_at FROM users")
          .all() as { id: string; locale: string; updated_at: string }[];
        usersDB.close();
        const baseline = `Baseline ${theme} ${locale} ${width}`,
          changed = `Draft ${theme} ${locale} ${width}`;
        try {
          await page.goto("/admin/appearance/themes");
          expect(
            (
              await page.request.post(
                `/admin/appearance/themes/${theme}/activate`,
                { form: { _csrf: await csrf(page) }, maxRedirects: 0 },
              )
            ).status(),
          ).toBe(303);
          const d = db();
          d.prepare("DELETE FROM settings WHERE key IN (?,?)").run(
            `theme.${theme}.identity_draft`,
            `theme.${theme}.identity_history`,
          );
          d.close();
          // Own the latest-posts homepage: a prior import may leave an Arabic
          // static front page whose explicit SEO title correctly overrides identity.
          // The full site/theme settings snapshot restores that page afterwards.
          for (const [key, entry] of Object.entries({
            "site.title": baseline,
            "site.front_page": "",
            "site.logo": mediaID,
            "site.logo_light": mediaID,
            "site.logo_dark": mediaID,
            "site.icon": mediaID,
            [`theme.${theme}.acceptance_extra`]: "keep-extra",
            [`theme.${theme}.cta_label`]: "Keep CTA",
            [`theme.${theme}.cta_url`]: "/preserved-cta",
          }))
            setting(key, entry);
          const before = preserved();
          await page.setViewportSize({ width, height: 900 });
          await page.goto("/admin/appearance/identity");
          await setLocale(page, locale);
          // Capture the real editor before changing identity or choosing a preview.
          await page.screenshot({
            path: info.outputPath("before.png"),
            fullPage: true,
          });
          const form = page.locator("[data-identity-form]"),
            frame = page.frameLocator("[data-identity-preview-frame]");
          await expect(frame.locator("html")).toHaveAttribute(
            "data-theme",
            theme,
          );
          await expect(frame.locator("html")).toHaveAttribute("lang", locale);
          await expect(frame.locator("html")).toHaveAttribute(
            "dir",
            locale === "ar" ? "rtl" : "ltr",
          );
          await page
            .locator(
              `[data-preview-width="${width === 375 ? "mobile" : "desktop"}"]`,
            )
            .click();
          await expect(
            page.locator("[data-identity-frame-wrap]"),
          ).toHaveAttribute("data-width", width === 375 ? "mobile" : "desktop");
          await form.locator('[name="site.title"]').fill(changed);
          await expect
            .poll(
              () =>
                value(`theme.${theme}.identity_draft`)?.snapshot.site[
                  "site.title"
                ],
            )
            .toBe(changed);
          await expect
            .poll(() => frame.locator("title").textContent())
            .toContain(changed);
          expect(value("site.title")).toBe(baseline);
          const publicPage = await page.context().newPage();
          try {
            expect((await publicPage.goto(`/${locale}/`))?.status()).toBe(200);
            await expect(publicPage.locator("html")).toHaveAttribute(
              "data-kind",
              "home",
            );
            expect(await publicPage.title()).toContain(baseline);
            expect(await publicPage.title()).not.toContain(changed);
          } finally {
            await publicPage.close();
          }
          expect(preserved()).toEqual(before);
          const fullPreview = await page.context().newPage();
          try {
            await fullPreview.setViewportSize({ width, height: 900 });
            expect(
              (
                await fullPreview.goto(
                  (await page
                    .locator("[data-identity-preview-frame]")
                    .getAttribute("src"))!,
                )
              )?.status(),
            ).toBe(200);
            await expect(fullPreview.locator("html")).toHaveAttribute(
              "data-theme",
              theme,
            );
            await expect(fullPreview.locator("html")).toHaveAttribute(
              "lang",
              locale,
            );
            await expect(fullPreview.locator("html")).toHaveAttribute(
              "dir",
              locale === "ar" ? "rtl" : "ltr",
            );
            expect(await fullPreview.title()).toContain(changed);
            await fullPreview.screenshot({
              path: info.outputPath("preview-exact-width.png"),
              fullPage: true,
            });
          } finally {
            await fullPreview.close();
          }
          // Both real preview kinds must keep the selected locale and active theme.
          await page.locator('[data-preview-view="single"]').click();
          await expect(frame.locator("html")).toHaveAttribute(
            "data-kind",
            "single",
          );
          await expect(frame.locator("html")).toHaveAttribute("lang", locale);
          await expect(frame.locator("html")).toHaveAttribute(
            "dir",
            locale === "ar" ? "rtl" : "ltr",
          );
          await expect(frame.locator("html")).toHaveAttribute(
            "data-theme",
            theme,
          );
          await submitAndLand(
            page,
            form.locator("button[name=action][value=apply]"),
            "/admin/appearance/identity",
          );
          await expect(page).toHaveURL(/identity\?saved=1/);
          expect(value("site.title")).toBe(changed);
          expect(
            await (await page.request.get(`/${locale}/`)).text(),
          ).toContain(changed);
          expect(value(`theme.${theme}.identity_draft`)).toBeUndefined();
          const history = value(`theme.${theme}.identity_history`);
          expect(history.length).toBeGreaterThanOrEqual(2);
          const initial = history.find(
            (revision: any) => revision.kind === "baseline",
          );
          expect(initial.snapshot.site["site.title"]).toBe(baseline);
          await page.screenshot({
            path: info.outputPath("applied.png"),
            fullPage: true,
          });
          const revert = page
            .locator(".identity-history form")
            .filter({
              has: page.locator(`input[name=revision][value="${initial.id}"]`),
            })
            .locator("button[value=revert]");
          await revert.scrollIntoViewIfNeeded();
          const dock = page.locator("[data-dock-open]");
          await expect(dock).toBeVisible();
          await expect(dock).toBeInViewport();
          const geometry = await revert.evaluate((el) => {
            const measure = () => {
              const r = el.getBoundingClientRect(),
                dock = document
                  .querySelector("[data-dock-open]")
                  ?.getBoundingClientRect(),
                hit = document.elementFromPoint(
                  r.x + r.width / 2,
                  r.y + r.height / 2,
                );
              return {
                scrollY,
                maxScroll: document.documentElement.scrollHeight - innerHeight,
                button: {
                  x: r.x,
                  y: r.y,
                  width: r.width,
                  height: r.height,
                  bottom: r.bottom,
                },
                dock: dock
                  ? {
                      x: dock.x,
                      y: dock.y,
                      width: dock.width,
                      height: dock.height,
                      bottom: dock.bottom,
                    }
                  : null,
                pointerReachable: hit === el || (!!hit && el.contains(hit)),
                hit: hit?.outerHTML.slice(0, 250),
              };
            };
            const initial = measure();
            window.scrollTo({
              top: document.documentElement.scrollHeight,
              behavior: "instant",
            });
            return { initial, atScrollEnd: measure() };
          });
          await info.attach("revert-pointer-geometry", {
            body: JSON.stringify(geometry, null, 2),
            contentType: "application/json",
          });
          await page.screenshot({
            path: info.outputPath("revert-pointer.png"),
          });
          expect(
            Math.abs(
              geometry.atScrollEnd.scrollY - geometry.atScrollEnd.maxScroll,
            ),
            "geometry was measured at maximum scroll",
          ).toBeLessThanOrEqual(1);
          expect(geometry.atScrollEnd.dock).not.toBeNull();
          expect(geometry.atScrollEnd.dock!.width).toBeGreaterThan(0);
          expect(geometry.atScrollEnd.dock!.height).toBeGreaterThan(0);
          expect(
            geometry.atScrollEnd.pointerReachable,
            "Revert remains reachable above fixed overlays at maximum scroll",
          ).toBe(true);
          await submitAndLand(page, revert, "/admin/appearance/identity");
          await expect(page).toHaveURL(/identity\?reverted=1/);
          expect(value("site.title")).toBe(baseline);
          expect(
            await (await page.request.get(`/${locale}/`)).text(),
          ).toContain(baseline);
          expect(value(`theme.${theme}.identity_history`)[0].kind).toBe(
            "revert",
          );
          expect(value(`theme.${theme}.acceptance_extra`)).toBe("keep-extra");
          expect(value(`theme.${theme}.cta_label`)).toBe("Keep CTA");
          expect(value(`theme.${theme}.cta_url`)).toBe("/preserved-cta");
          for (const key of [
            "site.logo",
            "site.logo_light",
            "site.logo_dark",
            "site.icon",
          ])
            expect(value(key)).toBe(mediaID);
          expect(preserved()).toEqual(before);
          for (const post of posts) {
            const read = await page.request.get(`/api/v1/content/${post.id}`);
            expect(read.status()).toBe(200);
            expect((await read.json()).data).toEqual(post.snapshot);
          }
        } finally {
          try {
            if (!page.isClosed()) await page.goto("/admin");
          } finally {
            restore(saved);
            const d = db();
            for (const user of userLocales)
              d.prepare(
                "UPDATE users SET locale=?,updated_at=? WHERE id=?",
              ).run(user.locale, user.updated_at, user.id);
            d.close();
            const front = saved.find((row) => row.key === "site.front_page");
            expect(value("site.front_page")).toEqual(
              front ? JSON.parse(front.value_json) : undefined,
            );
          }
        }
      });
    }
