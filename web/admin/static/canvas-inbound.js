(() => {
  'use strict';
  window.CanvasInbound = {create};
  function create(api) {
    const {root, t, element:el, button:btn, request, csrf, save, changed} = api;
    const records=new Map(), generations=new Map(), endpointBase=`/admin/canvases/${encodeURIComponent(root.dataset.id)}/cards/`;
    let selected='', review=null, busy=false, lastAuthorityShape='';
    const authorityShape=()=>JSON.stringify({cards:(api.document()?.cards||[]).map(({x,y,...card})=>card),edges:api.document()?.edges||[]});
    const dialog=el('dialog',undefined,'canvas-dialog canvas-inbound-dialog');dialog.setAttribute('aria-labelledby','canvas-inbound-title');root.append(dialog);
    const secretDialog=el('dialog',undefined,'canvas-dialog canvas-inbound-secret');secretDialog.setAttribute('aria-labelledby','canvas-inbound-secret-title');root.append(secretDialog);
    const secretHeading=el('h2',t('inbound_secret_title'));secretHeading.id='canvas-inbound-secret-title';
    const secretValue=el('textarea');secretValue.readOnly=true;secretValue.dir='ltr';secretValue.autocomplete='off';secretValue.spellcheck=false;secretValue.rows=3;secretValue.setAttribute('aria-label',t('inbound_signing_secret'));
    const secretStatus=el('p');secretStatus.setAttribute('role','status');
    secretDialog.append(secretHeading,el('p',t('inbound_secret_help')),secretValue,btn(t('inbound_copy_secret'),()=>copy(secretValue.value,secretStatus)),secretStatus,btn(t('inbound_close'),()=>secretDialog.close()));
    const clearSecret=()=>{secretValue.value='';secretStatus.textContent='';};
    secretDialog.addEventListener('close',clearSecret);window.addEventListener('pagehide',clearSecret);
    function showSecret(value){if(typeof value!=='string'||!value)return;clearSecret();secretValue.value=value;secretDialog.showModal();}
    async function copy(value,status){
      if(!value)return;
      try{if(!navigator.clipboard?.writeText)throw Error();await navigator.clipboard.writeText(value);status.textContent=t('inbound_copied');}
      catch(_){status.textContent=t('inbound_manual_copy');const input=status.closest('dialog')?.querySelector('textarea[readonly],input[readonly]');input?.focus();input?.select();}
    }
    const label=(text,input)=>{const node=el('label',text);node.append(input);return node;};
    const heading=el('h2',t('inbound_settings'));heading.id='canvas-inbound-title';
    const dismiss=btn(t('inbound_close'),()=>dialog.close());
    const top=el('header');top.append(heading,dismiss);
    const summary=el('div',undefined,'canvas-inbound-summary');
    const command=el('input');command.name='inbound_command';command.maxLength=64;command.required=true;command.pattern='[A-Za-z0-9][A-Za-z0-9_.:\-]{0,63}';command.autocomplete='off';command.dir='ltr';
    const promptField=el('textarea');promptField.name='inbound_prompt';promptField.rows=5;promptField.maxLength=4000;
    const promptHint=el('p',undefined,'canvas-inbound-hint');
    const error=el('p',undefined,'canvas-inbound-error');error.setAttribute('role','status');
    const reviewHost=el('section',undefined,'canvas-inbound-review');reviewHost.hidden=true;
    const controls=el('div',undefined,'canvas-inbound-controls');
    const configure=btn(t('inbound_save_disabled'),()=>action(configureRule));
    const preview=btn(t('inbound_review'),()=>action(reviewRule));
    const disable=btn(t('inbound_disable'),()=>action(()=>mutate('disable')));
    const rotate=btn(t('inbound_rotate'),()=>action(async()=>{if(confirm(t('inbound_rotate_confirm')))await mutate('rotate');}));
    controls.append(configure,preview,disable,rotate);
    const guide=el('a',t('inbound_sender_guide'));guide.href='https://github.com/zadstack/gorun/blob/main/docs/CANVAS-INBOUND-WEBHOOKS.md';guide.target='_blank';guide.rel='noopener noreferrer';
    dialog.append(top,el('p',t('inbound_help')),guide,summary,label(t('inbound_command'),command),el('small',t('inbound_command_help')),label(t('inbound_prompt'),promptField),promptHint,controls,error,reviewHost);
    dialog.addEventListener('cancel',event=>{if(busy)event.preventDefault();});
    dialog.addEventListener('close',()=>{selected='';review=null;promptField.value='';command.value='';reviewHost.replaceChildren();error.replaceChildren();});
    const invalidateReview=()=>{review=null;reviewHost.hidden=true;reviewHost.replaceChildren();};
    command.addEventListener('input',invalidateReview);promptField.addEventListener('input',invalidateReview);
    const getCard=()=>api.document()?.cards.find(card=>card.id===selected);
    const promptsFor=card=>{if(!card)return [];const ids=new Set((api.document()?.edges||[]).filter(edge=>edge.to===card.id).map(edge=>edge.from));return(api.document()?.cards||[]).filter(node=>node.kind==='prompt'&&ids.has(node.id));};
    function cleanURL(value,path){
      try{const url=new URL(value,location.origin);if(!['https:','http:'].includes(url.protocol)||url.username||url.password||url.search||url.hash||url.pathname!==path||!/^\/webhooks\/canvas\/[A-Z0-9]{26}$/.test(path))return '';return url.href;}catch(_){return '';}
    }
    const endpoint=data=>data?.endpoint_path?cleanURL(data.endpoint||data.endpoint_path,data.endpoint_path):'';
    const metadata=card=>card?records.get(card.id)?.data:undefined;
    function statusText(data){if(!data?.id)return t('inbound_unconfigured');if(data.status==='needs_review')return t('inbound_needs_review');if(data.enabled)return t('inbound_enabled');return t('inbound_disabled');}
    function line(title,value,copyable=true){
      const row=el('div',undefined,'canvas-inbound-line');row.append(el('span',title));const code=el('code',value||t('inbound_not_selected'));code.dir='ltr';code.title=value||'';row.append(code);
      if(value&&copyable){const copyButton=btn(t('inbound_copy'),()=>copy(value,row.querySelector('[role=status]')));copyButton.setAttribute('aria-label',t('inbound_copy')+': '+title);row.append(copyButton);}
      const status=el('span',undefined,'canvas-sr-only');status.setAttribute('role','status');row.append(status);return row;
    }
    function paint(container,card){
      container.replaceChildren();const data=metadata(card),record=records.get(card.id);
      container.append(line(t('inbound_card_id'),card.id));
      container.append(line(t(card.kind==='agent'?'inbound_agent_id':'inbound_provider_id'),card.kind==='agent'?card.agent_id:card.provider_id));
      if(data?.model)container.append(line(t('inbound_model'),data.model,false));
      const url=endpoint(data);container.append(line(t('inbound_webhook'),url||'',!!url));
      const state=el('span',record?.error?t('inbound_unavailable'):record?.pending?t('inbound_loading'):statusText(data),'canvas-inbound-state');state.dataset.inboundState=data?.status==='needs_review'?'needs_review':data?.enabled?'enabled':data?.status||'unconfigured';container.append(state);
      const prompts=promptsFor(card),effective=typeof data?.prompt==='string'&&data.prompt.trim()?data.prompt:'';
      const promptText=effective||prompts.map(node=>node.body).join('\n\n');if(promptText){const promptLabel=t(effective?'inbound_effective_prompt':'inbound_prompt');const sample=el('p',promptText,'canvas-inbound-prompt-preview');sample.title=promptLabel;container.append(el('small',promptLabel),sample);}
      const open=btn(t('inbound_settings'),()=>action(()=>openSettings(card.id)));open.dataset.inboundOpen=card.id;container.append(open);
    }
    function repaint(){for(const box of root.querySelectorAll('[data-inbound-card]')){const card=api.document()?.cards.find(node=>node.id===box.dataset.inboundCard);if(card)paint(box,card);}}
    async function fetchMetadata(card,force=false){
      const cached=records.get(card.id);
      if(cached?.pending)return cached.pending;
      if(cached&&!force)return cached.data;
      const generation=(generations.get(card.id)||0)+1;generations.set(card.id,generation);
      const pending=(async()=>{
        try{
          const response=await request(endpointBase+encodeURIComponent(card.id)+'/inbound');const data=await response.json();if(!response.ok)throw Error();
          if(generations.get(card.id)!==generation)return records.get(card.id)?.data;
          records.set(card.id,{data:data.inbound,pending:null,error:false});return data.inbound;
        }catch(_){if(generations.get(card.id)===generation)records.set(card.id,{pending:null,error:true});return null;}
        finally{repaint();}
      })();
      records.set(card.id,{...cached,pending,error:false});repaint();return pending;
    }
    async function json(actionName,fields={}){
      const response=await request(endpointBase+encodeURIComponent(selected)+'/inbound',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf},body:JSON.stringify({action:actionName,...fields})});
      let data;try{data=await response.json();}catch(_){throw Error(t('inbound_error'));}
      if(!response.ok){const failure=Error(t(response.status===409?'inbound_changed':'inbound_error'));if(data.error==='reauth_required')failure.reauth=data.reauth_url;throw failure;}
      if(data.inbound){generations.set(selected,(generations.get(selected)||0)+1);records.set(selected,{data:data.inbound,pending:null,error:false});}repaint();return data;
    }
    function renderSummary(data){
      summary.replaceChildren(line(t('inbound_card_id'),selected),line(t('inbound_webhook'),endpoint(data)),el('p',statusText(data)));
      if(data?.execution_mode==='external')summary.append(el('p',t('inbound_external_help')));
      if(data?.execution_mode==='managed')summary.append(el('p',t('inbound_managed_help')));
      if(data?.id)summary.append(line(t('inbound_event_body'),JSON.stringify({command:data.command})));
      preview.disabled=!data?.id;disable.disabled=!data?.enabled;rotate.disabled=!data?.id;
    }
    function refreshDialog(data){invalidateReview();command.value=data?.command||'';renderSummary(data);}
    async function openSettings(id){
      if(!await save())throw Error(t('save_first'));selected=id;error.replaceChildren();review=null;reviewHost.hidden=true;
      const card=getCard();if(!card)return;
      const data=await fetchMetadata(card,true);if(!data){selected='';throw Error(t('inbound_unavailable'));}
      refreshDialog(data);
      const prompts=promptsFor(card);promptField.readOnly=prompts.length!==1;promptField.value=prompts.map(node=>node.body).join('\n\n');
      promptHint.textContent=t(prompts.length===1?'inbound_prompt_edit_help':'inbound_prompt_graph_help');
      for(const prompt of prompts){const edit=btn(t('inbound_edit_linked_prompt')+': '+(prompt.title||t('prompt')),()=>{dialog.close();api.editCard?.(prompt.id);});promptHint.append(edit);}
      dialog.showModal();
    }
    async function configureRule(){
      if(!command.reportValidity())return;
      const card=getCard();if(!card)throw Error(t('inbound_changed'));
      const prompts=promptsFor(card);
      if(prompts.length===1&&promptField.value!==prompts[0].body){if(!promptField.value.trim())throw Error(t('inbound_prompt_required'));prompts[0].body=promptField.value;changed();}
      if(!await save())throw Error(t('save_first'));
      const result=await json('configure',{command:command.value});refreshDialog(result.inbound);if(result.secret)showSecret(result.secret);
    }
    async function mutate(name){const result=await json(name);refreshDialog(result.inbound);if(result.secret)showSecret(result.secret);}
    async function reviewRule(){
      const current=metadata(getCard());
      if(command.value!==current?.command||promptsFor(getCard()).length===1&&promptField.value!==promptsFor(getCard())[0].body)throw Error(t('inbound_save_first'));
      if(!await save())throw Error(t('save_first'));
      const data=await json('review');review=data;reviewHost.replaceChildren();reviewHost.hidden=false;
      const field=(name,text)=>{const box=el('div');box.append(el('h3',name),el('pre',text||'—'));return box;};
      reviewHost.append(el('h3',t('inbound_review_title')),field(t('inbound_command'),data.inbound?.command||command.value),field(t('destination'),[data.provider?.title,data.provider?.model,data.provider?.endpoint].filter(Boolean).join('\n')),field(t('inbound_prompt'),data.prompt),field(t('context'),data.context));
      const consent=el('input');consent.type='checkbox';consent.dataset.inboundConsent='';reviewHost.append(label(t('inbound_consent'),consent));
      const enable=btn(t('inbound_enable'),()=>action(async()=>{if(!consent.checked||!review)return;const result=await json('enable',{review_token:review.review_token});refreshDialog(result.inbound);}));enable.disabled=true;enable.dataset.inboundEnable='';consent.addEventListener('change',()=>{enable.disabled=!consent.checked;});reviewHost.append(enable);reviewHost.scrollIntoView({block:'nearest'});
    }
    async function action(fn){
      if(busy)return;busy=true;error.replaceChildren();dialog.setAttribute('aria-busy','true');dismiss.disabled=true;controls.querySelectorAll('button').forEach(button=>button.disabled=true);
      try{await fn();}catch(failure){error.textContent=failure.message||t('inbound_error');if(failure.reauth){try{const url=new URL(failure.reauth,location.origin);if(url.origin===location.origin&&url.pathname.startsWith('/admin/')){const link=el('a',t('inbound_reauthenticate'));link.href=url.href;error.append(' ',link);}}catch(_){}}if(!dialog.open){dialog.showModal();}}
      finally{busy=false;dialog.removeAttribute('aria-busy');dismiss.disabled=false;configure.disabled=false;const data=selected?records.get(selected)?.data:null;preview.disabled=!data?.id;disable.disabled=!data?.enabled;rotate.disabled=!data?.id;}
    }
    return {
      cardHeader(card){if(!['agent','provider'].includes(card.kind))return null;if(!lastAuthorityShape)lastAuthorityShape=authorityShape();const box=el('section',undefined,'canvas-inbound-card');box.dataset.inboundCard=card.id;paint(box,card);fetchMetadata(card);return box;},
      saved(){const shape=authorityShape();if(shape===lastAuthorityShape)return;lastAuthorityShape=shape;for(const card of api.document()?.cards||[])if(['agent','provider'].includes(card.kind)){generations.set(card.id,(generations.get(card.id)||0)+1);records.delete(card.id);fetchMetadata(card,true);}}
    };
  }
})();
