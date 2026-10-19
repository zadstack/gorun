(() => {
 const init = root => {
  const view=root.querySelector?.('[data-operator-seo]');
  if(!view || view.dataset.ready) return;
  view.dataset.ready='true';
  const rows=Array.from(view.querySelectorAll('[data-seo-row]'));
  rows.sort((a,b)=>Number(b.dataset.priority)-Number(a.dataset.priority));
  for(const row of rows) row.parentElement.append(row);
  const filters=Array.from(view.querySelectorAll('[data-seo-filter]'));
  for(const filter of filters) {
   const key=filter.dataset.seoFilter;
   for(const value of Array.from(new Set(rows.map(row=>row.dataset[key]))).sort()) {
    const option=document.createElement('option');option.value=value;option.textContent=value;filter.append(option);
   }
   filter.addEventListener('change',()=>{for(const row of rows) row.hidden=!filters.every(item=>!item.value || row.dataset[item.dataset.seoFilter]===item.value);});
  }
 };
 if(!window.__operatorInventory){window.__operatorInventory=true;document.addEventListener('htmx:afterSettle',event=>init(event.target));}
 if(document.readyState==='loading')document.addEventListener('DOMContentLoaded',()=>init(document),{once:true});else init(document);
})();
