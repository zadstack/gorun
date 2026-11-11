import {test,expect} from '@playwright/test';

test('integrations settings remain readable within desktop and mobile page bounds',async({page},testInfo)=>{
 await page.goto('/admin/settings/integrations');
 await expect(page.locator('[data-integrations-live]')).toBeVisible();
 await expect(page.locator('body')).not.toContainText('admin.integrations.meta.');
 for(const width of [1440,390]) {
  await page.setViewportSize({width,height:900});
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth)).toBe(true);
  await page.screenshot({path:testInfo.outputPath(`integrations-${width}.png`),fullPage:true});
 }
});

function snapshot(pending:boolean,names=['alpha','beta'],revision='r1') {
 return `<div data-integrations-live>
 <div data-live-region="issues"></div><fieldset data-section-fieldset="mail"><input name="mail-host" aria-label="Mail host"></fieldset>
 <span data-live-message data-failed="Refresh failed" data-paused="Offline" hidden></span>
 <section data-live-region="inventory-workers" data-config-revision="${revision}" data-refresh-pending="${pending}">
 <div data-resource-table><input data-resource-search aria-label="Filter"><select data-resource-filter aria-label="State"><option value="">All</option><option value="candidate">Candidates</option></select><span data-resource-count></span>
 <table><tbody>${names.map(name=>`<tr data-search="${name}" data-bound="candidate"><td>${name}<details><summary>${name}</summary><form method="post" action="/admin/settings/integrations/cloudflare/workers/${name}/identify"><input name="origin" aria-label="${name} origin"><input name="manager_key" type="password" aria-label="${name} key"></form></details></td></tr>`).join('')}<tr data-resource-empty hidden><td>No matches</td></tr></tbody></table></div></section></div>`;
}

test('saved-result polling preserves drafts by worker identity and stops after completion',async({page})=>{
 await page.goto('/admin/settings/integrations?inv_page_workers=3');
 await page.clock.install();
 await page.setContent(snapshot(true));
 let reads=0;
 await page.route('**/admin/settings/integrations*',async route=>{
  expect(route.request().method()).toBe('GET');
  expect(new URL(route.request().url()).searchParams.get('inv_page_workers')).toBe('3');
  expect(route.request().headers()['hx-request']).toBe('true');
  reads++;await route.fulfill({contentType:'text/html',body:snapshot(false,['beta','alpha'])});
 });
 await page.addScriptTag({url:'/static/admin/integrations.js'});
 await page.getByLabel('Mail host').fill('unsaved.example');
 await page.getByText('alpha',{exact:true}).last().click();
 await page.getByLabel('alpha origin').fill('https://alpha.example.workers.dev');
 await page.getByLabel('alpha key').fill('synthetic-test-secret');
 await page.clock.runFor(2000);
 await expect(page.locator('[data-refresh-pending]')).toHaveAttribute('data-refresh-pending','false');
 await expect(page.getByLabel('Mail host')).toHaveValue('unsaved.example');
 await expect(page.getByLabel('alpha origin')).toHaveValue('https://alpha.example.workers.dev');
 await expect(page.getByLabel('alpha key')).toHaveValue('synthetic-test-secret');
 await expect(page.getByLabel('alpha key')).toBeFocused();
 await page.getByText('beta',{exact:true}).last().click();
 await expect(page.getByLabel('beta key')).toHaveValue('');
 await page.clock.runFor(15000);
 expect(reads).toBe(1);
});

test('offline polling pauses and transient failures back off without replacing drafts',async({page,context})=>{
 await page.goto('/admin/settings/integrations');
 await page.clock.install();
 await page.setContent(snapshot(true));
 let reads=0;
 await page.route('**/admin/settings/integrations*',async route=>{reads++;await route.fulfill({status:503,body:'unavailable'});});
 await page.addScriptTag({url:'/static/admin/integrations.js'});
 await page.getByLabel('Mail host').fill('keep-this-draft');
 await context.setOffline(true);
 await page.clock.runFor(10000);
 expect(reads).toBe(0);
 await context.setOffline(false);
 await page.clock.runFor(1);
 await expect.poll(()=>reads).toBe(1);
 await page.clock.runFor(2000);
 expect(reads).toBe(1);
 await page.clock.runFor(2000);
 await expect.poll(()=>reads).toBe(2);
 await expect(page.getByLabel('Mail host')).toHaveValue('keep-this-draft');
});


test('a changed account revision does not carry a worker manager key into new results',async({page})=>{
 await page.goto('/admin/settings/integrations');
 await page.clock.install();
 await page.setContent(snapshot(true,['alpha'],'r1'));
 await page.route('**/admin/settings/integrations*',route=>route.fulfill({contentType:'text/html',body:snapshot(false,['alpha'],'r2')}));
 await page.addScriptTag({url:'/static/admin/integrations.js'});
 await page.getByText('alpha',{exact:true}).last().click();
 await page.getByLabel('alpha key').fill('synthetic-old-account-secret');
 await page.clock.runFor(2000);
 await expect(page.locator('[data-config-revision]')).toHaveAttribute('data-config-revision','r2');
 await expect(page.getByLabel('alpha key')).toHaveValue('');
});


test('removed inventory cannot leave obsolete actions and unavailable forms keep drafts',async({page})=>{
 await page.goto('/admin/settings/integrations');
 await page.clock.install();
 await page.setContent(snapshot(true));
 await page.route('**/admin/settings/integrations*',route=>route.fulfill({contentType:'text/html',body:'<div data-integrations-live><div data-live-region="issues">Section unavailable</div><fieldset data-section-fieldset="mail" disabled></fieldset></div>'}));
 await page.addScriptTag({url:'/static/admin/integrations.js'});
 await page.getByLabel('Mail host').fill('keep-unsubmitted-value');
 await page.clock.runFor(2000);
 await expect(page.locator('[data-live-region="inventory-workers"]')).toHaveCount(0);
 await expect(page.locator('[data-live-region="issues"]')).toHaveText('Section unavailable');
 await expect(page.getByLabel('Mail host')).toHaveValue('keep-unsubmitted-value');
 await expect(page.getByLabel('Mail host')).toBeDisabled();
});

test('a data-confirm action asks once in the accessible modal, which traps Tab and returns focus',async({page})=>{
 // The page only renders its confirm forms when Cloudflare or storage is configured, so the
 // real page is served with one such form added before integrations.js binds.
 await page.route('**/admin/settings/integrations',async route=>{
  const response=await route.fetch();const html=(await response.text()).replace('<div class="integrations space-y-6" data-integrations-live>','<div class="integrations space-y-6" data-integrations-live><form method="post" action="/admin/settings/integrations/cloudflare/clear" data-confirm="Clear the cache?"><button type="submit" data-e2e-confirm-opener>Clear</button></form>');
  await route.fulfill({response,body:html});
 });
 let nativeDialogs=0;page.on('dialog',dialog=>{nativeDialogs++;void dialog.dismiss();});
 await page.goto('/admin/settings/integrations');
 const opener=page.locator('[data-e2e-confirm-opener]'),modal=page.locator('[role="dialog"][aria-labelledby="gp-confirm-title"]');
 await opener.focus();await page.keyboard.press('Enter');
 await expect(modal).toBeVisible();await expect(modal).toContainText('Clear the cache?');
 expect(nativeDialogs).toBe(0);
 const confirm=modal.locator('button[\\@click="$store.confirm.yes()"]'),cancel=modal.locator('button[\\@click="$store.confirm.close()"]');
 await expect(confirm).toBeFocused();
 for(const [key,target] of [['Tab',cancel],['Tab',confirm],['Shift+Tab',cancel],['Shift+Tab',confirm]] as const){await page.keyboard.press(key);await expect(target).toBeFocused();}
 await page.keyboard.press('Escape');
 await expect(modal).toBeHidden();await expect(opener).toBeFocused();
 await page.keyboard.press('Enter');await expect(modal).toBeVisible();await cancel.click();
 await expect(modal).toBeHidden();await expect(opener).toBeFocused();
 expect(nativeDialogs).toBe(0);
});
