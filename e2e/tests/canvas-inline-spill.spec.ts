import { test, expect, type Page } from '@playwright/test';
import { randomUUID } from 'node:crypto';
import { csrf, db, setLocale, submitAndLand } from '../lib/helpers.ts';

// A long content source is shortened to fit the run context, never failed or
// silently cut: the review dialog says how much was left out and that an
// external agent can fetch the rest (get_canvas_source). Seeded straight into
// the fixture database; nothing runs.

const id = () => randomUUID().replaceAll('-', '').slice(0, 26).toUpperCase();

async function agentKey(page: Page): Promise<string> {
 await page.goto('/admin/agents/keys');await setLocale(page,'en');
 await page.locator('#key-create > summary').click();await page.locator('[data-key-permissions] > summary').click();
 const name='Spill reader '+randomUUID().slice(0,8);await page.locator('#k-name').fill(name);await page.locator('input[name=all]').check();
 await submitAndLand(page,page.locator('form[action="/admin/agents/keys"] button[type=submit]'),'/admin/agents/keys',201);
 const database=db();const key=(database.prepare('SELECT id FROM api_keys WHERE name=?').get(name) as {id:string}).id;database.close();
 return key;
}

async function spillBoard(page: Page, keyID: string): Promise<{path:string,agent:string}> {
 const database=db();const owner=database.prepare('SELECT id FROM users WHERE email=?').get('admin@e2e.test') as {id:string};
 const contentID=id(),now=new Date().toISOString();
 const paragraph='Quarterly findings paragraph with enough words to read like a report. ';
 const body=Array.from({length:120},(_,n)=>`${n+1}. ${paragraph.repeat(4)}`).join('\n\n');
 expect(body.length).toBeGreaterThan(30000);
 database.prepare(`INSERT INTO content(id,type,slug,title,body,author_id,status,locale,created_at,updated_at) VALUES(?,'post',?,'Long quarterly report',?,?,'draft','en',?,?)`).run(contentID,'spill-'+contentID.toLowerCase(),body,owner.id,now,now);
 await page.goto('/admin/canvases');
 const created=await page.request.post('/admin/canvases',{form:{title:'Inline and spill',_csrf:await csrf(page)},maxRedirects:0});
 expect(created.status()).toBe(303);const path=created.headers().location;const board=path.split('/').pop()!;
 const source=randomUUID(),prompt=randomUUID(),agent=randomUUID();
 const cards=[
  {id:source,kind:'content',title:'Long quarterly report',content_id:contentID,body:'',x:24,y:24},
  {id:prompt,kind:'prompt',title:'Brief',body:'Summarize the report.',x:24,y:320},
  {id:agent,kind:'agent',title:'Reader',agent_id:keyID,body:'',x:400,y:160},
 ];
 database.prepare('UPDATE admin_canvases SET document_json=?,edges_json=? WHERE id=?').run(JSON.stringify(cards),JSON.stringify([{from:source,to:agent},{from:prompt,to:agent}]),board);
 database.close();
 await page.goto(path);
 const grant=await page.request.post(path+'/operations/grants',{headers:{'X-CSRF-Token':await csrf(page)},data:{agent_id:keyID,read:true,append:true,run:true,node_ids:[source,prompt]}});
 expect(grant.status()).toBe(200);
 return {path,agent};
}

test('a long source is shortened and the review dialog says so, in en and ar',async({page})=>{
 await page.setViewportSize({width:1440,height:1000});
 const key=await agentKey(page);const {path,agent}=await spillBoard(page,key);
 const expected={
  en:/^Shortened 1 source; [\d,]+ more characters are available to the agent on request\.$/,
  ar:/^مصادر مختصرة: 1؛ أحرف إضافية متاحة للوكيل عند الطلب: [\d,]+\.$/,
 } as const;
 for(const locale of ['en','ar'] as const){
  await page.goto(path);await setLocale(page,locale);
  const reviewed=page.waitForResponse(r=>r.request().method()==='POST'&&new URL(r.url()).pathname===path+'/operations/preview');
  await page.locator(`[data-card-id="${agent}"] .canvas-operation-button`).click();
  const preview=await(await reviewed).json();
  expect(preview.spill.sources).toBe(1);expect(preview.spill.on_request).toBe(true);
  expect(preview.context).toContain('## [ref:content/');expect(preview.context).toMatch(/\[… [\d,]+ more characters\. Full text: ref:content\/[0-9A-Z]{26}\]/);
  const review=page.locator('.canvas-review');await expect(review).toBeVisible();
  const line=review.locator('[data-review-spill]');await expect(line).toHaveText(expected[locale]);
  // Latin digits in every locale, and the count matches the server's summary.
  await expect(line).toContainText(new Intl.NumberFormat('en-US').format(preview.spill.characters));
  await review.getByRole('button').first().click();await expect(review).toBeHidden();
 }
});
