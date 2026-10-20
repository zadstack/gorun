import {test,expect} from '@playwright/test';
import {createServer} from 'node:http';
import {randomUUID} from 'node:crypto';
import {csrf,db,mcp,setLocale,submitAndLand,toolPayload} from '../lib/helpers.ts';

// The external-client fixture calls a real local scripted HTTP provider after
// the normal canvas consent. It is not a live research/model-quality test and
// does not bypass the SSRF rules for named managed connections.
test('Updates Researcher recipe EN/AR saves privately, then hands exact reviewed context to an external test client',async({page,request},info)=>{
 let calls=0;let received:any;
 const provider=createServer(async(req,res)=>{let body='';for await(const chunk of req)body+=chunk;received=JSON.parse(body);calls++;res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify({choices:[{message:{content:'Synthetic brief: source https://example.test/updates, publication 2026-10-20. Evidence is supplied; no live browsing verified.'}}]}));});
 await new Promise<void>(resolve=>provider.listen(0,'127.0.0.1',resolve));const port=(provider.address() as {port:number}).port;
 const database=db();let keyID='',keyToken='';const boards:string[]=[];
 const capture=async(locale:'en'|'ar',name:string)=>{
  await page.evaluate(()=>window.scrollTo(0,0));
  await page.screenshot({path:info.outputPath(name+'-'+locale+'.png'),fullPage:true});
  if(locale==='ar'){
   await page.setViewportSize({width:1440,height:1000});
   await page.evaluate(()=>window.scrollTo(0,0));
   await page.screenshot({path:info.outputPath(name+'-ar-desktop.png'),fullPage:true});
   await page.setViewportSize({width:390,height:844});
  }
 };
 try{
  await page.goto('/admin/agents/keys');await setLocale(page,'en');await page.locator('#key-create > summary').click();await page.locator('[data-key-permissions] > summary').click();const name='Atlas Researcher';await page.locator('#k-name').fill(name);await page.locator('input[name=all]').check();await submitAndLand(page,page.locator('form[action="/admin/agents/keys"] button[type=submit]'),'/admin/agents/keys',201);keyToken=await page.locator('#k-plaintext').inputValue();keyID=(database.prepare('SELECT id FROM api_keys WHERE name=?').get(name) as any).id;
  for(const locale of ['en','ar'] as const){
   await page.goto('/admin/agents/recipes');await setLocale(page,locale);await page.setViewportSize(locale==='en'?{width:1440,height:1000}:{width:390,height:844});
   await expect(page.locator('[data-agent-recipe]')).toContainText(locale==='en'?'Updates Researcher':'باحث المستجدات');await page.locator('#recipe-agent').selectOption(keyID);await page.locator('form[method=get] button').click();await expect(page.locator('[data-recipe-configuration]')).toContainText(locale==='en'?'External client':'عميل خارجي');
   await page.locator('#recipe-title').fill(locale==='en'?'Atlas Studio · Updates brief':'أطلس ستوديو · موجز المستجدات');await page.locator('#recipe-context').fill('Topic: release notes. Source https://example.test/updates, publication 2026-10-20. Supplied evidence only; uncertainty remains.');
   await page.locator('[data-recipe-form] button[value=preview]').click();await expect(page.locator('[data-recipe-preview]')).toBeVisible();expect(calls).toBe(boards.length);expect(await page.locator('#k-plaintext').count()).toBe(0);expect(await page.content()).not.toContain(keyToken);expect(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth)).toBe(false);await capture(locale,'recipe');
   const before=(database.prepare('SELECT count(*) n FROM admin_canvases').get() as any).n;
   await page.locator('[data-recipe-preview] input[name=reviewed]').check();await submitAndLand(page,page.locator('[data-recipe-form] button[value=save]'),'/admin/agents/recipes');await expect(page).toHaveURL(/\/admin\/canvases\/[A-Z0-9]{26}$/);
   const path=new URL(page.url()).pathname;boards.push(path.split('/').pop()!);expect((database.prepare('SELECT count(*) n FROM admin_canvases').get() as any).n).toBe(before+1);const board=await(await page.request.get(path+'/document')).json();const agent=board.cards.find((c:any)=>c.kind==='agent');expect(agent.agent_id).toBe(keyID);expect(board.cards.some((c:any)=>c.kind==='schedule')).toBe(false);expect((database.prepare('SELECT count(*) n FROM admin_canvas_grants WHERE board_id=?').get(boards.at(-1)!) as any).n).toBe(0);
   const post=async(suffix:string,data:any)=>page.request.post(path+suffix,{headers:{'X-CSRF-Token':await csrf(page)},data});
   expect((await post('/operations/preview',{version:board.version,node_id:agent.id})).status()).toBe(422);
   expect((await post('/operations/grants',{agent_id:keyID,read:true,append:true,run:true,node_ids:board.cards.filter((c:any)=>c.kind==='prompt'||c.kind==='note'&&c.id!==board.cards[3].id).map((c:any)=>c.id)})).status()).toBe(200);
   await page.reload();const card=page.locator(`[data-card-id="${agent.id}"]`);await card.locator('.canvas-operation-button').click();const review=page.locator('.canvas-review');await expect(review).toContainText('https://example.test/updates');const check=review.locator('input[type=checkbox]');await expect(review.getByRole('button').last()).toBeDisabled();await capture(locale,'recipe-review');await check.check();const pending=page.waitForResponse(r=>r.request().method()==='POST'&&new URL(r.url()).pathname===path+'/operations/runs',{timeout:15000});await review.getByRole('button').last().click();const queued=await(await pending).json();expect(queued.run.status).toBe('waiting_agent');expect(calls).toBe(boards.length-1);
   const args={board_id:boards.at(-1),run_id:queued.run.id};const ctx=toolPayload((await mcp(request,keyToken,'tools/call',{name:'canvases.context',arguments:args})).body.result).data;
   // This fixture is the separately running external client. Only now does it
   // choose to send the reviewed context to its scripted local HTTP provider.
   const result=await(await fetch(`http://127.0.0.1:${port}/v1/chat/completions`,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({model:'scripted-external-demo',messages:[{role:'system',content:ctx.prompt},{role:'user',content:ctx.context}]})})).json() as any;expect(received.messages[0].content).toBe(ctx.prompt);expect(received.messages[1].content).toBe(ctx.context);
   const appended=await mcp(request,keyToken,'tools/call',{name:'canvases.append_result',arguments:{...args,context_token:ctx.context_token,text:result.choices[0].message.content}});expect(appended.body.result?.isError).not.toBe(true);const after=await(await page.request.get(path+'/document')).json();expect(after.cards.some((c:any)=>c.kind==='note'&&c.body===result.choices[0].message.content)).toBe(true);await page.reload();await page.locator('[data-canvas-view=grid]').click();const resultCard=page.locator('[data-card-id]').filter({hasText:result.choices[0].message.content});await expect(resultCard).toHaveCount(1);await resultCard.scrollIntoViewIfNeeded();await capture(locale,'recipe-result');
   await page.goto('/admin/canvases');await expect(page.locator('body')).toContainText(locale==='en'?'Atlas Studio · Updates brief':'أطلس ستوديو · موجز المستجدات');
  }
  expect(calls).toBe(2);expect(database.prepare('SELECT value_json FROM settings WHERE key=?').get('agents.bundle.'+keyID)).toBeUndefined();
 }finally{for(const id of boards)database.prepare('DELETE FROM admin_canvases WHERE id=?').run(id);if(keyID)database.prepare('UPDATE api_keys SET revoked_at=? WHERE id=?').run(new Date().toISOString(),keyID);database.close();provider.closeAllConnections();await new Promise<void>(resolve=>provider.close(()=>resolve()));}
});

test('recipe inspects exact saved model and approved tool metadata without exposing connection secrets',async({page},info)=>{
 await page.goto('/admin/agents/keys');await setLocale(page,'en');await page.locator('#key-create > summary').click();await page.locator('[data-key-permissions] > summary').click();const name='Configured recipe agent '+randomUUID();await page.locator('#k-name').fill(name);await page.locator('input[name=all]').check();await submitAndLand(page,page.locator('form[action="/admin/agents/keys"] button[type=submit]'),'/admin/agents/keys',201);
 const database=db(),key=database.prepare('SELECT id,user_id FROM api_keys WHERE name=?').get(name) as any;
 const ids=['01J00000000000000000000701','01J00000000000000000000702','01J00000000000000000000703','01J00000000000000000000704','01J00000000000000000000705','01J00000000000000000000706'];
 const {createHash}=await import('node:crypto');const tool:any={connection_id:ids[2],connection_version:ids[3],name:'fetch_records',description:'Provider description, not a safety guarantee',schema:{type:'object',properties:{},additionalProperties:false},catalog_hash:'b'.repeat(64)};tool.alias='gorun_'+createHash('sha256').update([tool.connection_id,tool.connection_version,tool.name,tool.catalog_hash].join('\0')).digest('hex').slice(0,32);
 const settings:any={['connections.profile.'+ids[0]]:{id:ids[0],version:ids[1],kind:'ai',name:'Brief generation model',endpoint:'https://model.example/PRIVATE_MODEL_PATH',provider:'openai',model:'configured-example-model',secret:'ENCRYPTED_MODEL_SENTINEL'},['connections.profile.'+ids[2]]:{id:ids[2],version:ids[3],kind:'mcp',name:'Operator-selected source catalog',endpoint:'https://sources.example/PRIVATE_SOURCE_PATH',secret:'ENCRYPTED_SOURCE_SENTINEL'},['agents.model.'+key.id]:{mode:'managed',connection_id:ids[0],connection_version:ids[1],model:'configured-example-model',version:ids[4]},['agents.bundle.'+key.id]:{owner_id:key.user_id,version:ids[5],skills:[],mcp:[{id:ids[2],version:ids[3]}],tools:{enabled:true,model_version:ids[4],allow:[tool]}}};
 try{
  for(const[k,v]of Object.entries(settings))database.prepare('INSERT INTO settings(key,value_json,updated_at) VALUES(?,?,?)').run(k,JSON.stringify(v),new Date().toISOString());
  const path='/admin/agents/recipes?agent='+key.id;await page.goto(path);await page.locator('[data-recipe-details] > summary').click();await expect(page.locator('[data-recipe-configuration]')).toContainText('Assigned generation profile');await expect(page.locator('[data-recipe-configuration]')).toContainText('Brief generation model');await expect(page.locator('[data-recipe-tool]')).toHaveText('fetch_records · Operator-selected source catalog');for(const sentinel of ['PRIVATE_MODEL_PATH','PRIVATE_SOURCE_PATH','ENCRYPTED_MODEL_SENTINEL','ENCRYPTED_SOURCE_SENTINEL'])expect(await page.content()).not.toContain(sentinel);
  await setLocale(page,'ar');await page.setViewportSize({width:390,height:844});await page.locator('[data-recipe-details] > summary').click();await expect(page.locator('[data-recipe-configuration]')).toContainText('اتصال توليد النص المعيّن');expect(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth)).toBe(false);await page.screenshot({path:info.outputPath('recipe-configured-ar-mobile.png'),fullPage:true});
  const profile=settings['connections.profile.'+ids[2]];profile.version='01J00000000000000000000707';database.prepare('UPDATE settings SET value_json=? WHERE key=?').run(JSON.stringify(profile),'connections.profile.'+ids[2]);const stale=await page.goto(path);expect(stale!.status()).toBe(409);await expect(page.locator('[role=alert]')).toBeVisible();await expect(page.locator('[data-recipe-form]')).toHaveCount(0);await expect(page.locator('a[href="/admin/agents/keys/'+key.id+'/bundle"]')).toBeVisible();
 }finally{for(const k of Object.keys(settings))database.prepare('DELETE FROM settings WHERE key=?').run(k);database.prepare('UPDATE api_keys SET revoked_at=? WHERE id=?').run(new Date().toISOString(),key.id);database.close();}
});
