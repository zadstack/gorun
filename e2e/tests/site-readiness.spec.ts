import {test,expect} from '@playwright/test';
import {execFileSync} from 'node:child_process';
import {resolve} from 'node:path';
import {db,env,setLocale,expectNoServerError,submitAndLand} from '../lib/helpers.ts';

const route='/admin/appearance/readiness';

test('readiness gives actionable local evidence, updates after corrections and stays read-only',async({page,browser},testInfo)=>{
 const database=db(), cli=resolve('.bin/gorun');
 const keys=['site.title','site.tagline','site.locale','site.timezone','site.base_url','site.front_page','site.theme','site.logo','site.posts_per_page','contact.email','mail.host','mail.port','mail.username','mail.from','mail.from_name','mail.tls'];
 const saved=keys.map(key=>({key,value:(database.prepare('SELECT value_json FROM settings WHERE key=?').get(key) as any)?.value_json}));
 const set=(key:string,value:unknown)=>execFileSync(cli,['settings','set',key,JSON.stringify(value),'--data',env('E2E_DATA')],{stdio:'pipe'});
 const snapshot=()=>JSON.stringify({settings:database.prepare('SELECT key,value_json FROM settings ORDER BY key').all(),content:database.prepare('SELECT id,title,status,body,updated_at FROM content ORDER BY id').all(),layouts:database.prepare('SELECT * FROM builder_documents ORDER BY content_id').all()});
 const check=(id:string)=>page.locator(`[data-readiness-check="${id}"]`);
 const errors:string[]=[];page.on('pageerror',error=>errors.push(error.message));
 try{
  execFileSync(cli,['settings','unset','contact.email','--data',env('E2E_DATA')],{stdio:'pipe'});
  execFileSync(cli,['settings','unset','site.front_page','--data',env('E2E_DATA')],{stdio:'pipe'});
  execFileSync(cli,['settings','unset','mail.host','--data',env('E2E_DATA')],{stdio:'pipe'});
  await page.goto('/admin');await setLocale(page,'en');
  const before=snapshot();
  const response=await page.goto(route);expect(response?.status()).toBe(200);expect(response?.headers()['cache-control']).toContain('no-store');
  await expectNoServerError(page);await expect(page.getByRole('heading',{level:1})).toHaveText('Site readiness');
  await expect(check('contact_email')).toHaveAttribute('data-status','warn');
  await expect(check('contact_email')).toContainText('may fall back to an administrator');
  await expect(check('smtp_config')).toHaveAttribute('data-status','warn');
  await expect(check('smtp_delivery')).toHaveAttribute('data-status','unknown');
  await expect(check('smtp_delivery')).toContainText('No message sent');
  await expect(check('mail_inbox')).toHaveAttribute('data-status','unknown');
  await expect(check('seo')).toHaveAttribute('data-status','warn');
  await expect(check('base_url')).toHaveAttribute('data-status','warn');
  expect(snapshot()).toBe(before);
  const fixURLs=await page.locator('[data-readiness-check] a').evaluateAll(nodes=>nodes.map(node=>(node as HTMLAnchorElement).getAttribute('href')!));
  for(const url of new Set(fixURLs)){const target=await page.request.get(url);expect(target.status(),`readiness fix target ${url}`).toBe(200)}
  expect(snapshot()).toBe(before);
  await page.goto('/admin/content/page/new');await page.fill('#title','Readiness real home');await page.fill('#slug','readiness-real-home');
  await page.locator('[data-field-key="body"] .ql-editor').fill('Our independent studio designs websites for local organizations.');
  await submitAndLand(page,page.locator('button[name="intent"][value="publish"]'),'/admin/content/page');
  await page.goto(route);await check('contact_email').getByRole('link').click();await expect(page).toHaveURL(/\/admin\/settings\/general$/);
  await page.fill('#s-title','Readiness Studio');await page.fill('#s-contact-email','readiness-owner@e2e.test');
  await page.fill('#s-front-page','Readiness real home');await page.locator('#s-front-page-options button[data-id]').filter({hasText:'Readiness real home'}).first().click();
  await submitAndLand(page,page.locator('form[action="/admin/settings/general"] button[type="submit"]'),'/admin/settings/general');
  await page.goto(route);await expect(check('contact_email')).toHaveAttribute('data-status','pass');await expect(check('front')).toHaveAttribute('data-status','pass');await expect(check('seo')).toHaveAttribute('data-status','pass');
  await expect(check('seo')).toContainText('rendered metadata remains unverified');
  // Presence of SMTP configuration cannot turn local evidence into delivery proof.
  set('mail.host','smtp.unverified.e2e.test');
  await page.reload();await expect(check('smtp_config')).toHaveAttribute('data-status','pass');await expect(check('smtp_delivery')).toHaveAttribute('data-status','unknown');await expect(check('smtp_delivery')).toContainText('No message sent');
  await expect(check('smtp_delivery')).toContainText('Acceptance does not prove inbox receipt');
  await expect(check('mail_inbox')).toHaveAttribute('data-status','unknown');
  await expect(page.locator('#main')).not.toContainText('smtp.unverified.e2e.test');
  await expect(page.locator('#main')).not.toContainText('readiness-owner@e2e.test');
  await expect(page.locator('#main')).toContainText('not a production certification');
  await page.setViewportSize({width:1440,height:1000});await page.screenshot({path:testInfo.outputPath('readiness-en-desktop.png'),fullPage:true});
  for(const locale of ['ar','en'] as const){
   await setLocale(page,locale);await expect(page.locator('html')).toHaveAttribute('dir',locale==='ar'?'rtl':'ltr');
   for(const width of [320,390]){await page.setViewportSize({width,height:844});await page.reload();expect(await page.evaluate(()=>document.documentElement.scrollWidth<=document.documentElement.clientWidth),`readiness ${locale}/${width} overflows`).toBe(true);await page.screenshot({path:testInfo.outputPath(`readiness-${locale}-${width}.png`),fullPage:true})}
  }
  const anon=await browser.newContext({storageState:{cookies:[],origins:[]}});
  try{const response=await anon.request.get(route,{maxRedirects:0});expect(response.status()).toBe(303);expect(response.headers().location).toContain('/admin/login')}finally{await anon.close()}
  await page.setViewportSize({width:1280,height:900});await page.goto('/admin/users/new');
  await page.fill('#u-name','Readiness contributor');await page.fill('#u-email','readiness-contributor@e2e.test');await page.fill('#u-password','e2e-readiness-password-0001');await page.selectOption('#u-role','contributor');
  await page.locator('form[action="/admin/users"] button[type="submit"]').click();
  const restricted=await browser.newContext({storageState:{cookies:[],origins:[]}});
  try{const rp=await restricted.newPage();await rp.goto('/admin/login');await rp.fill('input[name="email"]','readiness-contributor@e2e.test');await rp.fill('input[name="password"]','e2e-readiness-password-0001');await rp.locator('form[action^="/admin/login"] button[type="submit"]').click();const denied=await rp.goto(route);expect(denied?.status()).toBe(403);await expect(rp.locator('[data-readiness-check]')).toHaveCount(0);const wizardDenied=await rp.goto('/admin/site-builder');expect(wizardDenied?.status()).toBe(403)}finally{await restricted.close()}
  expect(errors).toEqual([]);
 }finally{
  for(const item of saved){execFileSync(cli,item.value===undefined?['settings','unset',item.key,'--data',env('E2E_DATA')]:['settings','set',item.key,item.value,'--data',env('E2E_DATA')],{stdio:'pipe'})}
  await page.setViewportSize({width:1280,height:900});await page.goto('/admin');await setLocale(page,'ar');database.close();
 }
});
