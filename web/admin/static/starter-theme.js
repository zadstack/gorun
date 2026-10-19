(() => {
 'use strict';
 document.querySelectorAll('input[name="starter"][data-identity]').forEach(radio => {
  radio.addEventListener('change', () => {
   if (!radio.checked) return;
   const identity = JSON.parse(radio.dataset.identity), form = radio.closest('form');
   ['accent','background','text','font','width'].forEach(key => {
    const field = form.elements.namedItem(key);
    if (field) field.value = identity[key];
   });
  });
 });

 document.querySelectorAll('[data-starter-preview]').forEach(img=>{const fallback=()=>{img.hidden=true;const description=img.nextElementSibling;if(description?.matches('[data-preview-fallback]'))description.hidden=false;};img.addEventListener('error',fallback);if(img.complete&&!img.naturalWidth)fallback();});
 const form=document.querySelector('[data-starter-wizard]');
 if(form){const steps=[...form.querySelectorAll('[data-wizard-step]')],nav=form.querySelector('[data-wizard-nav]');let current=0;
 const labels=document.documentElement.lang==='ar'?['اختر التصميم','عرّف مشروعك','راجع المسودة']:['Choose design','Introduce project','Review drafts'];
 const controls=document.createElement('div');controls.dataset.wizardControls='true';controls.className='flex flex-wrap gap-3';const back=document.createElement('button'),next=document.createElement('button');back.type=next.type='button';back.textContent=nav.dataset.back;next.textContent=nav.dataset.next;back.className=next.className='border border-line px-4 py-2';controls.append(back,next);form.append(controls);
 function show(n){current=n;steps.forEach((s,i)=>s.hidden=i!==n);nav.replaceChildren();labels.forEach((label,i)=>{const b=document.createElement('button');b.type='button';b.textContent=`${i+1} · ${label}`;b.setAttribute('aria-current',i===n?'step':'false');b.disabled=i>n;b.addEventListener('click',()=>show(i));nav.append(b);});back.hidden=n===0;next.hidden=n===2;if(n===2){const list=form.querySelector('[data-wizard-review]');list.replaceChildren();['starter','title','locale','tagline','description','about','services','contact_email','contact_phone','contact_address'].forEach(name=>{const input=form.elements.namedItem(name),value=name==='starter'?form.querySelector('[name=starter]:checked')?.closest('label')?.querySelector('strong')?.textContent:input?.value;if(!value)return;const entry=document.createElement('div'),term=document.createElement('dt'),definition=document.createElement('dd');term.className='font-bold text-sm';term.textContent=name==='starter'?labels[0]:(input?.closest('label')?.firstChild?.textContent.trim()||name);definition.textContent=value.length>500?value.slice(0,500)+'…':value;definition.className='whitespace-pre-wrap break-words';entry.append(term,definition);list.append(entry);});}nav.focus();}
 back.addEventListener('click',()=>show(Math.max(0,current-1)));next.addEventListener('click',()=>{const invalid=[...steps[current].querySelectorAll('input,select,textarea')].find(el=>!el.checkValidity());if(invalid){invalid.closest('details')?.setAttribute('open','');invalid.reportValidity();return;}show(Math.min(2,current+1));});
 form.addEventListener('submit',e=>{if(current<2){e.preventDefault();next.click();return;}const invalid=[...form.querySelectorAll('input,select,textarea')].find(el=>!el.checkValidity());if(invalid){e.preventDefault();const step=invalid.closest('[data-wizard-step]');show(steps.indexOf(step));invalid.closest('details')?.setAttribute('open','');invalid.reportValidity();}});form.noValidate=true;nav.hidden=false;nav.tabIndex=-1;show(0);
 }
})();
