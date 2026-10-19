(() => {
 'use strict';
 const root=document.querySelector('[data-connections-table]');if(!root)return;
 const rows=[...root.querySelectorAll('tbody>[data-connection-id]')],search=root.querySelector('[data-connection-search]'),kind=root.querySelector('[data-connection-type]'),status=root.querySelector('[data-connection-status]');
 const apply=()=>{const term=search.value.trim().toLocaleLowerCase();let count=0;for(const row of rows){const visible=(!term||row.dataset.search.toLocaleLowerCase().includes(term))&&(!kind.value||row.dataset.kind===kind.value)&&(!status.value||row.dataset.status===status.value);row.hidden=!visible;if(visible)count++;}root.querySelector('[data-connection-count]').textContent=`${count} / ${rows.length}`;root.querySelector('[data-connection-no-results]').hidden=!rows.length||count!==0;};
 [search,kind,status].forEach(input=>{input.addEventListener('input',apply);input.addEventListener('change',apply);});
 root.querySelector('[data-connection-reset]').addEventListener('click',()=>{search.value=kind.value=status.value='';apply();search.focus();});
 for(const checkbox of root.querySelectorAll('[data-connection-column]')){const paint=()=>root.querySelectorAll('[data-connection-cell]').forEach(cell=>{if(cell.dataset.connectionCell===checkbox.dataset.connectionColumn)cell.hidden=!checkbox.checked;});checkbox.addEventListener('change',paint);paint();}
 root.querySelector('[data-connection-columns]').hidden=false;root.querySelector('[data-connection-filters]').hidden=false;apply();
 document.querySelector('[data-connection-create]')?.addEventListener('click',event=>{event.preventDefault();const editor=document.getElementById('connection-editor');editor.open=true;editor.querySelector('[name=name]')?.focus();});
})();
