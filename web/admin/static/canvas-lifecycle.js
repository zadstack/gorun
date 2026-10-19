(() => {
  'use strict';
  const create = api => {
    const {root, t, element, button, request, csrf, save, document: getDocument} = api;
    const base = `/admin/canvases/${encodeURIComponent(root.dataset.id)}`;
    const status = root.querySelector('[data-lifecycle-status]');
    const say = key => {status.textContent=t(key);};
    const post = (url, data={}) => request(url,{method:'POST',headers:{'X-CSRF-Token':csrf,'Content-Type':'application/x-www-form-urlencoded'},body:new URLSearchParams(data)});
    const templates = root.querySelector('[data-owned-templates]');
    const safeNavigate = href => { if (/^\/admin\/(canvases\/[0-9A-HJKMNP-TV-Z]{26}|content\/[a-z0-9_-]+\/[0-9A-HJKMNP-TV-Z]{26})$/.test(href)) location.assign(href); };
    async function refresh() {
      try {
        const response=await request('/admin/canvas-templates');if(!response.ok)throw Error();
        const list=await response.json();templates.replaceChildren();
        if (!list.length) templates.append(element('p',t('templates_empty')));
        for(const item of list){
          const row=element('div',undefined,'canvas-template-row');row.append(element('strong',item.title));
          row.append(button(t('reuse_template'),async()=>{
            const title=prompt(t('template_board_title'),item.title);if(!title?.trim())return;
            if(save&&!await save())return;
            try{const response=await post(`/admin/canvas-templates/${encodeURIComponent(item.id)}/reuse`,{title});if(!response.ok)throw Error();safeNavigate((await response.json()).href);}catch(_){say('lifecycle_error');}
          }));
          row.append(button(t('delete_template'),async()=>{
            if(!confirm(t('delete_template_confirm')))return;
            try{const response=await request(`/admin/canvas-templates/${encodeURIComponent(item.id)}`,{method:'DELETE',headers:{'X-CSRF-Token':csrf}});if(!response.ok)throw Error();await refresh();}catch(_){say('lifecycle_error');}
          }));templates.append(row);
        }
      }catch(_){say('lifecycle_error');}
    }
    root.querySelector('[data-save-template]')?.addEventListener('click',async()=>{
      if(!await save())return;
      if(!confirm(t('template_safety')))return;
      const title=prompt(t('template_name'),getDocument().title);if(!title?.trim())return;
      try{const response=await post(base+'/template',{title});if(!response.ok)throw Error();say('template_saved');await refresh();}catch(_){say('lifecycle_error');}
    });
    const draftDialog=root.querySelector('[data-draft-dialog]');
    const draftForm=draftDialog?.querySelector('form');
    let selectedCard=null;
    draftDialog?.querySelector('[data-draft-cancel]').addEventListener('click',()=>draftDialog.close());
    draftForm?.addEventListener('submit',async event=>{
      event.preventDefault();const submit=draftForm.querySelector('[type=submit]');submit.disabled=true;
      try {
        if(!await save())throw Error();
        const response=await post(base+`/cards/${encodeURIComponent(selectedCard)}/draft`,{type:draftForm.elements.type.value});
        if(!response.ok)throw Error();const data=await response.json();draftDialog.close();safeNavigate(data.href);
      }catch(_){draftDialog.querySelector('[data-draft-error]').textContent=t('draft_error');}finally{submit.disabled=false;}
    });
    refresh();
    return {cardActions(actions,card){
      if(!draftDialog||!['note','result'].includes(card.kind)||!card.body||!getDocument().provenance?.[card.id]||getDocument().results?.[card.run_id])return;
      actions.append(button(t('create_draft'),async()=>{
        if(!await save())return;
        try {
          const response=await request(base+'/draft-options');if(!response.ok)throw Error();
          const types=await response.json();if(!types.length)throw Error();
          const select=draftForm.elements.type;select.replaceChildren(...types.map(type=>{const option=element('option',type.title);option.value=type.id;return option;}));
          selectedCard=card.id;draftDialog.querySelector('[data-draft-error]').textContent='';draftDialog.showModal();
        }catch(_){say('lifecycle_error');}
      }));
    }};
  };
  window.CanvasLifecycle={create};
  const list=document.querySelector('[data-template-library]');
  if(list){
    const labels=Object.fromEntries([...list.querySelectorAll('[data-life-label]')].map(el=>[el.dataset.lifeLabel,el.textContent]));
    create({root:list,t:key=>labels[key]||key,csrf:list.querySelector('[name=_csrf]').value,request:(url,options={})=>fetch(url,{credentials:'same-origin',cache:'no-store',redirect:'error',...options}),element:(tag,text,cls)=>{const el=document.createElement(tag);if(text!==undefined)el.textContent=text;if(cls)el.className=cls;return el;},button:(text,handler)=>{const el=document.createElement('button');el.type='button';el.textContent=text;el.addEventListener('click',handler);return el;}});
  }
})();
