(() => {
 'use strict';
 function init(scope=document) {
  scope.querySelectorAll('[data-content-selection]').forEach(form=>{
   if(form.dataset.ready)return;form.dataset.ready='true';
   const all=form.querySelector('[data-select-all]'),bar=form.querySelector('[data-selection-bar]'),count=form.querySelector('[data-selection-count]');
   const update=()=>{const boxes=[...form.querySelectorAll('input[name=ids]')],selected=boxes.filter(x=>x.checked).length;if(bar)bar.hidden=!selected;if(count)count.textContent=String(selected);if(all){all.checked=!!boxes.length&&selected===boxes.length;all.indeterminate=selected>0&&selected<boxes.length;}};
   all?.addEventListener('change',()=>{form.querySelectorAll('input[name=ids]').forEach(x=>x.checked=all.checked);update();});form.addEventListener('change',update);form.addEventListener('submit',e=>{if(!form.querySelector('input[name=ids]:checked'))e.preventDefault();});update();
   // Bulk action inputs: show only the fields the chosen action needs, and ask
   // (data-confirm, partials/confirm_modal) with the selected count where the option says so.
   const action=form.querySelector('[data-bulk-action]'),number=new Intl.NumberFormat(document.documentElement.lang||undefined);
   const syncBulk=()=>{if(!action)return;const opt=action.selectedOptions[0],needs=opt?opt.dataset.bulkNeeds||'':'';form.querySelectorAll('[data-bulk-field]').forEach(f=>{const on=f.dataset.bulkField===needs;f.hidden=!on;f.querySelectorAll('input,select').forEach(x=>{x.disabled=!on;if(x.type==='datetime-local')x.required=on;});});const n=form.querySelectorAll('input[name=ids]:checked').length;if(opt&&opt.dataset.confirm)form.dataset.confirm=opt.dataset.confirm.replace('{count}',number.format(n));else delete form.dataset.confirm;};
   action?.addEventListener('change',syncBulk);form.addEventListener('change',syncBulk);syncBulk();
  });
 }
 init();document.addEventListener('htmx:afterSwap',()=>init());
 const form=document.querySelector('[data-content-editor]');
 if(form){const state=form.querySelector('[data-save-state]'),label=form.querySelector('[data-save-label]'),status=form.querySelector('[name=status]');const update=()=>{if(label&&status)label.textContent=status.value==='published'?label.dataset.published:status.value==='draft'?(form.dataset.wasPublished==='true'?label.dataset.unpublish:label.dataset.draft):label.dataset.other;};const dirty=e=>{if(e.target&&e.target.closest&&e.target.closest('[data-preview-links]'))return;/* preview link controls are not item edits */if(state)state.textContent=state.dataset.dirty;update();};form.addEventListener('input',dirty);form.addEventListener('change',dirty);update();}
 const filterForm=document.getElementById('content-filters');filterForm?.addEventListener('change',event=>{if(['status','author'].includes(event.target.name)){const view=filterForm.elements.namedItem('view');if(view)view.value='';}},true);
 const filters=document.querySelector('.content-filter-drawer');if(filters&&matchMedia('(max-width:639px)').matches&&!filters.dataset.active)filters.open=false;
})();
