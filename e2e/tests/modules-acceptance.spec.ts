import { test, expect, type Page } from '@playwright/test';
import { randomUUID } from 'node:crypto';
import { writeFileSync } from 'node:fs';
import { csrf, db, setLocale } from '../lib/helpers.ts';

const route = '/admin/system/modules';
const apiRoute = '/api/v1/system/modules';
type Entry = { id: string; state: { enabled: boolean; desired: boolean | null; optional: boolean; switchable: boolean; pending_restart: boolean; status: string } };
async function catalog(page: Page): Promise<Entry[]> {
  const response = await page.request.get(apiRoute);
  expect(response.status()).toBe(200);
  expect(response.headers()['cache-control']).toContain('no-store');
  return (await response.json()).data.entries;
}

test('modules catalog distinguishes running and next-boot state while preserving product data', async ({ page }, testInfo) => {
  const sql = db();
  const key = 'addons.commerce.enabled';
  const original = sql.prepare('SELECT * FROM settings WHERE key=?').get(key) as { key: string; value_json: string; updated_at: string } | undefined;
  const id = 'modules-browser-' + randomUUID();
  sql.prepare(`INSERT INTO products(id,slug,name,description,price_minor,currency,created_at,updated_at)
    VALUES(?,?,?,'<p>Synthetic preservation fixture.</p>',1234,'AED','','')`).run(id,id,'منتج تجريبي · Preservation fixture');
  const preserved = sql.prepare('SELECT * FROM products WHERE id=?').get(id);
  const observations: unknown[] = [];
  const errors: string[] = [];
  page.on('pageerror', error => errors.push(error.message));
  let originalLocale = 'ar';
  try {
    await page.goto(route);
    originalLocale = await page.locator('html').getAttribute('lang') ?? 'ar';
    const entries = await catalog(page);
    expect(entries.find(e => e.id === 'commerce')?.state).toMatchObject({enabled:true,desired:true,switchable:true,pending_restart:false});
    for (const locale of ['ar','en'] as const) {
      await setLocale(page,locale);
      for (const width of [1440,390]) {
        await page.setViewportSize({width,height:900});
        expect((await page.goto(route))?.status()).toBe(200);
        await expect(page.locator('html')).toHaveAttribute('dir',locale === 'ar' ? 'rtl' : 'ltr');
        await expect(page.getByRole('heading',{level:1})).toHaveText(locale === 'ar' ? 'الوحدات' : 'Modules');
        await expect(page.locator('[data-module]')).toHaveCount(entries.length);
        await expect(page.locator('.admin-modules a[href="/admin/extensions"]')).toHaveCount(entries.some(e => e.id === 'ext' && e.state.enabled) ? 1 : 0);
        for (const entry of entries) {
          const row = page.locator(`[data-module="${entry.id}"]`);
          await expect(row).toHaveAttribute('data-status',entry.state.status);
          if (!entry.state.optional) await expect(row.locator('form[action$="/activation"]')).toHaveCount(0);
        }
        await page.locator('[data-module="commerce"] details summary').click();
        await expect(page.locator('[data-module="commerce"] details')).toHaveAttribute('open','');
        await page.screenshot({path:testInfo.outputPath(`modules-${locale}-${width}.png`),fullPage:true});
        await page.locator('[data-module="commerce"]').screenshot({path:testInfo.outputPath(`commerce-registrations-${locale}-${width}.png`)});
        const bounds = await page.evaluate(() => ({viewport:innerWidth,document:document.documentElement.scrollWidth}));
        observations.push({locale,width,entries:entries.length,bounds});
        expect.soft(bounds.document, `${locale}/${width}: expanded registrations fit viewport`).toBeLessThanOrEqual(bounds.viewport + 1);
      }
    }
    await page.setViewportSize({width:1440,height:900});
    await page.goto(route);
    const row = page.locator('[data-module="commerce"]');
    await row.locator('form[action$="/activation"] button').click();
    await expect(row).toHaveAttribute('data-status','pending_disable');
    await expect(page.getByRole('status')).toContainText('restart');
    const disabled = (await catalog(page)).find(e => e.id === 'commerce')!;
    expect(disabled.state).toMatchObject({enabled:true,desired:false,pending_restart:true,status:'pending_disable'});
    expect(sql.prepare('SELECT * FROM products WHERE id=?').get(id)).toEqual(preserved);
    expect((await page.request.get('/shop')).status()).toBe(200);
    observations.push({action:'disable_next_boot',state:disabled.state,productUnchanged:true,shopStatus:200});
    await page.screenshot({path:testInfo.outputPath('modules-pending-disable.png'),fullPage:true});
    await row.locator('form[action$="/activation"] button').click();
    await expect(row).toHaveAttribute('data-status','enabled');
    const restored = (await catalog(page)).find(e => e.id === 'commerce')!;
    expect(restored.state).toMatchObject({enabled:true,desired:true,pending_restart:false,status:'enabled'});
    expect(sql.prepare('SELECT * FROM products WHERE id=?').get(id)).toEqual(preserved);
    observations.push({action:'enable_next_boot_without_restart',state:restored.state,productUnchanged:true});
    expect(errors).toEqual([]);
  } finally {
    try { await setLocale(page,originalLocale === 'en' ? 'en' : 'ar'); }
    finally {
      if (original) sql.prepare('INSERT INTO settings(key,value_json,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json,updated_at=excluded.updated_at').run(original.key,original.value_json,original.updated_at);
      else sql.prepare('DELETE FROM settings WHERE key=?').run(key);
      sql.prepare('DELETE FROM products WHERE id=?').run(id);
      expect(sql.prepare('SELECT * FROM settings WHERE key=?').get(key)).toEqual(original);
      sql.close();
      writeFileSync(testInfo.outputPath('catalog-evidence.json'),JSON.stringify({observations,pageErrors:errors,activationSettingRestored:true},null,2));
    }
  }
});

test('activation rejects missing CSRF and a health viewer without settings permission', async ({page,browser},testInfo) => {
  await page.goto(route);
  const before = (await catalog(page)).find(e => e.id === 'commerce')!;
  const path = '/admin/system/modules/commerce/activation';
  expect((await page.request.post(path,{form:{enabled:'false'},maxRedirects:0})).status()).toBe(403);
  const slug = 'module_viewer_' + randomUUID().slice(0,8);
  const email = slug + '@fixture.test';
  const password = 'synthetic-viewer-password-12345';
  const headers = {'X-CSRF-Token':await csrf(page)};
  let userID = '';
  let roleCreated = false;
  const context = await browser.newContext({storageState:{cookies:[],origins:[]}});
  const sql = db();
  const settingBefore = sql.prepare('SELECT * FROM settings WHERE key=?').get('addons.commerce.enabled');
  try {
    const role = await page.request.post('/api/v1/roles',{headers,data:{slug,name:{en:'Module health viewer',ar:'مشاهد الوحدات'},permissions:['system.health']}});
    expect(role.status()).toBe(201);
    roleCreated = true;
    const user = await page.request.post('/api/v1/users',{headers,data:{email,name:'Synthetic viewer',password,role:slug,locale:'en',status:'active'}});
    expect(user.status()).toBe(201);
    userID = (await user.json()).data.id;
    const viewer = await context.newPage();
    await viewer.goto('/admin/login');
    await viewer.locator('input[name="email"]').fill(email);
    await viewer.locator('input[name="password"]').fill(password);
    await viewer.locator('form[action^="/admin/login"] button[type="submit"]').click();
    await expect(viewer).not.toHaveURL(/\/admin\/login/);
    expect((await viewer.goto(route))?.status()).toBe(200);
    const exposedControls = await viewer.locator('form[action$="/activation"]').count();
    expect.soft(exposedControls, 'health-only reader must not be offered forbidden activation controls').toBe(0);
    await expect(viewer.locator('.admin-modules a[href="/admin/extensions"]')).toHaveCount(0);
    const token = await csrf(viewer);
    const ui = await viewer.request.post(path,{form:{enabled:'false',_csrf:token},maxRedirects:0});
    expect(ui.status()).toBe(403);
    const api = await viewer.request.put(apiRoute+'/commerce/activation',{headers:{'X-CSRF-Token':token},data:{enabled:false}});
    expect(api.status()).toBe(403);
    const alwaysOn = await page.request.put(apiRoute+'/auth/activation',{headers,data:{enabled:false}});
    expect(alwaysOn.status()).toBe(409);
    expect((await catalog(page)).find(e => e.id === 'commerce')?.state).toEqual(before.state);
    expect(sql.prepare('SELECT * FROM settings WHERE key=?').get('addons.commerce.enabled')).toEqual(settingBefore);
    await viewer.screenshot({path:testInfo.outputPath('modules-health-viewer.png'),fullPage:true});
    const removeHealth = await page.request.patch('/api/v1/roles/'+slug,{headers,data:{permissions:[]}});
    expect(removeHealth.status()).toBe(200);
    const noHealthPage = await viewer.request.get(route);
    const noHealthAPI = await viewer.request.get(apiRoute);
    expect(noHealthPage.status()).toBe(403);
    expect(noHealthAPI.status()).toBe(403);
    writeFileSync(testInfo.outputPath('permissions-evidence.json'),JSON.stringify({missingCSRF:403,viewerPage:200,viewerUI:ui.status(),viewerAPI:api.status(),alwaysOnAPI:alwaysOn.status(),exposedControls,noHealthPage:noHealthPage.status(),noHealthAPI:noHealthAPI.status(),stateUnchanged:true},null,2));
  } finally {
    sql.close();
    await context.close();
    if (userID) expect((await page.request.delete('/api/v1/users/'+userID,{headers})).status()).toBe(200);
    if (roleCreated) expect((await page.request.delete('/api/v1/roles/'+slug,{headers})).status()).toBe(200);
  }
});

// A second, explicitly requested process fixture persists membership=false
// before serve starts (global-setup); this test never restarts that process.
test('cold-disabled membership can request enable without claiming it is running', async ({page},testInfo) => {
  test.skip(process.env.E2E_MODULES_COLD_START !== 'membership', 'requires explicit cold-start membership fixture');
  const sql = db();
  const key = 'addons.membership.enabled';
  const original = sql.prepare('SELECT * FROM settings WHERE key=?').get(key) as {key:string;value_json:string;updated_at:string};
  try {
    await page.goto(route);
    const before = (await catalog(page)).find(e => e.id === 'membership')!;
    expect(before.state).toMatchObject({enabled:false,desired:false,switchable:true,pending_restart:false,status:'disabled'});
    const row = page.locator('[data-module="membership"]');
    await row.locator('form[action$="/activation"] button').click();
    await expect(row).toHaveAttribute('data-status','pending_enable');
    const pending = (await catalog(page)).find(e => e.id === 'membership')!;
    expect(pending.state).toMatchObject({enabled:false,desired:true,pending_restart:true,status:'pending_enable'});
    expect((await page.request.get('/membership')).status()).toBe(404);
    await page.screenshot({path:testInfo.outputPath('modules-pending-enable.png'),fullPage:true});
    await row.locator('form[action$="/activation"] button').click();
    const cancelled = (await catalog(page)).find(e => e.id === 'membership')!;
    expect(cancelled.state).toMatchObject({enabled:false,desired:false,pending_restart:false,status:'disabled'});
    writeFileSync(testInfo.outputPath('cold-start-evidence.json'),JSON.stringify({before:before.state,pending:pending.state,routeWhilePending:404,cancelled:cancelled.state,restarted:false},null,2));
  } finally {
    sql.prepare('INSERT INTO settings(key,value_json,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json,updated_at=excluded.updated_at').run(original.key,original.value_json,original.updated_at);
    expect(sql.prepare('SELECT * FROM settings WHERE key=?').get(key)).toEqual(original);
    sql.close();
  }
});
