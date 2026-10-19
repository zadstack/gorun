import {test,expect} from '@playwright/test';
import {randomUUID} from 'node:crypto';
import {csrf,db,mcp,toolPayload,setLocale} from '../lib/helpers.ts';

test('saved skills reach reviewed external execution, references export no credentials, and edits retire old consent',async({page,request},testInfo)=>{
 await page.goto('/admin/agents/keys');await setLocale(page,'en');
 const name='Bundle writer '+randomUUID();await page.locator('#k-name').fill(name);await page.locator('input[name=all]').check();await page.locator('form[action="/admin/agents/keys"] button[type=submit]').click();
 const token=await page.locator('#k-plaintext').inputValue();
 const database=db(),key=(database.prepare('SELECT id FROM api_keys WHERE name=?').get(name) as any).id;
 const profiles=[{id:'01J00000000000000000000011',version:'01J00000000000000000000012',name:'Evidence MCP',kind:'mcp',endpoint:'https://mcp.example/PATH_CAPABILITY_SECRET',secret:'ENCRYPTED_CREDENTIAL_SENTINEL',test_status:'pass',tested_at:'2026-10-19T00:00:00Z',test_count:2},{id:'01J00000000000000000000013',version:'01J00000000000000000000014',name:'Archive MCP',kind:'mcp',endpoint:'https://archive.example/mcp'}];
 // Stored discovery snapshots are explicit synthetic fixtures. The existing
 // bounded discovery protocol/SSRF implementation is exercised by Go tests.
 for(const p of profiles)database.prepare('INSERT INTO settings(key,value_json,updated_at) VALUES(?,?,?)').run('connections.profile.'+p.id,JSON.stringify(p),new Date().toISOString());
 const path='/admin/agents/keys/'+key+'/bundle';let boardPath='';
 try {
  await page.goto(path);await page.locator('details').first().locator('summary').click();
  await page.locator('[name=skill_name]').first().fill('Evidence policy');await page.locator('[name=skill_instructions]').first().fill('Use only the supplied evidence.');await page.locator('[name=resource_name]').first().fill('Editorial reference');await page.locator('[name=resource_text]').first().fill('Keep all output private for human review.');
  for(const p of profiles)await page.locator(`input[name=mcp][value="${p.id}"]`).check();
  await expect(page.locator('[data-agent-bundle]')).toContainText('Last tool discovery passed');await expect(page.locator('[data-agent-bundle]')).toContainText('Configured; not tested');
  await page.locator('[name=reviewed]').check();await page.getByRole('button',{name:'Save bundle',exact:true}).click();await expect(page.getByRole('status')).toContainText('Bundle saved');
  const exportResponse=await page.request.get(path+'/export');expect(exportResponse.status()).toBe(200);const exported=await exportResponse.json();expect(exported.mcp).toHaveLength(2);expect(exported.skills[0].resource_text).toContain('human review');expect(exported.mcp.find((c:any)=>c.id===profiles[0].id).tested_at).toBe(profiles[0].tested_at);
  const raw=JSON.stringify(exported);for(const hidden of [token,'PATH_CAPABILITY_SECRET','ENCRYPTED_CREDENTIAL_SENTINEL','https://','endpoint','scopes'])expect(raw).not.toContain(hidden);
  const published=(database.prepare("SELECT count(*) n FROM content WHERE status='published'").get() as any).n;
  await page.goto('/admin/canvases');await page.locator('#new-canvas-title').fill('Skill-aware workflow');await page.locator('form[action="/admin/canvases"] [type=submit]').click();boardPath=new URL(page.url()).pathname;
  const prompt=randomUUID(),agent=randomUUID();
  const post=async(suffix:string,data:any)=>page.request.post(boardPath+suffix,{headers:{'X-CSRF-Token':await csrf(page)},data});
  expect((await post('/document',{version:1,title:'Skill-aware workflow',cards:[{id:prompt,kind:'prompt',body:'Prepare a concise editorial draft.',x:24,y:24},{id:agent,kind:'agent',title:'Evidence writer',agent_id:key,x:420,y:24}],edges:[{from:prompt,to:agent}]})).status()).toBe(200);
  expect((await post('/operations/grants',{agent_id:key,read:true,append:true,run:true,node_ids:[prompt]})).status()).toBe(200);
  const preview=await(await post('/operations/preview',{version:2,node_id:agent})).json();expect(preview.prompt).toContain('Use only the supplied evidence.');expect(preview.prompt).toContain('Keep all output private for human review.');
  const queued=await(await post('/operations/runs',{request_id:randomUUID(),review_token:preview.review_token})).json();expect(queued.run.status).toBe('waiting_agent');
  const args:any={board_id:boardPath.split('/').pop(),run_id:queued.run.id};
  const prepared=await mcp(request,token,'tools/call',{name:'canvases.context',arguments:args});expect(prepared.body.result?.isError).not.toBe(true);const context=toolPayload(prepared.body.result).data;expect(context.prompt).toBe(preview.prompt);
  const appended=await mcp(request,token,'tools/call',{name:'canvases.append_result',arguments:{...args,context_token:context.context_token,text:'Private evidence-based draft from the connected external client.'}});expect(appended.body.result?.isError).not.toBe(true);
  const document=await(await page.request.get(boardPath+'/document')).json();expect(document.cards.some((c:any)=>c.body==='Private evidence-based draft from the connected external client.')).toBe(true);expect((database.prepare("SELECT count(*) n FROM content WHERE status='published'").get() as any).n).toBe(published);
  const stale=await(await post('/operations/preview',{version:document.version,node_id:agent})).json();
  const waiting=await(await post('/operations/runs',{request_id:randomUUID(),review_token:stale.review_token})).json();
  await page.goto('/admin');await page.locator('[data-dock-open]').click();await page.locator('[data-dock-list]').getByRole('button',{name:/Evidence writer/}).click();await expect(page.locator('#agents-dock-dialog').getByRole('link',{name:'Skills & MCP connections',exact:true})).toHaveAttribute('href',path);await page.keyboard.press('Escape');
  await page.goto(path);await page.locator('[name=skill_instructions]').first().fill('Use the revised policy.');await page.locator('[name=reviewed]').check();await page.getByRole('button',{name:'Save bundle',exact:true}).click();await expect(page.getByRole('status')).toContainText('Bundle saved');
  expect((await post('/operations/runs',{request_id:randomUUID(),review_token:stale.review_token})).status()).toBe(409);
  const rejected=await mcp(request,token,'tools/call',{name:'canvases.context',arguments:{board_id:args.board_id,run_id:waiting.run.id}});expect(rejected.body.result?.isError).toBe(true);
  const savedVersion=await page.locator('[name=version]').inputValue();await setLocale(page,'ar');await page.reload();await expect(page.locator('[name=version]')).toHaveValue(savedVersion);await expect(page.locator('[data-agent-bundle] [role=alert]')).toHaveCount(0);await page.setViewportSize({width:390,height:844});await expect(page.locator('h1')).toContainText('المهارات واتصالات MCP');expect(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth)).toBe(false);
  await page.screenshot({path:testInfo.outputPath('agent-bundle-ar-mobile.png'),animations:'disabled'});
 } finally {
  if(boardPath)database.prepare('DELETE FROM admin_canvases WHERE id=?').run(boardPath.split('/').pop()!);
  for(const p of profiles)database.prepare('DELETE FROM settings WHERE key=?').run('connections.profile.'+p.id);
  database.prepare('DELETE FROM settings WHERE key=?').run('agents.bundle.'+key);database.prepare('DELETE FROM api_keys WHERE id=?').run(key);database.close();
 }
});
