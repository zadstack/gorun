(() => {
 'use strict';
 function init(scope=document) {
  scope.querySelectorAll('[data-content-selection]').forEach(form=>{
   if(form.dataset.ready)return;form.dataset.ready='true';
   const all=form.querySelector('[data-select-all]'),bar=form.querySelector('[data-selection-bar]'),count=form.querySelector('[data-selection-count]');
   const update=()=>{const boxes=[...form.querySelectorAll('input[name=ids]')],selected=boxes.filter(x=>x.checked).length;if(bar)bar.hidden=!selected;if(count)count.textContent=String(selected);if(all){all.checked=!!boxes.length&&selected===boxes.length;all.indeterminate=selected>0&&selected<boxes.length;}};
   all?.addEventListener('change',()=>{form.querySelectorAll('input[name=ids]').forEach(x=>x.checked=all.checked);update();});form.addEventListener('change',update);form.addEventListener('submit',e=>{if(!form.querySelector('input[name=ids]:checked'))e.preventDefault();});update();
  });
 }
 init();document.addEventListener('htmx:afterSwap',()=>init());
 const form=document.querySelector('[data-content-editor]');
 if(form){const state=form.querySelector('[data-save-state]'),label=form.querySelector('[data-save-label]'),status=form.querySelector('[name=status]');const update=()=>{if(label&&status)label.textContent=status.value==='published'?label.dataset.published:status.value==='draft'?(form.dataset.wasPublished==='true'?label.dataset.unpublish:label.dataset.draft):label.dataset.other;};form.addEventListener('input',()=>{if(state)state.textContent=state.dataset.dirty;update();});form.addEventListener('change',()=>{if(state)state.textContent=state.dataset.dirty;update();});update();}
 const filterForm=document.getElementById('content-filters');filterForm?.addEventListener('change',event=>{if(['status','author'].includes(event.target.name)){const view=filterForm.elements.namedItem('view');if(view)view.value='';}},true);
 const filters=document.querySelector('.content-filter-drawer');if(filters&&matchMedia('(max-width:639px)').matches&&!filters.dataset.active)filters.open=false;
})();
