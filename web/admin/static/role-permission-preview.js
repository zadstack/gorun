(() => {
 const init = root => {
  const form=root.querySelector?.('[data-role-preview]');
  if(!form || form.dataset.previewReady)return;
  form.dataset.previewReady='true';
  const saved=new Set(Array.from(form.querySelector('[data-role-baseline]').content.querySelectorAll('[data-permission]'),element=>element.dataset.permission));
  const added=form.querySelector('[data-role-added]'),removed=form.querySelector('[data-role-removed]'),unchanged=form.querySelector('[data-role-unchanged]');
  const fill=(list,values)=>{list.replaceChildren();for(const value of values){const item=document.createElement('li');item.textContent=value;list.append(item);}};
  const update=()=>{
   const current=new Set(Array.from(form.querySelectorAll('input[name=permissions]')).filter(input=>input.type==='hidden'||input.checked).map(input=>input.value));
   const additions=Array.from(current).filter(value=>!saved.has(value)).sort(),removals=Array.from(saved).filter(value=>!current.has(value)).sort();
   fill(added,additions);fill(removed,removals);unchanged.hidden=Boolean(additions.length||removals.length);
  };
  form.addEventListener('change',update);update();
 };
 if(!window.__rolePermissionPreview){window.__rolePermissionPreview=true;document.addEventListener('htmx:afterSettle',event=>init(event.target));}
 if(document.readyState==='loading')document.addEventListener('DOMContentLoaded',()=>init(document),{once:true});else init(document);
})();
