(() => {
 'use strict';
 const root=document.querySelector('[data-studio-structured]'),raw=document.querySelector('[data-studio-config]');if(!root||!raw)return;
 const ar=document.documentElement.lang==='ar',t=(en,arabic)=>ar?arabic:en;
 const node=(tag,text)=>{const el=document.createElement(tag);if(text!==undefined)el.textContent=text;return el;};
 const status=document.querySelector('[data-studio-config-status]');let config,rawEdited=false,readers=[];
 const button=(label,fn)=>{const b=node('button',label);b.type='button';b.addEventListener('click',fn);return b;};
 function field(parent,title,value='',options=null){const label=node('label',title),input=node(options?'select':'input');if(options)for(const v of options){const titles={text:t('Text draft','مسودة نصية'),image:t('Image candidate','صورة للمراجعة'),video:t('Video candidate','فيديو للمراجعة'),render:t('Rendered media','وسائط مُعالجة')};const o=node('option',titles[v]||v);o.value=v;input.append(o);}input.value=value||'';label.append(input);parent.append(label);return input;}
 function text(parent,title,value){const label=node('label',title),input=node('textarea');input.value=value||'';input.rows=3;label.append(input);parent.append(label);return input;}
 function sync(){if(rawEdited)return;const next=structuredClone(config);for(const read of readers)read(next);raw.value=JSON.stringify(next,null,2);}
 function show(){
  root.replaceChildren();readers=[];
  root.append(node('p',t('Configure saved drafting tools and reusable instructions. Saving never executes a tool. Fields preserve existing advanced provider settings.','أعدّ أدوات المسودات والتعليمات المحفوظة. الحفظ لا ينفذ أداة. تحتفظ الحقول بإعدادات المزوّد المتقدمة الموجودة.')));
  const image=field(root,t('Image model (optional)','موديل الصور (اختياري)'),config.image_model);readers.push(next=>next.image_model=image.value.trim());
  const presets=node('section');presets.append(node('h3',t('Reusable instructions','تعليمات قابلة لإعادة الاستخدام')));root.append(presets);
  (config.presets||[]).forEach((p,index)=>{
   const card=node('article');card.className='audit-studio-card';card.append(node('h3',p.name||t('Instructions','تعليمات')));
   const name=field(card,t('Name','الاسم'),p.name),id=field(card,t('Identifier','المعرّف'),p.id),kind=field(card,t('Output','المخرجات'),p.kind,['text','image','video','render']),prompt=text(card,t('Purpose and instructions','الغرض والتعليمات'),p.prompt);
   readers.push(next=>{Object.assign(next.presets[index],{name:name.value.trim(),id:id.value.trim(),kind:kind.value,prompt:prompt.value});});
   card.append(node('small',t('Used only when selected for a reviewed request. No publishing or external action is authorized by this instruction.','تُستخدم عند اختيارها لطلب تتم مراجعته. هذه التعليمات لا تمنح إذنًا بالنشر أو بتنفيذ إجراء خارجي.')));
   card.append(button(t('Remove instruction','حذف التعليمات'),()=>{if(!collect())return;config.presets.splice(index,1);show();sync();}));presets.append(card);
  });
  presets.append(button(t('Add instructions','إضافة تعليمات'),()=>{if(!collect())return;config.presets??=[];config.presets.push({id:'instructions-'+crypto.randomUUID().slice(0,8),name:'',kind:'text',prompt:''});show();sync();}));
  const bindings=node('section');bindings.append(node('h3',t('Allowed drafting tools','أدوات المسودات المسموح بها')));root.append(bindings);
  (config.bindings||[]).forEach((p,index)=>{
   const card=node('article');card.className='audit-studio-card';card.append(node('h3',p.name||t('Tool binding','ربط أداة')));
   const name=field(card,t('Name','الاسم'),p.name),id=field(card,t('Identifier','المعرّف'),p.id),kind=field(card,t('Output','المخرجات'),p.kind,['text','image','video','render']);
   const connection=field(card,t('Saved MCP connection','اتصال MCP المحفوظ'),'',[]);for(const option of document.querySelector('[data-studio-connection]').options)connection.append(option.cloneNode(true));
   if(p.connection_id&&!Array.from(connection.options).some(o=>o.value===p.connection_id)){const option=node('option',t('Unavailable saved connection','الاتصال المحفوظ غير متاح'));option.value=p.connection_id;connection.append(option);}connection.value=p.connection_id||'';
   const tool=field(card,t('Exact discovered tool name','اسم الأداة المكتشفة الدقيق'),p.tool);tool.setAttribute('list','studio-discovered-tools');
   const args=text(card,t('Input mapping (JSON object)','ربط المدخلات (كائن JSON)'),JSON.stringify(p.arguments||{},null,2));args.dir='ltr';
   const output=field(card,t('Result text path (optional)','مسار نص النتيجة (اختياري)'),p.text_path),url=field(card,t('Media URL path (for media tools)','مسار رابط الوسائط (لأدوات الوسائط)'),p.url_path);
   card.append(node('small',t('Discover the connection first. The schema, result paths and limits must match your provider. Never include credentials. Async polling settings remain in Advanced JSON.','اكتشف الاتصال أولًا. يجب أن يطابق ربط المدخلات والمسارات والحدود المزوّد الفعلي. لا تضف بيانات سرية. إعدادات متابعة المهام المتقدمة تبقى في JSON.')));
   readers.push(next=>{const argumentsObject=JSON.parse(args.value);if(!argumentsObject||typeof argumentsObject!=='object'||Array.isArray(argumentsObject))throw Error(t('Input mapping must be a JSON object.','ربط المدخلات يجب أن يكون كائن JSON.'));Object.assign(next.bindings[index],{name:name.value.trim(),id:id.value.trim(),kind:kind.value,connection_id:connection.value,tool:tool.value.trim(),arguments:argumentsObject,text_path:output.value.trim(),url_path:url.value.trim()});});
   card.append(button(t('Remove tool','حذف الأداة'),()=>{if(!collect())return;config.bindings.splice(index,1);show();sync();}));bindings.append(card);
  });
  bindings.append(button(t('Add tool binding','إضافة ربط أداة'),()=>{if(!collect())return;config.bindings??=[];config.bindings.push({id:'tool-'+crypto.randomUUID().slice(0,8),name:'',kind:'text',connection_id:'',tool:'',arguments:{prompt:'{{prompt}}',text:'{{text}}'}});show();sync();}));
 }
 function collect(){try{if(rawEdited){status.textContent=t('Load the edited JSON into fields first.','حمّل JSON المعدّل في الحقول أولًا.');return false;}sync();config=JSON.parse(raw.value);return true;}catch(e){status.textContent=e.message;return false;}}
 function load(){try{const next=JSON.parse(raw.value);if(!next||typeof next!=='object'||Array.isArray(next)||next.presets&&!Array.isArray(next.presets)||next.bindings&&!Array.isArray(next.bindings))throw Error(t('Invalid configuration object.','كائن الإعدادات غير صالح.'));config=next;rawEdited=false;show();status.textContent='';}catch(e){root.replaceChildren(node('p',t('Use Advanced JSON to repair this configuration.','استخدم JSON المتقدم لإصلاح الإعدادات.')));document.querySelector('[data-studio-advanced]').open=true;status.textContent=e.message;}}
 raw.addEventListener('input',()=>rawEdited=true);document.querySelector('[data-studio-json-load]').addEventListener('click',load);
 document.querySelector('[data-studio-config-save]').addEventListener('click',event=>{if(!rawEdited&&!collect()){event.stopImmediatePropagation();event.preventDefault();}},true);
 const choices=node('datalist');choices.id='studio-discovered-tools';root.after(choices);
 document.addEventListener('studio-tools-discovered',event=>{choices.replaceChildren();for(const tool of event.detail?.tools||[]){const option=node('option');option.value=tool.name;choices.append(option);}});
 load();
})();
