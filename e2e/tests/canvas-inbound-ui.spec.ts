import {test,expect} from '@playwright/test';
import {csrf,setLocale} from '../lib/helpers.ts';

// UI contract test only: the receiver itself is covered by integration tests.
// All backend responses here are explicit synthetic fixtures, never real secrets.
test('incoming-event UI distinguishes identifiers, requires review and clears one-time secrets',async({page},testInfo)=>{
 await page.goto('/admin/canvases');await setLocale(page,'en');
 await page.locator('#new-canvas-title').fill('Weekly editorial briefing');await page.locator('form[action="/admin/canvases"] [type=submit]').click();
 const path=new URL(page.url()).pathname, board=path.split('/').pop()!;
 const promptID='11111111-1111-4111-8111-111111111111',agentID='22222222-2222-4222-8222-222222222222',providerID='33333333-3333-4333-8333-333333333333',keyID='01J00000000000000000000001';
 const cards=[{id:promptID,kind:'prompt',title:'Editorial instructions',body:'Summarize approved campaign updates.',x:24,y:24},{id:agentID,kind:'agent',title:'Editorial assistant',body:'',agent_id:keyID,x:400,y:24},{id:providerID,kind:'provider',title:'Managed writer',body:'',provider_id:'native-text',generation_kind:'text',x:760,y:24}];
 const payload={version:1,title:'Weekly editorial briefing',cards,edges:[{from:promptID,to:agentID},{from:promptID,to:providerID}]};
 expect((await page.request.post(path+'/document',{data:payload,headers:{'X-CSRF-Token':await csrf(page)}})).status()).toBe(200);
 let state:any={id:'',node_id:agentID,agent_id:keyID,provider_id:'',command:'',enabled:false,status:'unconfigured',revision:0,execution_mode:'external',prompt:'PRESET · Follow the saved editorial policy.\n\nSummarize approved campaign updates.',can_manage:true,endpoint:'',endpoint_path:''};
 let rotations=0;
 await page.route('**/admin/canvases/*/cards/*/inbound',async route=>{
  const node=route.request().url().split('/').at(-2);
  if(node===providerID){await route.fulfill({json:{inbound:{...state,id:'',node_id:providerID,agent_id:'',provider_id:'native-text',execution_mode:'provider',model:'Synthetic managed model',enabled:false,status:'unconfigured',endpoint:'',endpoint_path:''}}});return;}
  const body=route.request().method()==='POST'?route.request().postDataJSON():null;
  let secret='';
  if(body?.action==='configure'){secret=state.id?'':'SYNTHETIC_ONE_TIME_SIGNING_SECRET';state={...state,id:'01J00000000000000000000002',command:body.command,status:'disabled',enabled:false,revision:state.revision+1,endpoint_path:'/webhooks/canvas/01J00000000000000000000002',endpoint:new URL('/webhooks/canvas/01J00000000000000000000002',page.url()).href};}
  if(body?.action==='disable'){state={...state,enabled:false,status:'disabled',revision:state.revision+1};}
  if(body?.action==='rotate'){rotations++;secret='SYNTHETIC_ROTATED_SIGNING_SECRET_'+rotations;state={...state,enabled:false,status:'disabled',revision:state.revision+1};}
  if(body?.action==='enable'){expect(body.review_token).toBe('synthetic-reviewed-revision');state={...state,enabled:true,status:'enabled'};}
  if(body?.action==='review'){await route.fulfill({json:{inbound:state,review_token:'synthetic-reviewed-revision',provider:{title:'Editorial assistant'},prompt:'Summarize approved campaign updates as a private draft.',context:'Only the connected, reviewed source context.'}});return;}
  await route.fulfill({json:{inbound:state,...(secret?{secret}:{})}});
 });
 await page.reload();
 const card=page.locator(`[data-card-id="${agentID}"]`);
 await expect(card.locator('.canvas-inbound-card')).toContainText('Card ID');await expect(card.locator('.canvas-inbound-card')).toContainText(agentID);await expect(card.locator('.canvas-inbound-card')).toContainText(keyID);await expect(card.locator('.canvas-inbound-prompt-preview')).toContainText('PRESET · Follow the saved editorial policy.');
 await card.locator('[data-inbound-open]').click();const dialog=page.locator('.canvas-inbound-dialog');await expect(dialog).toContainText('MCP client must stay connected');await expect(dialog.locator('[name=inbound_prompt]')).not.toHaveValue(/PRESET/);
 await dialog.locator('[name=inbound_command]').fill('weekly-summary');await dialog.locator('[name=inbound_prompt]').fill('Summarize approved campaign updates as a private draft.');
 await dialog.getByRole('button',{name:'Save disabled',exact:true}).click();
 const secretDialog=page.locator('.canvas-inbound-secret');await expect(secretDialog).toBeVisible();await expect(secretDialog.locator('textarea')).toHaveValue('SYNTHETIC_ONE_TIME_SIGNING_SECRET');
 await secretDialog.getByRole('button',{name:'Close',exact:true}).click();await expect(secretDialog.locator('textarea')).toHaveValue('');
 await expect(dialog.locator('.canvas-inbound-summary')).toContainText('Disabled');
 const document=await(await page.request.get(path+'/document')).json();expect(document.cards.find((c:any)=>c.id===promptID).body).toContain('as a private draft');
 await dialog.getByRole('button',{name:'Review action',exact:true}).click();await expect(dialog.locator('.canvas-inbound-review')).toContainText('Only the connected, reviewed source context.');
 const enable=dialog.locator('[data-inbound-enable]');await expect(enable).toBeDisabled();await dialog.locator('[data-inbound-consent]').check();await expect(enable).toBeEnabled();
 await page.screenshot({path:testInfo.outputPath('inbound-review-desktop.png')});await enable.click();await expect(dialog.locator('.canvas-inbound-summary')).toContainText('Enabled for reviewed action');
 await dialog.getByRole('button',{name:'Disable',exact:true}).click();await expect(dialog.locator('.canvas-inbound-summary')).toContainText('Disabled');
 page.once('dialog',dialog=>dialog.accept());await dialog.getByRole('button',{name:'Rotate signing secret',exact:true}).click();await expect(secretDialog.locator('textarea')).toHaveValue('SYNTHETIC_ROTATED_SIGNING_SECRET_1');await secretDialog.getByRole('button',{name:'Close',exact:true}).click();await expect(secretDialog.locator('textarea')).toHaveValue('');
 await dialog.getByRole('button',{name:'Close',exact:true}).click();
 await setLocale(page,'ar');await page.setViewportSize({width:390,height:844});await page.locator(`[data-card-id="${agentID}"] [data-inbound-open]`).click();await expect(dialog).toContainText('يجب أن يبقى عميل MCP');
 await expect(dialog.locator('[name=inbound_command]')).toHaveValue('weekly-summary');expect(await dialog.evaluate(el=>el.scrollWidth<=el.clientWidth)).toBe(true);
 await page.screenshot({path:testInfo.outputPath('inbound-settings-mobile-ar.png')});
});
