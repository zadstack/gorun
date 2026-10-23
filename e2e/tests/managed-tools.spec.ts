import {test,expect} from '@playwright/test';
import {createHash,randomUUID} from 'node:crypto';
import {csrf,db,setLocale,submitAndLand} from '../lib/helpers.ts';

// Protocol execution is covered by TestManagedToolsActualProtocolApprovalAndPrivateResult.
// This fixture seeds only a paused proposal to exercise the real browser/session
// review boundary without weakening SSRF rules or contacting an external server.
// The fixture connections get a fresh connectivity observation first: otherwise
// the scheduled probe (observe.tick) may test them mid-run, and its write to the
// profile advances the tool authority epoch, so the pending review would answer
// tool_review_changed.
test('managed tool review shows exact destination and arguments, requires consent, rejects safely, and survives late responses',async({page},testInfo)=>{
 await page.goto('/admin/agents/keys');await setLocale(page,'en');
 const name='Reviewed tool agent '+randomUUID();await page.locator('.audit-key-create > summary').click();await page.locator('[data-key-permissions] > summary').click();
  await page.locator('#k-name').fill(name);await page.locator('input[name=all]').check();await submitAndLand(page,page.locator('form[action="/admin/agents/keys"] button[type=submit]'),'/admin/agents/keys',201);
 const database=db(),key=(database.prepare('SELECT id,user_id FROM api_keys WHERE name=?').get(name) as any);
 const ids=['01J00000000000000000000101','01J00000000000000000000102','01J00000000000000000000103','01J00000000000000000000104','01J00000000000000000000105','01J00000000000000000000106'];
 const schema={type:'object',properties:{query:{type:'string'}},required:['query'],additionalProperties:false};
 const tool:any={connection_id:ids[2],connection_version:ids[3],name:'lookup',description:'Read an archive entry',schema,catalog_hash:'a'.repeat(64)};
 tool.alias='gorun_'+createHash('sha256').update([tool.connection_id,tool.connection_version,tool.name,tool.catalog_hash].join('\0')).digest('hex').slice(0,32);
 const settings:any={['connections.profile.'+ids[0]]:{id:ids[0],version:ids[1],kind:'ai',name:'Reviewed model',endpoint:'https://model.example/v1',provider:'openai',model:'reviewed-model'},['connections.profile.'+ids[2]]:{id:ids[2],version:ids[3],kind:'mcp',name:'Editorial archive',endpoint:'https://archive.example/mcp'},['agents.model.'+key.id]:{mode:'managed',connection_id:ids[0],connection_version:ids[1],model:'reviewed-model',version:ids[4]},['agents.bundle.'+key.id]:{owner_id:key.user_id,version:ids[5],skills:[],mcp:[{id:ids[2],version:ids[3]}],tools:{enabled:true,model_version:ids[4],allow:[tool]}}};
 const stamp=()=>new Date().toISOString();let boardPath='';
 const checked=new Date().toISOString().replace(/\.\d+Z$/,'Z'),connections=[[ids[0],ids[1]],[ids[2],ids[3]]];
 try {
  for(const[id,version]of connections)database.prepare(`INSERT OR REPLACE INTO observations(kind,key,success_at,success_revision,success_scope,result_json,updated_at) VALUES('connection',?,?,?,'connectivity','{"count":1}',?)`).run(id,checked,version,checked);
  for(const[k,v]of Object.entries(settings))database.prepare('INSERT INTO settings(key,value_json,updated_at) VALUES(?,?,?)').run(k,JSON.stringify(v),stamp());
  await page.goto('/admin/agents/keys/'+key.id+'/tools');await expect(page.locator('[data-managed-tools]')).toContainText('Selected tools enabled');await expect(page.locator('[data-managed-tools]')).toContainText('lookup');
  await page.goto('/admin/canvases');await page.locator('#new-canvas-title').fill('Review an archive lookup');await page.locator('form[action="/admin/canvases"] [type=submit]').click();boardPath=new URL(page.url()).pathname;
  const prompt=randomUUID(),agent=randomUUID(),board=boardPath.split('/').pop()!;
  const post=async(suffix:string,data:any)=>page.request.post(boardPath+suffix,{headers:{'X-CSRF-Token':await csrf(page)},data});
  expect((await post('/document',{version:1,title:'Review an archive lookup',cards:[{id:prompt,kind:'prompt',body:'Prepare a private evidence-based draft.',x:24,y:24},{id:agent,kind:'agent',title:'Evidence writer',agent_id:key.id,x:420,y:24}],edges:[{from:prompt,to:agent}]})).status()).toBe(200);
  expect((await post('/operations/grants',{agent_id:key.id,read:true,append:true,run:true,node_ids:[prompt]})).status()).toBe(200);
  const review=await(await post('/operations/preview',{version:2,node_id:agent})).json();expect(review.managed_tools).toEqual(['lookup']);
  const prepared=JSON.parse((database.prepare('SELECT document FROM admin_canvas_reviews WHERE id=?').get(review.review_token) as any).document);
  const run=ids[0].slice(0,-2)+'11',approval=ids[0].slice(0,-2)+'12';
  const state={created_at:stamp(),active_millis:1,calls:0,turns:1,messages:[{role:'assistant',tool_calls:[{id:'call_1',type:'function',function:{name:tool.alias,arguments:'{"query":"<script>alert(1)</script>"}'}}]}],pending:{id:approval,call:{id:'call_1',type:'function',function:{name:tool.alias,arguments:'{"query":"<script>alert(1)</script>"}'}},expires_at:new Date(Date.now()+600000).toISOString(),approved:false,attempted:false}};
  database.prepare(`INSERT INTO admin_canvas_runs(id,board_id,owner_id,key_id,request_id,node_id,status,document,created_at) VALUES(?,?,?,?,?,?,'waiting_tool',?,?)`).run(run,board,key.user_id,key.id,randomUUID(),agent,JSON.stringify(prepared),stamp());
  database.prepare('INSERT INTO admin_canvas_tool_states(run_id,owner_id,document) VALUES(?,?,?)').run(run,key.user_id,JSON.stringify(state));
  await page.goto(boardPath);const history=page.locator('.canvas-ops-section').filter({has:page.locator('summary',{hasText:'Run history'})});await history.locator('summary').click();
  const open=page.getByRole('button',{name:'Review proposed tool call',exact:true});await expect(open).toBeVisible();await open.click();
  const dialog=page.locator('.canvas-review');await expect(dialog).toContainText('Editorial archive · archive.example');await expect(dialog).toContainText('<script>alert(1)</script>');expect(await dialog.locator('script').count()).toBe(0);
  await expect(dialog.getByRole('button',{name:'Approve this exact call',exact:true})).toBeDisabled();await dialog.locator('input[type=checkbox]').check();await expect(dialog.getByRole('button',{name:'Approve this exact call',exact:true})).toBeEnabled();
  await page.keyboard.press('Escape');await expect(dialog).not.toBeVisible();expect((database.prepare('SELECT status FROM admin_canvas_runs WHERE id=?').get(run) as any).status).toBe('waiting_tool');
  // A delayed GET must not reopen an escaped or page-hidden review dialog.
  let release:()=>void=()=>{};let entered:()=>void=()=>{};const waiting=new Promise<void>(r=>entered=r),held=new Promise<void>(r=>release=r);
  await page.route('**/operations/runs/'+run+'/tool',async route=>{if(route.request().method()!=='GET'){await route.continue();return;}entered();await held;await route.continue();});
  await open.click();await waiting;await page.keyboard.press('Escape');release();await page.waitForTimeout(200);await expect(dialog).not.toBeVisible();await page.unroute('**/operations/runs/'+run+'/tool');
  await open.click();await expect(dialog).toContainText('Exact arguments');expect(await dialog.evaluate(d=>d.contains(document.activeElement))).toBe(true);await dialog.getByRole('button',{name:'Reject and stop run',exact:true}).click();await expect(dialog).not.toBeVisible();expect((database.prepare('SELECT status FROM admin_canvas_runs WHERE id=?').get(run) as any).status).toBe('cancelled');
  expect((database.prepare('SELECT count(*) n FROM admin_canvas_provenance WHERE run_id=?').get(run) as any).n).toBe(0);
  const nextRun=ids[0].slice(0,-2)+'13';state.pending.id=ids[0].slice(0,-2)+'14';
  database.prepare(`INSERT INTO admin_canvas_runs(id,board_id,owner_id,key_id,request_id,node_id,status,document,created_at) VALUES(?,?,?,?,?,?,'waiting_tool',?,?)`).run(nextRun,board,key.user_id,key.id,randomUUID(),agent,JSON.stringify(prepared),stamp());
  database.prepare('INSERT INTO admin_canvas_tool_states(run_id,owner_id,document) VALUES(?,?,?)').run(nextRun,key.user_id,JSON.stringify(state));
  await page.getByRole('button',{name:'Refresh status',exact:true}).click();await page.getByRole('button',{name:'Review proposed tool call',exact:true}).click();await dialog.locator('input[type=checkbox]').check();
  const accepted=page.waitForResponse(r=>r.request().method()==='POST'&&new URL(r.url()).pathname===boardPath+'/operations/runs/'+nextRun+'/tool');await dialog.getByRole('button',{name:'Approve this exact call',exact:true}).click();expect((await accepted).status()).toBe(200);await expect(dialog).not.toBeVisible();
  expect((await post('/operations/runs/'+nextRun+'/tool',{approval_id:state.pending.id,revision:1,approve:true})).status()).toBe(409);
  // No fabricated completion: this browser fixture has no reachable MCP server.
  expect((database.prepare('SELECT count(*) n FROM admin_canvas_provenance WHERE run_id=?').get(nextRun) as any).n).toBe(0);

  await page.goto('/admin/agents/keys/'+key.id+'/tools');await setLocale(page,'ar');await page.setViewportSize({width:390,height:844});await expect(page.locator('h1')).toContainText('أدوات MCP');expect(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth)).toBe(false);await page.screenshot({path:testInfo.outputPath('managed-tools-ar-mobile.png'),animations:'disabled'});
 } finally {
  // A failure above must not leave the admin (and new content) in English for later specs.
  await setLocale(page,'ar').catch(()=>{});
  if(boardPath)database.prepare('DELETE FROM admin_canvases WHERE id=?').run(boardPath.split('/').pop()!);
  for(const k of Object.keys(settings))database.prepare('DELETE FROM settings WHERE key=?').run(k);
  for(const[id]of connections)database.prepare(`DELETE FROM observations WHERE kind='connection' AND key=?`).run(id);
  database.prepare('DELETE FROM api_keys WHERE id=?').run(key.id);database.close();
 }
});
