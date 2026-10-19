// Opt-in browser acceptance. Credentials only in memory/private file, never argv.
// No traces, HAR, video, screenshots, storage-state files or request dumps.
import {chromium} from '@playwright/test';
import assert from 'node:assert/strict';
import {readFile,stat} from 'node:fs/promises';
import {pathToFileURL} from 'node:url';
export async function browserAcceptance(base,key,recover) {
 const origin=new URL(base).origin,browser=await chromium.launch({headless:true});
 const context=await browser.newContext({viewport:{width:1280,height:900}}),page=await context.newPage();
 const secrets=[key],slug='owner-check-'+Date.now();
 async function clickNavigation(locator){await Promise.all([page.waitForNavigation(),locator.click()]);}
 try {
  await page.goto(origin+'/admin/login');await page.locator('input[name=key]').fill(key);const loginResponse=page.waitForResponse(r=>new URL(r.url()).pathname==='/admin/login'&&r.request().method()==='POST');await clickNavigation(page.getByRole('button',{name:'Sign in',exact:true}));const loginResult=await loginResponse;const loginStatus=loginResult.status();assert.equal(loginResult.request().headers()['origin'],origin);if(loginStatus!==303)throw Error('Owner login POST status '+loginStatus+'; visible heading: '+await page.locator('h1').innerText());await page.getByRole('heading',{name:'Content',exact:true}).waitFor();
  const session=(await context.cookies()).find(v=>v.name.endsWith('edge_session'));assert.ok(session?.httpOnly);assert.equal(session.secure,origin.startsWith('https:'));assert.equal(session.sameSite,'Strict');
  await clickNavigation(page.getByRole('link',{name:'New content',exact:true}));
  await page.locator('input[name=title]').fill('Synthetic owner acceptance');await page.locator('input[name=slug]').fill(slug);await page.locator('input[name=excerpt]').fill('Synthetic test data only');await page.locator('textarea[name=body_text]').fill('<script>throw new Error("must remain text")</script>\nFirst saved body');
  await clickNavigation(page.getByRole('button',{name:'Save draft',exact:true}));const editURL=page.url();assert.equal((await context.request.get(origin+'/post/'+slug)).status(),404);
  const stale=await context.newPage();await stale.goto(editURL);
  await page.locator('textarea[name=body_text]').fill('Final reviewed body');await clickNavigation(page.getByRole('button',{name:'Save draft',exact:true}));
  const bad=stale.waitForResponse(r=>r.url().includes('/admin/save')&&r.request().method()==='POST');await stale.getByRole('button',{name:'Save draft',exact:true}).click();assert.equal((await bad).status(),409);await stale.close();
  await clickNavigation(page.getByRole('button',{name:'Review publication',exact:true}));assert.ok(await page.getByText('Final reviewed body',{exact:true}).isVisible());await page.getByRole('checkbox').check();await clickNavigation(page.getByRole('button',{name:'Publish',exact:true}));
  const publicPage=await context.request.get(origin+'/post/'+slug);assert.equal(publicPage.status(),200);assert.match(await publicPage.text(),/Final reviewed body/);
  await page.goto(editURL);await page.locator('textarea[name=body_text]').fill('Private next draft');await clickNavigation(page.getByRole('button',{name:'Save draft',exact:true}));assert.ok(!(await(await context.request.get(origin+'/post/'+slug)).text()).includes('Private next draft'));
  const exported=await(await context.request.get(origin+'/admin/export')).text();for(const value of ['key_hash','token_hash','EDGE_ADMIN_SECRET',key])assert.ok(!exported.includes(value));
  const mobile=await browser.newContext({viewport:{width:390,height:844},storageState:await context.storageState()});const m=await mobile.newPage();await m.goto(editURL.replace('lang=en','lang=ar'));await m.getByRole('heading',{name:'تحرير المحتوى',exact:true}).waitFor();assert.equal(await m.locator('html').getAttribute('dir'),'rtl');await m.getByRole('button',{name:'مراجعة النشر',exact:true}).click();await m.getByRole('heading',{name:'مراجعة النشر',exact:true}).waitFor();assert.ok(await m.getByRole('button',{name:'نشر',exact:true}).isVisible());await mobile.close();
  await page.goto(editURL);await page.locator('form[action*="/admin/unpublish"] input[name=confirm]').check();await clickNavigation(page.locator('form[action*="/admin/unpublish"] button'));assert.equal((await context.request.get(origin+'/post/'+slug)).status(),404);
  if(recover) {
   const newKey=await recover();secrets.push(newKey);const oldCookieContext=await browser.newContext({storageState:await context.storageState()});const old=await oldCookieContext.newPage();await old.goto(origin+'/admin');assert.ok(old.url().includes('/admin/login'));await oldCookieContext.close();
   await page.goto(origin+'/admin/login');await page.locator('input[name=key]').fill(key);const oldResponse=page.waitForResponse(r=>r.url().includes('/admin/login')&&r.request().method()==='POST');await page.getByRole('button',{name:'Sign in',exact:true}).click();assert.equal((await oldResponse).status(),401);
   await page.locator('input[name=key]').fill(newKey);await clickNavigation(page.getByRole('button',{name:'Sign in',exact:true}));await page.getByRole('heading',{name:'Content',exact:true}).waitFor();
  }
  await clickNavigation(page.getByRole('button',{name:'Sign out',exact:true}));assert.ok(page.url().includes('/admin/login'));
  console.log('Browser acceptance passed: owner login, private draft, stale save, exact review/publish, EN+AR mobile, export isolation, unpublish, logout'+(recover?', offline recovery/session revocation':''));
 } catch(error) {let message=String(error?.message??'Browser acceptance failed');for(const secret of secrets)message=message.replaceAll(secret,'[redacted]');message=message.replace(/[A-Za-z0-9_-]{43,}/g,'[redacted]');throw Error(message);} finally {await context.close();await browser.close();}
}
if(process.argv[1]&&import.meta.url===pathToFileURL(process.argv[1]).href) {
 const url=process.env.EDGE_ADMIN_TEST_URL,file=process.env.EDGE_ADMIN_TEST_KEY_FILE;
 if(!url||!file)throw Error('Opt-in requires EDGE_ADMIN_TEST_URL and private EDGE_ADMIN_TEST_KEY_FILE. No token in arguments.');
 if((await stat(file)).mode&0o077)throw Error('Key file must be private (mode0600)');
 const key=(await readFile(file,'utf8')).trim();if(!/^[A-Za-z0-9_-]{43}$/.test(key))throw Error('Invalid owner key file');
 await browserAcceptance(url,key);
}
