/* Explicit, local-only recovery choices. The editor owns all server writes. */
(() => {
  'use strict';
  window.CanvasRecovery={create(api) {
    const {root,t,journal}=api, host=root.querySelector('[data-canvas-recovery]');
    const el=(tag,text,cls)=>{const node=document.createElement(tag);if(text)node.textContent=text;if(cls)node.className=cls;return node;};
    let busy=false,failed='';
    const current=entry=>journal.entries().find(item=>item.key===entry.key&&item.revision===entry.revision);
    function download(entry) {
      const blob=new Blob([JSON.stringify({format:'gorun-canvas-recovery-v1',base_version:entry.baseVersion,document:entry.document},null,2)],{type:'application/json;charset=utf-8'});
      const url=URL.createObjectURL(blob),a=document.createElement('a');a.href=url;a.download='canvas-local-copy.json';a.click();setTimeout(()=>URL.revokeObjectURL(url),1000);
    }
    function action(label,attribute,fn) {const button=el('button',t(label));button.type='button';button.dataset[attribute]='';button.disabled=busy;button.addEventListener('click',fn);return button;}
    async function recover(entry,copy) {
      if(busy||!current(entry))return;busy=true;failed='';render();
      try {if(!await api.recover(entry,copy))failed=entry.key;}
      catch(_){failed=entry.key;}
      finally {busy=false;render();}
    }
    function render() {
      const active=journal.current(), entries=journal.entries().filter(entry=>entry.key!==active?.key || api.failed() || !entry.durable);
      host.hidden=!entries.length&&!journal.problem;
      if(host.hidden){host.replaceChildren();return;}
      const heading=el('h2',t('recovery_title')),help=el('p',t('recovery_help'),'canvas-recovery-help');
      host.replaceChildren(heading,help);
      if(journal.problem){const warning=el('p',t('recovery_storage'));warning.setAttribute('role','alert');host.append(warning);}
      const list=el('ul',undefined,'canvas-recovery-list');
      for(const entry of entries) {
        const item=el('li',undefined,'canvas-recovery-item'),name=el('strong',entry.document.title);name.dir='auto';item.append(name);
        const time=el('time',new Date(entry.changedAt).toLocaleString(document.documentElement.lang||undefined));time.dateTime=new Date(entry.changedAt).toISOString();item.append(time);
        const conflict=entry.baseVersion!==api.base()?.version;
        item.append(el('p',t(!entry.durable?'recovery_storage':conflict?'recovery_conflict':'recovery_other_tab'),'canvas-recovery-help'));
        const actions=el('div',undefined,'canvas-recovery-actions');
        if(!conflict){const restore=action(busy?'recovery_busy':'recovery_restore','recoveryRestore',()=>recover(entry,false));restore.disabled=busy||api.dirty();if(api.dirty())restore.title=t('recovery_edit_first');actions.append(restore);}
        actions.append(action(busy?'recovery_busy':'recovery_copy','recoveryCopy',()=>recover(entry,true)),action('recovery_download','recoveryDownload',()=>download(entry)),action('recovery_discard','recoveryDiscard',()=>{if(confirm(t('recovery_discard_confirm'))){if(!journal.discard(entry.key,entry.revision))failed=entry.key;render();}}));
        item.append(actions,el('p',t('recovery_copy_help'),'canvas-recovery-help'));
        if(failed===entry.key){const error=el('p',t('recovery_failed'));error.setAttribute('role','alert');item.append(error);}
        list.append(item);
      }
      host.append(list);
    }
    window.addEventListener('storage',event=>{if(event.key?.startsWith('gorun:canvas-draft:v1:'))render();});
    return {render,download};
  }};
})();
