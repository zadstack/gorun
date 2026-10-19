import {test,expect} from '@playwright/test';
import {createServer} from 'node:http';
import {createHmac,randomUUID} from 'node:crypto';
import {ADMIN,csrf,db,setLocale} from '../lib/helpers.ts';

test('reviewed signed event runs managed text once as a private result and stays owner-isolated',async({page})=>{
 const initialDB=db(),publishedBefore=(initialDB.prepare("SELECT count(*) n FROM content WHERE status='published'").get() as {n:number}).n;
 const previousAI=initialDB.prepare("SELECT key,value_json,updated_at FROM settings WHERE key LIKE 'ai.%'").all();initialDB.close();
 let calls=0;
 const model=createServer(async(request,response)=>{for await(const _ of request){}calls++;response.writeHead(200,{'Content-Type':'application/json'});response.end(JSON.stringify({choices:[{message:{role:'assistant',content:'Synthetic editorial draft from the reviewed signed event.'},finish_reason:'stop'}]}));});
 await new Promise<void>(resolve=>model.listen(0,'127.0.0.1',resolve));
 const address=model.address() as {port:number};
 try{
  await page.goto('/admin/settings/ai');await setLocale(page,'en');
  const setup=await page.request.post('/admin/settings/ai',{form:{_csrf:await csrf(page),edit_token:await page.locator('input[name=edit_token]').inputValue(),provider:'openai',base_url:`http://127.0.0.1:${address.port}/v1`,model:'synthetic-editor-model'},maxRedirects:0});expect(setup.status()).toBe(303);
  await page.goto('/admin/canvases');await page.locator('#new-canvas-title').fill('Signed editorial workflow');await page.locator('form[action="/admin/canvases"] [type=submit]').click();await expect(page).toHaveURL(/\/admin\/canvases\/[A-Z0-9]{26}$/);
  const path=new URL(page.url()).pathname,board=path.split('/').pop()!,promptID=randomUUID(),targetID=randomUUID();
  const token=await csrf(page),cards=[{id:promptID,kind:'prompt',title:'Reviewed instructions',body:'Prepare a short editorial draft. Keep the result private for human review.',x:24,y:24},{id:targetID,kind:'provider',title:'Managed editorial writer',provider_id:'native-text',generation_kind:'text',body:'',x:420,y:24}];
  expect((await page.request.post(path+'/document',{headers:{'X-CSRF-Token':token},data:{version:1,title:'Signed editorial workflow',cards,edges:[{from:promptID,to:targetID}]}})).status()).toBe(200);
  await page.reload();const card=page.locator(`[data-card-id="${targetID}"]`);await expect(card.locator('.canvas-inbound-prompt-preview')).toContainText('Keep the result private');
  await card.locator('[data-inbound-open]').click();const dialog=page.locator('.canvas-inbound-dialog');await dialog.locator('[name=inbound_command]').fill('editorial-draft');await dialog.getByRole('button',{name:'Save disabled',exact:true}).click();
  const secrets=page.locator('.canvas-inbound-secret');await expect(secrets).toBeVisible();let secret=await secrets.locator('textarea').inputValue();expect(secret.length).toBe(64);await secrets.getByRole('button',{name:'Close',exact:true}).click();await expect(secrets.locator('textarea')).toHaveValue('');
  const metadata=await(await page.request.get(path+`/cards/${targetID}/inbound`)).json();expect(metadata.secret).toBeUndefined();expect(metadata.inbound.enabled).toBe(false);expect(metadata.inbound.execution_mode).toBe('provider');
  const endpoint=metadata.inbound.endpoint_path;
  const send=async(delivery:string,signingSecret=secret)=>{const body=JSON.stringify({command:'editorial-draft'}),timestamp=String(Math.floor(Date.now()/1000));const signature=createHmac('sha256',signingSecret).update(timestamp+'.'+delivery+'.'+body).digest('hex');return fetch(new URL(endpoint,page.url()),{method:'POST',headers:{'Content-Type':'application/json','X-GoRun-Timestamp':timestamp,'X-GoRun-Delivery':delivery,'X-GoRun-Signature':'sha256='+signature},body});};
  expect((await send('before_enable_'+randomUUID())).status).toBe(409);expect(calls).toBe(0);
  await dialog.getByRole('button',{name:'Review action',exact:true}).click();await expect(dialog.locator('.canvas-inbound-review')).toContainText('Keep the result private for human review.');await expect(dialog.locator('[data-inbound-enable]')).toBeDisabled();await dialog.locator('[data-inbound-consent]').check();await dialog.locator('[data-inbound-enable]').click();await expect(dialog.locator('.canvas-inbound-summary')).toContainText('Enabled for reviewed action');
  const delivery='accepted_'+randomUUID();expect((await send(delivery)).status).toBe(202);
  await expect.poll(async()=>{const data=await(await page.request.get(path+'/document')).json();return data.cards.some((c:any)=>c.kind==='note'&&c.body==='Synthetic editorial draft from the reviewed signed event.');},{timeout:20000}).toBe(true);
  expect((await send(delivery)).status).toBe(200);expect(calls).toBe(1);
  const database=db();expect((database.prepare('SELECT count(*) n FROM content WHERE status=\'published\'').get() as {n:number}).n).toBe(publishedBefore);
  const draftMapping=database.prepare('SELECT id FROM admin_canvases WHERE id=?').get(board);expect(draftMapping).toBeTruthy();
  const owner=database.prepare('SELECT id,password_hash FROM users WHERE email=?').get(ADMIN.email) as {id:string,password_hash:string};const otherID=randomUUID(),email=`isolated-${otherID}@e2e.test`,now=new Date().toISOString();database.prepare("INSERT INTO users(id,email,name,password_hash,role,locale,created_at,updated_at) VALUES(?,?,?,?,'editor','en',?,?)").run(otherID,email,'Other editor',owner.password_hash,now,now);
  const isolated=await page.context().browser()!.newContext({baseURL:new URL(page.url()).origin,storageState:{cookies:[],origins:[]}});
  try{const other=await isolated.newPage();await other.goto('/admin/login');await other.locator('[name=email]').fill(email);await other.locator('[name=password]').fill(ADMIN.password);await other.locator('form[action^="/admin/login"] [type=submit]').click();await expect(other).toHaveURL(/\/admin\/?$/);expect((await other.request.get(path+`/cards/${targetID}/inbound`)).status()).toBe(404);expect((await other.request.get(path+'/document')).status()).toBe(404);}finally{await isolated.close();database.prepare('DELETE FROM users WHERE id=?').run(otherID);database.close();}
  await dialog.getByRole('button',{name:'Disable',exact:true}).click();expect((await send('disabled_'+randomUUID())).status).toBe(409);
  page.once('dialog',dialog=>dialog.accept());await dialog.getByRole('button',{name:'Rotate signing secret',exact:true}).click();await expect(secrets).toBeVisible();const replacement=await secrets.locator('textarea').inputValue();expect(replacement).not.toBe(secret);await secrets.getByRole('button',{name:'Close',exact:true}).click();expect((await send('old_secret_'+randomUUID())).status).toBe(401);secret='';await expect(secrets.locator('textarea')).toHaveValue('');
  // Structural reuse must not carry the receiver, signing secret or consent.
  const templateResponse=await page.request.post(path+'/template',{headers:{'X-CSRF-Token':token},form:{title:'Reusable editorial structure'}});expect(templateResponse.status()).toBe(201);const template=await templateResponse.json();
  const reuse=await page.request.post('/admin/canvas-templates/'+template.id+'/reuse',{headers:{'X-CSRF-Token':token},form:{title:'Fresh editorial structure'}});expect(reuse.status()).toBe(201);const fresh=await reuse.json();
  const cloned=await(await page.request.get(fresh.href+'/document')).json(),clonedTarget=cloned.cards.find((c:any)=>c.kind==='provider');expect(clonedTarget.provider_id).toBeUndefined();
  const clean=await(await page.request.get(fresh.href+'/cards/'+clonedTarget.id+'/inbound')).json();expect(clean.inbound.id).toBe('');expect(clean.inbound.enabled).toBe(false);expect(clean.inbound.endpoint).toBe('');expect(clean.secret).toBeUndefined();
 }finally{const restore=db();restore.prepare("DELETE FROM settings WHERE key LIKE 'ai.%'").run();for(const row of previousAI as any[])restore.prepare('INSERT INTO settings(key,value_json,updated_at) VALUES(?,?,?)').run(row.key,row.value_json,row.updated_at);restore.close();await new Promise<void>(resolve=>model.close(()=>resolve()));}
});
