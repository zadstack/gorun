(() => {
 'use strict';
 // Author/term choices (content admin_choices.go): a select with data-choices-src
 // fetches its <option>s on first use; a data-choices-search input (shown only
 // with JavaScript) refetches them filtered. Without JavaScript the bulk form
 // posts with no value and the server asks in a separate step.
 const loadChoices=(select,q)=>{const src=select&&select.dataset.choicesSrc;if(!src)return;const u=new URL(src,location.href);if(q)u.searchParams.set('q',q);const seq=String((+select.dataset.choicesSeq||0)+1);select.dataset.choicesSeq=seq;select.setAttribute('aria-busy','true');
  fetch(u,{credentials:'same-origin',headers:{'HX-Request':'true'}}).then(r=>r.ok?r.text():Promise.reject(r.status)).then(html=>{if(select.dataset.choicesSeq!==seq)return;select.innerHTML=html;select.dataset.choicesLoaded='';}).catch(()=>{}).finally(()=>{if(select.dataset.choicesSeq===seq)select.removeAttribute('aria-busy');});};
 function initChoices(scope=document){
  scope.querySelectorAll('input[data-choices-search]').forEach(input=>{if(input.dataset.ready)return;input.dataset.ready='true';const select=document.getElementById(input.dataset.choicesSearch);if(!select||!select.dataset.choicesSrc)return;input.hidden=false;let timer;
   input.addEventListener('input',()=>{clearTimeout(timer);timer=setTimeout(()=>loadChoices(select,input.value.trim()),250);});
   input.addEventListener('keydown',e=>{if(e.key==='Enter'){e.preventDefault();clearTimeout(timer);loadChoices(select,input.value.trim());}});});
 }
 function init(scope=document) {
  initChoices(scope);
  scope.querySelectorAll('[data-content-selection]').forEach(form=>{
   if(form.dataset.ready)return;form.dataset.ready='true';
   const all=form.querySelector('[data-select-all]'),bar=form.querySelector('[data-selection-bar]'),count=form.querySelector('[data-selection-count]');
   const update=()=>{const boxes=[...form.querySelectorAll('input[name=ids]')],selected=boxes.filter(x=>x.checked).length;if(bar)bar.hidden=!selected;if(count)count.textContent=String(selected);if(all){all.checked=!!boxes.length&&selected===boxes.length;all.indeterminate=selected>0&&selected<boxes.length;}};
   all?.addEventListener('change',()=>{form.querySelectorAll('input[name=ids]').forEach(x=>x.checked=all.checked);update();});form.addEventListener('change',update);form.addEventListener('submit',e=>{if(!form.querySelector('input[name=ids]:checked'))e.preventDefault();});update();
   // Bulk action inputs: show only the fields the chosen action needs, and ask
   // (data-confirm, partials/confirm_modal) with the selected count where the option says so.
   const action=form.querySelector('[data-bulk-action]'),number=new Intl.NumberFormat(document.documentElement.lang||undefined);
   const syncBulk=()=>{if(!action)return;const opt=action.selectedOptions[0],needs=opt?opt.dataset.bulkNeeds||'':'';form.querySelectorAll('[data-bulk-field]').forEach(f=>{const on=f.dataset.bulkField===needs;f.hidden=!on;f.querySelectorAll('input,select').forEach(x=>{x.disabled=!on;if(x.type==='datetime-local')x.required=on;if(on&&x.tagName==='SELECT'&&x.dataset.choicesSrc&&!('choicesLoaded' in x.dataset)&&!x.hasAttribute('aria-busy'))loadChoices(x,'');});});const n=form.querySelectorAll('input[name=ids]:checked').length;if(opt&&opt.dataset.confirm)form.dataset.confirm=opt.dataset.confirm.replace('{count}',number.format(n));else delete form.dataset.confirm;};
   action?.addEventListener('change',syncBulk);form.addEventListener('change',syncBulk);syncBulk();
  });
 }
 init();document.addEventListener('htmx:afterSwap',()=>init());
 const form=document.querySelector('[data-content-editor]');
 if(form){const state=form.querySelector('[data-save-state]'),label=form.querySelector('[data-save-label]'),status=form.querySelector('[name=status]');const update=()=>{if(label&&status)label.textContent=status.value==='published'?label.dataset.published:status.value==='draft'?(form.dataset.wasPublished==='true'?label.dataset.unpublish:label.dataset.draft):label.dataset.other;};const dirty=e=>{if(e.target&&e.target.closest&&(e.target.closest('[data-preview-links]')||e.target.matches('[data-choices-search]')))return;/* preview link controls are not item edits */if(state)state.textContent=state.dataset.dirty;update();};form.addEventListener('input',dirty);form.addEventListener('change',dirty);update();}
 const filterForm=document.getElementById('content-filters');filterForm?.addEventListener('change',event=>{if(['status','author'].includes(event.target.name)){const view=filterForm.elements.namedItem('view');if(view)view.value='';}},true);
 const filters=document.querySelector('.content-filter-drawer');if(filters&&matchMedia('(max-width:639px)').matches&&!filters.dataset.active)filters.open=false;
})();
