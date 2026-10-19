import {test,expect} from '@playwright/test';
import {randomUUID} from 'node:crypto';
import {ADMIN,csrf,db,setLocale} from '../lib/helpers.ts';

test('owned canvas actions appear in opposite-side agents drawer without dispatch and respect locale, focus and mobile',async({page,browser},testInfo)=>{
 const database=db(),saved=database.prepare("SELECT key,value_json,updated_at FROM settings WHERE key LIKE 'ai.%'").all() as any[];
 let path='';
 try {
  await page.goto('/admin/settings/ai');await setLocale(page,'en');
  const configured=await page.request.post('/admin/settings/ai',{form:{_csrf:await csrf(page),edit_token:await page.locator('input[name=edit_token]').inputValue(),provider:'openai',base_url:'http://127.0.0.1:9/v1',model:'saved-dock-model'},maxRedirects:0});expect(configured.status()).toBe(303);
  await page.goto('/admin/canvases');await page.locator('#new-canvas-title').fill('Editorial workspace');await page.locator('form[action="/admin/canvases"] [type=submit]').click();
  path=new URL(page.url()).pathname;
  const prompt=randomUUID(),writer=randomUUID(),external=randomUUID();
  const body='Use only the approved brief. Keep the output private for review.';
  expect((await page.request.post(path+'/document',{headers:{'X-CSRF-Token':await csrf(page)},data:{version:1,title:'Editorial workspace',cards:[{id:prompt,kind:'prompt',body,x:24,y:24},{id:writer,kind:'provider',title:'Editorial writer',provider_id:'native-text',generation_kind:'text',x:420,y:24},{id:external,kind:'agent',title:'External reviewer',x:840,y:24}],edges:[{from:prompt,to:writer}]}})).status()).toBe(200);
  await page.goto('/admin');
  const root=page.locator('[data-agents-dock]'),launcher=root.locator('[data-dock-open]'),dialog=root.locator('dialog');
  await expect(launcher).toBeVisible();
  expect(await root.locator('.agents-dock-rail').evaluate(el=>Math.round(el.getBoundingClientRect().right))).toBe(await page.evaluate(()=>innerWidth));
  expect(await page.locator('#main').evaluate(el=>el.getBoundingClientRect().right)).toBeLessThanOrEqual(await root.locator('.agents-dock-rail').evaluate(el=>el.getBoundingClientRect().left));
  await launcher.click();await expect(dialog).toBeVisible();
  await dialog.getByRole('button',{name:/Editorial writer/}).click();
  const integrations=dialog.getByRole('tab',{name:'Integrations',exact:true}),instructions=dialog.getByRole('tab',{name:'Instructions',exact:true}),defaults=dialog.getByRole('tab',{name:'Defaults',exact:true});
  await expect(integrations).toHaveAttribute('aria-selected','true');await expect(dialog.getByRole('tabpanel',{name:'Integrations',exact:true})).toBeVisible();
  await integrations.focus();await page.keyboard.press('ArrowRight');await expect(instructions).toBeFocused();await expect(instructions).toHaveAttribute('aria-selected','true');
  await expect(dialog.getByRole('tabpanel',{name:'Instructions',exact:true})).toContainText(body);
  await page.keyboard.press('End');await expect(defaults).toBeFocused();await expect(dialog.getByRole('tabpanel',{name:'Defaults',exact:true})).toContainText(writer);
  await page.keyboard.press('Home');await expect(integrations).toBeFocused();
  await expect(dialog.locator('[data-dock-detail]')).toContainText('saved-dock-model');
  await expect(dialog.locator('[data-dock-detail]')).toContainText('Configured · review before running');
  await expect(dialog.locator('[data-dock-detail]')).toContainText(writer);
  await page.keyboard.press('Tab');
  expect(await page.evaluate(()=>document.querySelector('#agents-dock-dialog')!.contains(document.activeElement))).toBe(true);
  await page.screenshot({path:testInfo.outputPath('agents-drawer-en.png'),animations:'disabled'});
  await page.keyboard.press('Escape');await expect(dialog).not.toBeVisible();await expect(launcher).toBeFocused();
  await launcher.click();await dialog.getByRole('button',{name:/External reviewer/}).click();await dialog.getByRole('tab',{name:'Integrations',exact:true}).click();await expect(dialog.locator('[data-dock-detail]')).toContainText('An external client must be connected');await expect(dialog.locator('[data-dock-detail]')).toContainText('Setup needed');await expect(dialog.locator('[data-dock-detail]')).not.toContainText('saved-dock-model');
  await dialog.locator('[data-dock-close]').click();
  await setLocale(page,'ar');await expect(launcher).toHaveAttribute('aria-label','الوكلاء');
  expect(await root.locator('.agents-dock-rail').evaluate(el=>Math.round(el.getBoundingClientRect().left))).toBe(0);
  expect(await page.locator('#main').evaluate(el=>el.getBoundingClientRect().left)).toBeGreaterThanOrEqual(await root.locator('.agents-dock-rail').evaluate(el=>el.getBoundingClientRect().right));
  await launcher.click();expect(await dialog.evaluate(el=>Math.round(el.getBoundingClientRect().left))).toBe(0);await dialog.locator('[data-dock-close]').click();
  await page.setViewportSize({width:390,height:844});
  const menu=page.locator('[data-sidebar-toggle]');await menu.click();await expect(menu).toHaveAttribute('aria-expanded','true');
  await launcher.click();await expect(menu).toHaveAttribute('aria-expanded','false');await expect(dialog).toBeVisible();
  await page.keyboard.press('Tab');expect(await page.evaluate(()=>document.querySelector('#agents-dock-dialog')!.contains(document.activeElement))).toBe(true);
  await dialog.getByRole('button',{name:/Editorial writer/}).click();
  await dialog.getByRole('tab',{name:'التكاملات',exact:true}).focus();await page.keyboard.press('ArrowLeft');await expect(dialog.getByRole('tab',{name:'التعليمات',exact:true})).toBeFocused();await expect(dialog.getByRole('tabpanel',{name:'التعليمات',exact:true})).toBeVisible();
  expect(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth)).toBe(false);
  await expect(dialog).toContainText('التعليمات المحفوظة الفعلية');
  await page.screenshot({path:testInfo.outputPath('agents-drawer-ar-mobile.png'),animations:'disabled'});
  await dialog.getByRole('link',{name:'افتح الكانفس وراجع المهمة'}).click();
  await expect(page).toHaveURL(new RegExp(writer+'$'));
  await expect(page.locator(`[data-card-id="${writer}"]`)).toBeInViewport();
  expect((database.prepare('SELECT count(*) n FROM admin_canvas_runs').get() as any).n).toBe(0);
  const owner=database.prepare('SELECT password_hash FROM users WHERE email=?').get(ADMIN.email) as any;
  const otherID=randomUUID(),email=`dock-${otherID}@e2e.test`,now=new Date().toISOString();
  database.prepare("INSERT INTO users(id,email,name,password_hash,role,locale,created_at,updated_at) VALUES(?,?,?,?,'editor','en',?,?)").run(otherID,email,'Other editor',owner.password_hash,now,now);
  const isolated=await browser.newContext({baseURL:new URL(page.url()).origin,storageState:{cookies:[],origins:[]}});
  try {const other=await isolated.newPage();await other.goto('/admin/login');await other.locator('[name=email]').fill(email);await other.locator('[name=password]').fill(ADMIN.password);await other.locator('form[action^="/admin/login"] [type=submit]').click();await expect(other).toHaveURL(/\/admin\/?$/);const response=await other.request.get('/admin/x/agents-dock');expect(response.status()).toBe(200);expect(await response.text()).not.toContain(body);await other.locator('[data-dock-open]').click();await expect(other.locator('[data-dock-list]')).not.toContainText('Editorial writer');}finally{await isolated.close();database.prepare('DELETE FROM users WHERE id=?').run(otherID);}
 } finally {
  database.prepare("DELETE FROM settings WHERE key LIKE 'ai.%'").run();for(const row of saved)database.prepare('INSERT INTO settings(key,value_json,updated_at) VALUES(?,?,?)').run(row.key,row.value_json,row.updated_at);
  if(path)await page.request.delete(path,{headers:{'X-CSRF-Token':await page.locator('meta[name=csrf-token]').getAttribute('content')||''}});
  database.close();
 }
});

test('closing or leaving the agents drawer discards delayed responses and renders names as text',async({page})=>{
 let held:any,delay=false;
 const item={id:'22222222-2222-4222-8222-222222222222',board:'Synthetic board',title:'<img src=x onerror=alert(1)>',kind:'agent',href:'/admin/canvases/01J00000000000000000000001#card-22222222-2222-4222-8222-222222222222',mode:'external',configured:false};
 await page.route('**/admin/x/agents-dock',async route=>{if(delay){held=route;return;}await route.fulfill({json:{available:true,agents:[item]}});});
 await page.goto('/admin');const root=page.locator('[data-agents-dock]'),dialog=root.locator('dialog'),launcher=root.locator('[data-dock-open]');await expect(launcher).toBeVisible();
 delay=true;await launcher.click();await expect.poll(()=>!!held).toBe(true);await dialog.locator('[data-dock-close]').click();const closedResponse=page.waitForResponse('**/admin/x/agents-dock');await held.fulfill({json:{available:true,agents:[{...item,title:'STALE_PRIVATE_TITLE'}]}});await(await closedResponse).finished();await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));await expect(dialog).not.toBeVisible();await expect(root).not.toContainText('STALE_PRIVATE_TITLE');
 delay=false;await launcher.click();await dialog.getByRole('button',{name:/<img src=x/}).click();await expect(dialog.locator('[data-dock-detail] img')).toHaveCount(0);await expect(dialog.locator('[data-dock-detail]')).toContainText('<img src=x onerror=alert(1)>');await dialog.locator('[data-dock-close]').click();
 held=undefined;delay=true;await launcher.click();await expect.poll(()=>!!held).toBe(true);await page.evaluate(()=>window.dispatchEvent(new PageTransitionEvent('pagehide')));const leftResponse=page.waitForResponse('**/admin/x/agents-dock');await held.fulfill({json:{available:true,agents:[{...item,title:'LATE_AFTER_PAGEHIDE'}]}});await(await leftResponse).finished();await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));await expect(root).toBeHidden();await expect(dialog).not.toBeVisible();await expect(root).not.toContainText('LATE_AFTER_PAGEHIDE');
});
