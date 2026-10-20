import {test,expect} from '@playwright/test';
import {createServer} from 'node:http';
import {csrf,db,setLocale} from '../lib/helpers.ts';

const suggestion={suggestions:[{title:'Human-reviewed campaign',summary:'Prepare a private draft for review',cards:[{kind:'prompt',title:'Instructions',body:'Draft only. Never publish or send.',x:24,y:24},{kind:'agent',title:'Choose an executor',body:'',x:420,y:24}],edges:[{from:0,to:1}]}]};
test('AI canvas suggestion requires visible preview and separate approval, invalidates changed targets and applies one unbound private board',async({page},info)=>{
 const database=db(),previous=database.prepare("SELECT * FROM settings WHERE key LIKE 'ai.%'").all() as any[];
 let calls=0;const received:any[]=[];
 const model=createServer(async(req,res)=>{let raw='';for await(const b of req)raw+=b;received.push(JSON.parse(raw));calls++;res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify({choices:[{message:{role:'assistant',content:JSON.stringify(suggestion)}}]}));});
 await new Promise<void>(resolve=>model.listen(0,'127.0.0.1',resolve));const address=model.address() as {port:number};let board='';
 try{
  await page.goto('/admin/settings/ai');await setLocale(page,'en');expect((await page.request.post('/admin/settings/ai',{form:{_csrf:await csrf(page),edit_token:await page.locator('[name=edit_token]').inputValue(),provider:'openai',base_url:`http://127.0.0.1:${address.port}/v1`,model:'reviewed-flow-model'},maxRedirects:0})).status()).toBe(303);
  await page.goto('/admin/canvases');const root=page.locator('[data-canvas-ai-suggestions]');await expect(root).toBeVisible();
  await root.locator('[data-ai-refresh]').click();await expect(root.locator('[data-ai-review]')).toBeVisible();expect(calls).toBe(0);
  await expect(root.locator('[data-ai-preview]')).toContainText('reviewed-flow-model');await expect(root.locator('[data-ai-preview]')).toContainText('Site context JSON');await expect(root.locator('[data-ai-confirm]')).toBeDisabled();await page.screenshot({path:info.outputPath('review-en.png'),fullPage:true});
  await root.locator('[data-ai-consent]').check();await expect(root.locator('[data-ai-confirm]')).toBeEnabled();
  await root.locator('[data-ai-target]').selectOption('provider');await expect(root.locator('[data-ai-review]')).toBeHidden();await expect(root.locator('[data-ai-confirm]')).toBeDisabled();expect(calls).toBe(0);
  await root.locator('[data-ai-target]').selectOption('agent');await root.locator('[data-ai-refresh]').click();await expect(root.locator('[data-ai-review]')).toBeVisible();
  const preview=JSON.parse(await root.locator('[data-ai-preview]').innerText());await root.locator('[data-ai-consent]').check();await root.locator('[data-ai-confirm]').click();await expect(root.locator('[data-ai-grid] article')).toHaveCount(1);expect(calls).toBe(1);expect(received[0].messages[0].content).toBe(preview.system);expect(received[0].messages[1].content).toBe(preview.user);
  await page.reload();await expect(root.locator('[data-ai-grid] article')).toHaveCount(1);expect(calls).toBe(1);
  await root.locator('[data-ai-grid] article button').click();await expect(page).toHaveURL(/\/admin\/canvases\/[A-Z0-9]{26}$/);board=new URL(page.url()).pathname.split('/').pop()!;
  const saved=await(await page.request.get(`/admin/canvases/${board}/document`)).json();expect(saved.cards.find((c:any)=>c.kind==='agent').agent_id).toBeUndefined();expect(saved.cards.some((c:any)=>c.schedule_enabled||c.run_id||c.granted)).toBe(false);
  await page.goto('/admin/canvases');await setLocale(page,'ar');await page.setViewportSize({width:390,height:844});await root.locator('[data-ai-refresh]').click();await expect(root.locator('[data-ai-review]')).toBeVisible();await expect(root.locator('[data-ai-confirm]')).toBeDisabled();expect(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth)).toBe(false);expect(calls).toBe(1);await page.screenshot({path:info.outputPath('review-ar-mobile.png'),fullPage:true});
 }finally{if(board)database.prepare('DELETE FROM admin_canvases WHERE id=?').run(board);database.prepare("DELETE FROM settings WHERE key LIKE 'ai.%'").run();for(const row of previous)database.prepare('INSERT INTO settings(key,value_json,updated_at) VALUES(?,?,?)').run(row.key,row.value_json,row.updated_at);database.close();await new Promise<void>(resolve=>model.close(()=>resolve()));}
});

test('late preview after pagehide cannot restore a stale consent panel',async({page})=>{
 await page.route('**/admin/x/canvases/ai-suggestions',route=>route.fulfill({json:{configured:true}}));
 let release:()=>void=()=>{};const held=new Promise<void>(resolve=>release=resolve);let requested=false;
 await page.route('**/admin/x/canvases/ai-suggestions/preview',async route=>{requested=true;await held;await route.fulfill({json:{request_id:'01AAAAAAAAAAAAAAAAAAAAAAAA',target_kind:'agent',locale:'en',destination:{host:'model.example'},system:'OLD_PREVIEW',user:'OLD_CONTEXT',context:{},expires_at:'later'}});});
 try{await page.goto('/admin/canvases');await page.locator('[data-ai-refresh]').click();await expect.poll(()=>requested).toBe(true);await page.evaluate(()=>dispatchEvent(new PageTransitionEvent('pagehide',{persisted:true})));release();await page.waitForTimeout(100);await expect(page.locator('[data-ai-review]')).toBeHidden();await expect(page.locator('[data-ai-preview]')).toBeEmpty();await expect(page.locator('[data-ai-confirm]')).toBeDisabled();await expect(page.locator('[data-ai-refresh]')).toBeEnabled();}finally{release();await page.unrouteAll({behavior:'wait'});}
});
