import {test,expect,type Page} from '@playwright/test';
import {randomUUID} from 'node:crypto';
import {csrf,setLocale} from '../lib/helpers.ts';

// Adversarial UI lifecycle fixtures: receiver responses are synthetic, delayed
// explicitly, and must never restore a one-time signing secret after departure.
async function pendingIncoming(page:Page,action:'configure'|'rotate',failure:boolean|'malformed'=false){
 await page.goto('/admin/canvases');await setLocale(page,'en');await page.locator('#new-canvas-title').fill('Lifecycle boundary fixture');await page.locator('form[action="/admin/canvases"] [type=submit]').click();await expect(page).toHaveURL(/\/admin\/canvases\/[A-Z0-9]{26}$/);
 const path=new URL(page.url()).pathname,node=randomUUID(),otherNode=randomUUID(),prompt=randomUUID(),token=await csrf(page);
 const saved=await page.request.post(path+'/document',{headers:{'X-CSRF-Token':token},data:{version:1,title:'Lifecycle boundary fixture',cards:[{id:prompt,kind:'prompt',title:'Saved instructions',body:'Prepare a private review note.',x:24,y:24},{id:node,kind:'provider',title:'Writer',body:'',provider_id:'native-text',generation_kind:'text',x:400,y:24},{id:otherNode,kind:'provider',title:'Other writer',body:'',provider_id:'native-text',generation_kind:'text',x:760,y:24}],edges:[{from:prompt,to:node},{from:prompt,to:otherNode}]}});expect(saved.status()).toBe(200);
 const id='01J00000000000000000000003',metadata={id:action==='rotate'?id:'',node_id:node,provider_id:'native-text',agent_id:'',command:action==='rotate'?'review-note':'',enabled:false,status:action==='rotate'?'disabled':'unconfigured',revision:1,can_manage:true,execution_mode:'provider',prompt:'Prepare a private review note.',endpoint_path:action==='rotate'?'/webhooks/canvas/'+id:'',endpoint:action==='rotate'?new URL('/webhooks/canvas/'+id,page.url()).href:''};
 let started=false,release!:()=>void;
 const delayed=new Promise<void>(resolve=>release=resolve);
 const endpoint=path+'/cards/'+node+'/inbound';
 await page.route('**'+endpoint,async route=>{
  if(route.request().method()==='POST'){
   expect(route.request().postDataJSON().action).toBe(action);started=true;await delayed;
   if(failure==='malformed'){await route.fulfill({status:500,contentType:'application/json',body:'not-json'});return;}
   await route.fulfill({status:failure?500:200,json:failure?{error:'synthetic_late_failure'}:{inbound:{...metadata,id,command:'review-note',endpoint_path:'/webhooks/canvas/'+id,endpoint:new URL('/webhooks/canvas/'+id,page.url()).href},secret:'SYNTHETIC_LATE_RESPONSE_SECRET'}});return;
  }
  await route.fulfill({json:{inbound:metadata}});
 });
 await page.reload();await page.locator(`[data-inbound-open="${node}"]`).click();
 const settings=page.locator('.canvas-inbound-dialog');
 if(action==='configure'){await settings.locator('[name=inbound_command]').fill('review-note');await settings.getByRole('button',{name:'Save disabled',exact:true}).click();}
 else{page.once('dialog',dialog=>dialog.accept());await settings.getByRole('button',{name:'Rotate signing secret',exact:true}).click();}
 await expect.poll(()=>started).toBe(true);
 return {settings,node,otherNode,release:async()=>{const response=page.waitForResponse(r=>r.request().method()==='POST'&&new URL(r.url()).pathname===endpoint);release();await(await response).finished();await page.evaluate(()=>new Promise<void>(resolve=>requestAnimationFrame(()=>requestAnimationFrame(()=>resolve()))));}};
}
for(const action of ['configure','rotate'] as const){
 test(`late ${action} response cannot restore secret after pagehide or BFCache return`,async({page})=>{
  const pending=await pendingIncoming(page,action);
  await page.evaluate(()=>window.dispatchEvent(new PageTransitionEvent('pagehide',{persisted:true})));
  await expect(pending.settings).not.toBeVisible();
  if(action==='rotate')await page.evaluate(()=>window.dispatchEvent(new PageTransitionEvent('pageshow',{persisted:true})));
  await pending.release();
  const secret=page.locator('.canvas-inbound-secret');await expect(secret).not.toBeVisible();await expect(secret.locator('textarea')).toHaveValue('');await expect(pending.settings).not.toBeVisible();
  if(action==='configure')await page.evaluate(()=>window.dispatchEvent(new PageTransitionEvent('pageshow',{persisted:true})));
  await expect(secret).not.toBeVisible();await expect(secret.locator('textarea')).toHaveValue('');await expect(pending.settings).not.toBeVisible();
 });
}
test('linked prompt edit is blocked while pending and a forced close discards its late secret',async({page})=>{
 const pending=await pendingIncoming(page,'configure');const linked=pending.settings.getByRole('button',{name:/^Edit prompt card:/});await expect(linked).toBeDisabled();
 // A synthetic dispatched click also exercises the handler guard behind disabled.
 await linked.dispatchEvent('click');await expect(pending.settings).toBeVisible();await expect(page.locator('dialog[aria-labelledby=canvas-dialog-title]')).not.toBeVisible();
 await pending.settings.evaluate((dialog:HTMLDialogElement)=>dialog.close());await expect(pending.settings).not.toBeVisible();await pending.release();
 await expect(page.locator('.canvas-inbound-secret')).not.toBeVisible();await expect(page.locator('.canvas-inbound-secret textarea')).toHaveValue('');await expect(pending.settings).not.toBeVisible();
 // The next settings opening reads fresh metadata; the previous response cannot
 // populate an unselected card or retain the secret in the reopened dialog.
 await page.locator(`[data-inbound-open="${pending.node}"]`).click();await expect(pending.settings).toBeVisible();await expect(pending.settings.locator('[name=inbound_command]')).toHaveValue('');await expect(page.locator('.canvas-inbound-secret textarea')).toHaveValue('');
});
test('late error cannot reopen closed settings',async({page})=>{
 const pending=await pendingIncoming(page,'configure',true);await pending.settings.evaluate((dialog:HTMLDialogElement)=>dialog.close());await expect(pending.settings).not.toBeVisible();await pending.release();await expect(pending.settings).not.toBeVisible();await expect(page.locator('.canvas-inbound-secret textarea')).toHaveValue('');
});

test('late response cannot overwrite a newly opened different card',async({page})=>{
 const pending=await pendingIncoming(page,'configure');
 await pending.settings.evaluate((dialog:HTMLDialogElement)=>dialog.close());await expect(pending.settings).not.toBeVisible();
 await page.locator(`[data-inbound-open="${pending.otherNode}"]`).click();await expect(pending.settings).toBeVisible();
 await expect(pending.settings.locator('.canvas-inbound-summary')).toContainText(pending.otherNode);
 await pending.release();
 await expect(pending.settings.locator('.canvas-inbound-summary')).toContainText(pending.otherNode);await expect(pending.settings.locator('[name=inbound_command]')).toHaveValue('');
 await expect(page.locator('.canvas-inbound-secret')).not.toBeVisible();await expect(page.locator('.canvas-inbound-secret textarea')).toHaveValue('');
});

test('late malformed response cannot reopen closed settings',async({page})=>{
 const pending=await pendingIncoming(page,'configure','malformed');await pending.settings.evaluate((dialog:HTMLDialogElement)=>dialog.close());await expect(pending.settings).not.toBeVisible();await pending.release();await expect(pending.settings).not.toBeVisible();await expect(page.locator('.canvas-inbound-secret textarea')).toHaveValue('');
});
