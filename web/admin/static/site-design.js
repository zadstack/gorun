(() => {
 'use strict';
 const workspace=document.querySelector('[data-site-design-workspace]');if(!workspace)return;
 const form=document.querySelector('[data-identity-form],[data-starter-wizard]');let dirty=false;
 if(form){for(const event of ['input','change'])form.addEventListener(event,()=>{dirty=true;});form.addEventListener('submit',event=>{if(!event.defaultPrevented)dirty=false;});}
 workspace.querySelectorAll('.site-design-tabs a').forEach(link=>link.addEventListener('click',event=>{if(event.ctrlKey||event.metaKey||event.shiftKey||event.altKey||event.button!==0)return;if(dirty&&!window.confirm(workspace.dataset.designLeave)){event.preventDefault();return;}dirty=false;}));
 window.addEventListener('beforeunload',event=>{if(dirty){event.preventDefault();event.returnValue='';}});
 const error=document.querySelector('[data-design-error]');if(error)error.focus();
})();
