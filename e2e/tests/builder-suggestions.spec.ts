import {expect, test} from "@playwright/test";
import {randomUUID} from "node:crypto";
import {csrf} from "../lib/helpers.ts";

test("layout suggestions preview and apply privately, preserve fields and undo", async ({page}) => {
  const errors: string[] = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto("/admin");
  const headers = {"X-CSRF-Token": await csrf(page)};
  const created = await page.request.post("/api/v1/content", {headers, data: {
    type:"page", locale:"ar", slug:`suggestions-${randomUUID().slice(0,8)}`,
    title:"E2E component suggestions", status:"published", body:"<p>Original public suggestion baseline</p>",
  }});
  expect(created.status()).toBe(201);
  const id = (await created.json()).data.id;
  try {
    await page.goto(`/admin/builder/${id}`);
    let state = JSON.parse((await page.locator("#page-builder").getAttribute("data-state"))!);
    const original = {version:1, sections:[{...state.defaults.services, id:"suggestion-services",
      title:"Keep this service title", text:"Keep this introduction",
      items:[{title:"Keep this service", text:"Keep these deliverables", link:"/ar/contact", image:"", image_alt:"", price:""}],
    }]};
    const saved = await page.request.post(`/admin/builder/${id}/save`, {headers, data:{
      document:original, base_updated_at:state.draft.updated_at || state.draft.base_updated_at,
    }});
    expect(saved.status()).toBe(200);
    await page.reload();
    state = JSON.parse((await page.locator("#page-builder").getAttribute("data-state"))!);
    const before = state.draft;
    const section = page.locator('[data-section-id="suggestion-services"]');
    const localResult = page.waitForResponse(response => response.url().endsWith(`/builder/${id}/suggest`) && response.request().postDataJSON().mode === "local");
    await section.getByRole("button", {name:/اقتراح تحسين|Suggest improvement/}).click();
    const local = await (await localResult).json();
    expect(local.source).toBe("local");
    expect(local.candidates.length).toBeGreaterThan(0);
    const panel = section.locator("[data-suggestions]");
    await expect(panel).toContainText(/اقتراحات محلية|Local suggestions/);
    if (!local.ai_destination) await expect(panel).toContainText(/غير متاح أو غير مُعدّ|unavailable or not configured/);
    else await expect(panel).toContainText(local.ai_destination);
    const publicBefore = await page.request.get(`/api/v1/content/${id}`);
    const originalPublicBody = (await publicBefore.json()).data.body;
    await panel.getByRole("button", {name:/معاينة الاقتراح|Preview suggestion/}).first().click();
    await expect(panel).toContainText(/معاينة للاقتراح فقط|Suggestion preview only/);
    const unchanged = await page.request.get(`/api/v1/builder/${id}`);
    expect((await unchanged.json()).data.document).toEqual(before.document);
    const writes: string[] = [];
    page.on("request", request => {
      if (request.method() === "POST" && /\/builder\/[^/]+\/(save|publish)$/.test(new URL(request.url()).pathname)) writes.push(request.url());
    });
    await page.clock.install();
    await panel.getByRole("button", {name:/تطبيق على المسودة غير المحفوظة|Apply to unsaved draft/}).first().click();
    await expect(page.locator("#builder-status")).toContainText(/المسودة غير المحفوظة|unsaved draft/);
    await expect(section.locator('input[data-field="title"]').first()).toHaveValue("Keep this service title");
    await page.clock.fastForward(1500); // crosses the normal 900ms autosave window deterministically
    expect(writes).toEqual([]);
    const stillPrivate = await page.request.get(`/api/v1/builder/${id}`);
    expect((await stillPrivate.json()).data.document).toEqual(before.document);
    await page.locator('[data-action="undo"]').click();
    await page.locator('[data-action="save"]').click();
    await expect(page.locator("#builder-status")).toContainText(/حُفظت|Draft saved/);
    const restored = await page.request.get(`/api/v1/builder/${id}`);
    expect((await restored.json()).data.document).toEqual(before.document);
    const publicAfter = await page.request.get(`/api/v1/content/${id}`);
    expect((await publicAfter.json()).data.body).toBe(originalPublicBody);
    expect(errors).toEqual([]);
  } finally {
    const removed = await page.request.delete(`/api/v1/content/${id}?hard=1`, {headers});
    expect(removed.ok()).toBeTruthy();
  }
});
