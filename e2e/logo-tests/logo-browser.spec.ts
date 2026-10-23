import { test, expect, type Page } from "@playwright/test";
import { DatabaseSync } from "node:sqlite";
import { join } from "node:path";
import { csrf, setLocale } from "../lib/helpers.ts";

const route = "/admin/appearance/identity/logo";
const password = "logo-browser-password-0001";
function db() {
  return new DatabaseSync(join(process.env.E2E_LOGO_DATA!, "gorun.db"));
}
function snapshot() {
  const d = db();
  try {
    return {
      settings: d.prepare("SELECT * FROM settings ORDER BY key").all(),
      content: d.prepare("SELECT * FROM content ORDER BY id").all(),
      menus: d.prepare("SELECT * FROM menus ORDER BY id").all(),
      items: d.prepare("SELECT * FROM menu_items ORDER BY menu_id,id").all(),
      media: d.prepare("SELECT * FROM media ORDER BY id").all(),
    };
  } finally {
    d.close();
  }
}
async function login(page: Page, role = "admin") {
  await page.goto("/admin/login");
  await page.locator("[name=email]").fill(`${role}@logo-browser.test`);
  await page.locator("[name=password]").fill(password);
  await page
    .locator('form[action^="/admin/login"] button[type=submit]')
    .click();
  await expect(page).not.toHaveURL(/login/);
}
async function stats(page: Page) {
  const r = await page.request.get("/fixture/logo/stats");
  expect(r.ok()).toBeTruthy();
  return r.json();
}
async function configure(page: Page, known = true) {
  const detail = page.locator("[data-logo-config]").first();
  await detail.locator("summary").click();
  const form = detail.locator("form");
  await form.locator("[name=estimate]").fill(known ? "0.04" : "");
  await form
    .locator("[name=source]")
    .fill(known ? "https://pricing.invalid/images" : "");
  await form.locator("[name=as_of]").fill(known ? "2026-10-23" : "");
  await form
    .locator("[name=basis]")
    .fill(known ? "Local fixture quote: one static PNG" : "");
  await form.locator("button[type=submit]").click();
  await expect(page.locator("[data-logo-error]")).toHaveCount(0);
}
async function review(page: Page, prompt: string) {
  const form = page.locator("[data-logo-brief]");
  await form.locator("[name=prompt]").fill(prompt);
  await form.locator("[name=budget]").fill("0.05");
  await form.locator("[name=currency]").fill("USD");
  await form.locator("button[type=submit]").click();
}
async function noOverflow(page: Page) {
  const geometry = await page.evaluate(() => ({
    width: innerWidth,
    content: document.documentElement.scrollWidth,
  }));
  expect(geometry.content).toBeLessThanOrEqual(geometry.width + 1);
}
async function generate(page: Page) {
  const button = page.locator("[data-logo-generate]");
  await button.focus();
  await page.keyboard.press("Shift+Tab");
  await expect(page.locator("[name=approved]")).toBeFocused();
  await page.keyboard.press("Space");
  await expect(page.locator("[name=approved]")).toBeChecked();
  await page.keyboard.press("Tab");
  await expect(button).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page.locator("[data-logo-candidate]")).toBeVisible();
}

for (const locale of ["ar", "en"] as const)
  for (const width of [375, 1280]) {
    test(`${locale} ${width}: reviewed PNG stays private until draft selection`, async ({
      page,
      browser,
    }, info) => {
      await page.setViewportSize({ width, height: 900 });
      await login(page);
      const initial = await page.goto(route);
      expect(initial?.status()).toBe(200);
      await setLocale(page, locale);
      await page.screenshot({
        path: info.outputPath("logo-before.png"),
        fullPage: true,
      });
      await expect(page.locator("html")).toHaveAttribute("lang", locale);
      await expect(page.locator("html")).toHaveAttribute(
        "dir",
        locale === "ar" ? "rtl" : "ltr",
      );
      await expect(page.locator("body")).toContainText(
        locale === "ar"
          ? "لا صور مرجعية أو SVG أو GIF أو رسوم متحركة"
          : "No reference images, SVG, GIF or animation",
      );
      const before = snapshot();
      expect(before.media).toHaveLength(0); // Dedicated temp fixture owns no prior assets.
      const calls = (await stats(page)).calls;
      try {
        await configure(page);
        expect((await stats(page)).calls).toBe(calls);
        const prompt = `Synthetic ${locale} logo: coral square only`;
        await review(page, prompt);
        await expect(page.locator("[data-logo-review]")).toBeVisible();
        await noOverflow(page);
        const payload = JSON.parse(
          await page.locator("[data-logo-payload]").innerText(),
        );
        expect(payload.prompt).toBe(prompt);
        expect(payload.n).toBe(1);
        expect(payload.size).toBe("1024x1024");
        await expect(page.locator("[data-logo-cost]")).toContainText("0.04");
        await expect(page.locator("[data-logo-cost]")).toContainText("USD");
        expect((await stats(page)).calls).toBe(calls);
        // Browser validation blocks an unchecked approval; server also rejects direct forgery.
        await page.locator("[data-logo-generate]").click();
        expect((await stats(page)).calls).toBe(calls);
        const token = await page.locator("[name=review]").inputValue();
        const csrfToken = await csrf(page);
        const forged = await page.request.post(route, {
          form: {
            action: "generate",
            review: "unknown-token",
            approved: "1",
            _csrf: csrfToken,
          },
        });
        expect(forged.status()).toBe(422);
        expect((await stats(page)).calls).toBe(calls);
        const denied = await page.request.post(route, {
          form: { action: "generate", review: token, _csrf: csrfToken },
        });
        expect(denied.status()).toBe(422);
        expect((await stats(page)).calls).toBe(calls);
        // An unsuccessful submission may consume approval; explicitly review again.
        await page.goto(route);
        await review(page, prompt);
        await generate(page);
        await page.reload();
        await expect(page.locator("[data-logo-candidate]")).toBeVisible();
        await noOverflow(page);
        const afterGenerate = await stats(page);
        expect(afterGenerate.calls).toBe(calls + 1);
        expect(afterGenerate.payloads.at(-1)).toEqual(payload);
        expect(snapshot().media).toEqual(before.media);
        const image = page.locator("[data-logo-image]");
        await expect(image).toBeVisible();
        await expect
          .poll(() =>
            image.evaluate(
              (el: HTMLImageElement) => el.complete && el.naturalWidth,
            ),
          )
          .toBe(1024);
        const pixel = await image.evaluate((el: HTMLImageElement) => {
          const c = document.createElement("canvas");
          c.width = 32;
          c.height = 32;
          const ctx = c.getContext("2d")!;
          ctx.drawImage(el, 0, 0);
          return [...ctx.getImageData(16, 16, 1, 1).data];
        });
        expect(pixel).toEqual([220, 40, 80, 255]);
        const imageURL = await image.getAttribute("src");
        const privateImage = await page.request.get(imageURL!);
        expect(privateImage.headers()["content-type"]).toBe("image/png");
        expect(privateImage.headers()["cache-control"]).toContain("no-store");
        expect(privateImage.headers()["cache-control"]).toContain("private");
        expect(privateImage.headers()["x-content-type-options"]).toBe("nosniff");
        const anonymous = await browser.newContext();
        try {
          const r = await anonymous.request.get(
            new URL(imageURL!, page.url()).href,
          );
          expect(r.headers()["content-type"] ?? "").not.toContain("image/png");
        } finally {
          await anonymous.close();
        }
        await expect(page.locator("[data-logo-public-note]")).toBeVisible();
        await page.screenshot({
          path: info.outputPath("logo-candidate.png"),
          fullPage: true,
        });
        await page
          .locator("[data-logo-candidate] select[name=slot]")
          .selectOption("site.logo");
        await page.locator("[data-logo-candidate] button[type=submit]").click();
        await expect(page.locator("[data-logo-error]")).toHaveCount(0);
        const after = snapshot();
        expect(after.content).toEqual(before.content);
        expect(after.menus).toEqual(before.menus);
        expect(after.items).toEqual(before.items);
        const values = (rows: any[]) =>
          Object.fromEntries(rows.map((r) => [r.key, r.value_json]));
        const live = values(after.settings);
        const baseline = values(before.settings);
        for (const key of [
          "site.title",
          "site.logo",
          "site.logo_light",
          "site.logo_dark",
          "site.icon",
          "theme.default.cta_label",
          "theme.default.cta_url",
        ])
          expect(live[key]).toBe(baseline[key]);
        const draft = JSON.parse(live["theme.default.identity_draft"]);
        expect(draft.snapshot.site["site.logo"]).toBeTruthy();
        const oldDraft = JSON.parse(baseline["theme.default.identity_draft"]);
        expect(draft.snapshot.values).toEqual(oldDraft.snapshot.values);
        expect(draft.snapshot.links).toEqual(oldDraft.snapshot.links);
        for (const key of ["site.title", "site.tagline"])
          expect(draft.snapshot.site[key]).toBe(oldDraft.snapshot.site[key]);
        expect(after.media.length).toBe(before.media.length + 1);
      } finally {
        // This fixture owns a fresh disposable site. Restore settings between cases;
        // media files remain confined to its temp directory, removed by global teardown.
        const d = db();
        try {
          d.exec("BEGIN");
          d.exec("DELETE FROM settings");
          const insert = d.prepare(
            "INSERT INTO settings(key,value_json,updated_at) VALUES(?,?,?)",
          );
          for (const row of before.settings as any[])
            insert.run(row.key, row.value_json, row.updated_at);
          d.exec("DELETE FROM media");
          d.exec("COMMIT");
        } finally {
          d.close();
        }
      }
    });
  }

test("insufficient settings or upload permissions prevent generation", async ({
  page,
}) => {
  await login(page, "editor");
  const denied = await page.goto(route);
  expect(denied?.status()).toBe(403);
  await page.context().clearCookies();
  await login(page, "logo_settings");
  await page.goto(route);
  await expect(page.locator("[data-logo-brief] button")).toBeDisabled();
  const count = (await stats(page)).calls;
  const forbidden = await page.request.post(route, {
    form: {
      _csrf: await csrf(page),
      action: "review",
      prompt: "no permission",
      budget: "1",
      currency: "USD",
    },
  });
  expect(forbidden.status()).toBe(403);
  expect((await stats(page)).calls).toBe(count);
});

test("unknown image price fails closed without a provider call", async ({
  page,
}) => {
  await login(page);
  await page.goto(route);
  const count = (await stats(page)).calls;
  await configure(page, false);
  await review(page, "Unknown-price static logo");
  await expect(page.locator("[data-logo-error]")).toBeVisible();
  expect((await stats(page)).calls).toBe(count);
  await expect(page.locator("[data-logo-generate]")).toHaveCount(0);
});

test("concurrent draft edit rejects stale candidate selection and preserves human fields", async ({
  page,
}) => {
  await login(page);
  await page.goto(route);
  const before = snapshot();
  const count = (await stats(page)).calls;
  try {
    await configure(page);
    await review(page, "Synthetic stale candidate");
    await generate(page);
    expect((await stats(page)).calls).toBe(count + 1);
    const d = db();
    try {
      d.prepare(
        "INSERT INTO settings(key,value_json,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json,updated_at=excluded.updated_at",
      ).run(
        "theme.default.identity_draft",
        JSON.stringify({
          snapshot: {
            theme: "default",
            values: { identity_accent: "#112233" },
            site: { "site.title": "Human concurrent draft" },
            links: { cta_label: "Human CTA", cta_url: "/human" },
          },
          base: { "site.title": "Untouched live title" },
          saved_at: "2026-10-23",
          author: "human",
        }),
        "2026-10-23",
      );
    } finally {
      d.close();
    }
    const concurrent = snapshot();
    await page.locator("[data-logo-candidate] button[type=submit]").click();
    await expect(page.locator("[data-logo-error]")).toBeVisible();
    expect(snapshot().media).toEqual(concurrent.media);
    expect(
      snapshot().settings.find(
        (r: any) => r.key === "theme.default.identity_draft",
      ),
    ).toEqual(
      concurrent.settings.find(
        (r: any) => r.key === "theme.default.identity_draft",
      ),
    );
    expect((await stats(page)).calls).toBe(count + 1);
  } finally {
    const d = db();
    try {
      d.exec("BEGIN;DELETE FROM settings");
      const insert = d.prepare(
        "INSERT INTO settings(key,value_json,updated_at) VALUES(?,?,?)",
      );
      for (const row of before.settings as any[])
        insert.run(row.key, row.value_json, row.updated_at);
      d.exec("COMMIT");
    } finally {
      d.close();
    }
  }
});
