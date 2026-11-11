(() => {
 'use strict';
 // Interface strings come from the admin catalog (partials/studio-strings).
 const L=document.querySelector('[data-studio-strings]')?.dataset||{};
 // Timestamps use the shared admin output: <time> with the short UTC text.
 const when=value=>window.gpDateTime?window.gpDateTime.element(value):document.createTextNode(String(value));
 const csrf=document.querySelector('meta[name="csrf-token"]')?.content||'';
 async function api(url,data){const r=await fetch(url,{method:data?'POST':'GET',credentials:'same-origin',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf},body:data?JSON.stringify(data):undefined});const v=await r.json();if(!r.ok)throw Error(v.error||L.requestFailed);return v;}
 const node=(tag,text,cls)=>{const e=document.createElement(tag);if(text!==undefined)e.textContent=text;if(cls)e.className=cls;return e;};
 const button=(text,fn)=>{const e=node('button',text,'min-h-11 px-3 border border-line rounded');e.type='button';e.addEventListener('click',fn);return e;};
 function editor(){return document.querySelector('[data-field-type="richtext"] [x-ref="editor"]')?.gpQuill;}
 function source(){const q=editor();return {title:document.querySelector('#title')?.value||'',text:q?q.getText():(document.querySelector('#body')?.value||'')};}
 for(const root of document.querySelectorAll('[data-studio]')){
  const find=s=>root.querySelector('[data-studio-'+s+']'),kind=find('kind');if(!kind)continue;
  const endpoint='/admin/x/studio/'+root.dataset.contentId,provider=find('provider'),preset=find('preset'),status=find('status');let data={providers:[],presets:[],items:[]},reviewed=null,timer=null;const drafts=new Map();
  const report=e=>status.textContent=e.message||String(e);
  function invalidate(){reviewed=null;find('review-box').hidden=true;}
  function options(){const selected=provider.value,selectedPreset=preset.value;provider.replaceChildren();for(const p of data.providers.filter(p=>p.kind===kind.value)){const o=node('option',p.name);o.value=p.id;provider.append(o);}if([...provider.options].some(o=>o.value===selected))provider.value=selected;
   preset.replaceChildren(node('option',L.customInstructions));preset.firstChild.value='';for(const p of (data.presets||[]).filter(p=>p.kind===kind.value)){const o=node('option',p.name);o.value=p.id;preset.append(o);}
   if([...preset.options].some(o=>o.value===selectedPreset))preset.value=selectedPreset;find('review').disabled=!provider.value;find('capability').textContent=provider.value?'':L.noProvider;}
  function input(){return {...source(),provider:provider.value,kind:kind.value,preset:preset.value,prompt:find('prompt').value,reviewed:false};}
  async function action(c,what,text){await api(endpoint+'/'+c.id+'/action',{action:what,text:text||''});await refresh();}
  function draw(){find('items').replaceChildren();for(const c of data.items){const card=node('article',undefined,'border border-line rounded p-3 space-y-3');const heading=node('h3',c.input.kind+' · '+c.status+' · ','font-medium');heading.append(when(c.created_at));card.append(heading);if(c.error)card.append(node('p',c.error,'text-red'));
   const controls=node('div',undefined,'flex flex-wrap gap-2');
   if(c.status==='ready'&&c.input.kind==='text'){const text=node('textarea');text.value=drafts.get(c.id)??c.text;text.addEventListener('input',()=>drafts.set(c.id,text.value));text.rows=5;text.className='w-full p-3 border border-line rounded bg-panel';text.setAttribute('aria-label',L.draftResult);card.append(text);
    controls.append(button(L.saveEdits,()=>action(c,'edit',text.value).catch(report)));
    const apply=replace=>{if(replace&&!confirm(L.replaceConfirm))return;const q=editor();if(q){if(replace)q.setText(text.value,'user');else q.insertText(q.getSelection()?.index??q.getLength()-1,'\n'+text.value+'\n','user');}else{const body=document.querySelector('#body');if(body){body.value=replace?text.value:body.value+'\n'+text.value;body.dispatchEvent(new Event('input',{bubbles:true}));}}status.textContent=L.added;};
    controls.append(button(L.insert,()=>apply(false)),button(L.replace,()=>apply(true)));
   }
   if(c.media_id){const url=new URL(c.media_url,location.href);if(['http:','https:'].includes(url.protocol)){const isVideo=/\.mp4(?:\?|$)/i.test(url.pathname);const media=node(isVideo?'video':'img');media.src=url.href;media.className='max-w-full max-h-80';if(isVideo)media.controls=true;else media.alt=L.generatedAlt;card.append(media);}const link=node('a',L.openMedia,'text-blue');link.href='/admin/media/'+encodeURIComponent(c.media_id);card.append(link);}
   if(c.status==='waiting')controls.append(button(L.checkStatus,()=>action(c,'check').catch(report)));
   controls.append(button(L.reusePrompt,()=>{kind.value=c.input.kind;options();provider.value=c.input.provider;find('prompt').value=c.input.prompt;invalidate();find('prompt').focus();}));
   if(!['running','queued'].includes(c.status))controls.append(button(L.remove,()=>action(c,'remove').catch(report)));
   card.append(controls);find('items').append(card);
  }}
  async function refresh(){data=await api(endpoint);find('settings').hidden=!data.can_configure;options();draw();clearTimeout(timer);if(data.items.some(c=>['queued','running'].includes(c.status)))timer=setTimeout(()=>refresh().catch(report),2500);}
  kind.addEventListener('change',()=>{options();invalidate();});provider.addEventListener('change',invalidate);find('prompt').addEventListener('input',invalidate);preset.addEventListener('change',()=>{const p=data.presets.find(p=>p.id===preset.value);if(p)find('prompt').value=p.prompt;invalidate();});
  find('review').addEventListener('click',async()=>{try{const next=input();const result=await api(endpoint+'/preview',next);reviewed={...next,request_id:result.request_id,review_hash:result.review_hash};find('arguments').textContent=JSON.stringify(result.arguments,null,2);find('review-box').hidden=false;status.textContent='';}catch(e){report(e);}});
  find('run').addEventListener('click',async()=>{const btn=find('run');if(!reviewed)return;const current=input();if(JSON.stringify(current)!==JSON.stringify(Object.fromEntries(Object.entries(reviewed).filter(([key])=>!['request_id','review_hash'].includes(key))))){invalidate();report(L.changed);return;}btn.disabled=true;try{await api(endpoint+'/generate',{...reviewed,reviewed:true});invalidate();status.textContent=L.queued;await refresh();}catch(e){report(e);}finally{btn.disabled=false;}});
  refresh().catch(report);
 }
 const settings=document.querySelector('[data-studio-config]');if(settings){const result=document.querySelector('[data-studio-config-status]');document.querySelector('[data-studio-config-save]').addEventListener('click',async()=>{try{await api('/admin/settings/studio',JSON.parse(settings.value));result.textContent=L.saved;}catch(e){result.textContent=e.message;}});document.querySelector('[data-studio-discover]').addEventListener('click',async()=>{try{const data=await api('/admin/settings/studio/discover',{id:document.querySelector('[data-studio-connection]').value});const list=document.querySelector('[data-studio-tools]');list.replaceChildren();for(const tool of data.tools||[]){const card=node('article',undefined,'audit-studio-card');card.append(node('h3',tool.name),node('p',tool.description||L.noDescription));const details=node('details');details.append(node('summary',L.inputSchema),node('pre',JSON.stringify(tool.inputSchema||tool.schema||{},null,2)));card.append(details);list.append(card);}document.dispatchEvent(new CustomEvent('studio-tools-discovered',{detail:data}));result.textContent=L.toolsListed;}catch(e){result.textContent=e.message;}});}
})();
