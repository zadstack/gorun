(() => {
 'use strict';
 const root=document.querySelector('[data-studio-structured]'),raw=document.querySelector('[data-studio-config]');if(!root||!raw)return;
 const L=document.querySelector('[data-studio-config-strings]')?.dataset||{},t=key=>L[key]||'';
 const node=(tag,text)=>{const el=document.createElement(tag);if(text!==undefined)el.textContent=text;return el;};
 const status=document.querySelector('[data-studio-config-status]'),advanced=document.querySelector('[data-studio-advanced]');let config,rawEdited=false,readers=[];
 const mode=node('p');mode.dataset.studioEditorMode='';mode.setAttribute('role','status');mode.setAttribute('aria-live','polite');mode.hidden=true;root.before(mode);
 function setRawMode(active){rawEdited=active;root.querySelectorAll('input,select,textarea,button').forEach(control=>control.disabled=active);mode.hidden=!active;mode.textContent=active?t('structuredFieldsArePausedSave'):'';}

 const button=(label,fn)=>{const b=node('button',label);b.type='button';b.addEventListener('click',fn);return b;};
 function field(parent,title,value='',options=null){const label=node('label',title),input=node(options?'select':'input');if(options)for(const v of options){const titles={text:t('textDraft'),image:t('imageCandidate'),video:t('videoCandidate'),render:t('renderedMedia')};const o=node('option',titles[v]||v);o.value=v;input.append(o);}input.value=value||'';label.append(input);parent.append(label);return input;}
 function text(parent,title,value){const label=node('label',title),input=node('textarea');input.value=value||'';input.rows=3;label.append(input);parent.append(label);return input;}
 function sync(){if(rawEdited)return;const next=structuredClone(config);for(const read of readers)read(next);raw.value=JSON.stringify(next,null,2);}
 function show(){
  root.replaceChildren();readers=[];
  root.append(node('p',t('configureSavedDraftingToolsAnd')));
  const image=field(root,t('imageModelOptional'),config.image_model);readers.push(next=>next.image_model=image.value.trim());
  const presets=node('section');presets.append(node('h3',t('reusableInstructions')));root.append(presets);
  (config.presets||[]).forEach((p,index)=>{
   const card=node('article');card.className='audit-studio-card';card.append(node('h3',p.name||t('instructions')));
   const name=field(card,t('name'),p.name),id=field(card,t('identifier'),p.id),kind=field(card,t('output'),p.kind,['text','image','video','render']),prompt=text(card,t('purposeAndInstructions'),p.prompt);
   readers.push(next=>{Object.assign(next.presets[index],{name:name.value.trim(),id:id.value.trim(),kind:kind.value,prompt:prompt.value});});
   card.append(node('small',t('usedOnlyWhenSelectedFor')));
   card.append(button(t('removeInstruction'),()=>{if(!collect())return;config.presets.splice(index,1);show();sync();}));presets.append(card);
  });
  presets.append(button(t('addInstructions'),()=>{if(!collect())return;config.presets??=[];config.presets.push({id:'instructions-'+crypto.randomUUID().slice(0,8),name:'',kind:'text',prompt:''});show();sync();}));
  const bindings=node('section');bindings.append(node('h3',t('allowedDraftingTools')));root.append(bindings);
  (config.bindings||[]).forEach((p,index)=>{
   const card=node('article');card.className='audit-studio-card';card.append(node('h3',p.name||t('toolBinding')));
   const name=field(card,t('name'),p.name),id=field(card,t('identifier'),p.id),kind=field(card,t('output'),p.kind,['text','image','video','render']);
   const connection=field(card,t('savedMcpConnection'),'',[]);for(const option of document.querySelector('[data-studio-connection]').options)connection.append(option.cloneNode(true));
   if(p.connection_id&&!Array.from(connection.options).some(o=>o.value===p.connection_id)){const option=node('option',t('unavailableSavedConnection'));option.value=p.connection_id;connection.append(option);}connection.value=p.connection_id||'';
   const tool=field(card,t('exactDiscoveredToolName'),p.tool);tool.setAttribute('list','studio-discovered-tools');
   const args=text(card,t('inputMappingJsonObject'),JSON.stringify(p.arguments||{},null,2));args.dir='ltr';
   const output=field(card,t('resultTextPathOptional'),p.text_path),url=field(card,t('mediaUrlPathForMedia'),p.url_path);
   card.append(node('small',t('discoverTheConnectionFirstThe')));
   readers.push(next=>{let argumentsObject;try{argumentsObject=JSON.parse(args.value);if(!argumentsObject||typeof argumentsObject!=='object'||Array.isArray(argumentsObject))throw Error();args.removeAttribute('aria-invalid');}catch{args.setAttribute('aria-invalid','true');args.focus();throw Error(t('inputMappingMustContainA'));}Object.assign(next.bindings[index],{name:name.value.trim(),id:id.value.trim(),kind:kind.value,connection_id:connection.value,tool:tool.value.trim(),arguments:argumentsObject,text_path:output.value.trim(),url_path:url.value.trim()});});
   card.append(button(t('removeTool'),()=>{if(!collect())return;config.bindings.splice(index,1);show();sync();}));bindings.append(card);
  });
  bindings.append(button(t('addToolBinding'),()=>{if(!collect())return;config.bindings??=[];config.bindings.push({id:'tool-'+crypto.randomUUID().slice(0,8),name:'',kind:'text',connection_id:'',tool:'',arguments:{prompt:'{{prompt}}',text:'{{text}}'}});show();sync();}));
 }
 function collect(){try{if(rawEdited){status.textContent=t('loadTheEditedJsonInto');return false;}sync();config=JSON.parse(raw.value);return true;}catch(e){status.textContent=e.message;return false;}}
 function load(announce=false){try{const next=JSON.parse(raw.value);if(!next||typeof next!=='object'||Array.isArray(next)||next.presets&&!Array.isArray(next.presets)||next.bindings&&!Array.isArray(next.bindings))throw Error(t('invalidConfigurationObject'));config=next;rawEdited=false;show();setRawMode(false);status.textContent=announce?t('jsonLoadedIntoFieldsReview'):'';}catch(e){root.replaceChildren(node('p',t('useAdvancedJsonToRepair')));setRawMode(true);advanced.open=true;status.textContent=e.message;}}
 raw.addEventListener('input',()=>{setRawMode(true);status.textContent='';});document.querySelector('[data-studio-json-load]').addEventListener('click',()=>load(true));
 advanced.addEventListener('toggle',()=>{if(advanced.open&&!rawEdited&&!collect())advanced.open=false;});
 document.querySelector('[data-studio-config-save]').addEventListener('click',event=>{if(!rawEdited&&!collect()){event.stopImmediatePropagation();event.preventDefault();}},true);
 const choices=node('datalist');choices.id='studio-discovered-tools';root.after(choices);
 document.addEventListener('studio-tools-discovered',event=>{choices.replaceChildren();for(const tool of event.detail?.tools||[]){const option=node('option');option.value=tool.name;choices.append(option);}});
 load();
})();
