(() => {
 'use strict';
 const studio=document.querySelector('[data-identity-studio]');if(!studio)return;
 const form=studio.querySelector('[data-identity-form]'),frame=studio.querySelector('[data-identity-preview-frame]'),wrap=studio.querySelector('[data-identity-frame-wrap]');
 if(!form||!frame)return;
 const base=studio.dataset.previewUrl;const state={view:'home',width:'desktop',locale:(studio.querySelector('[data-preview-locale][aria-pressed="true"]')||{}).dataset?.previewLocale||'ar'};
 const url=()=>base+'?view='+encodeURIComponent(state.view)+'&locale='+encodeURIComponent(state.locale)+'&t='+Date.now();
 const reload=()=>{frame.src=url();const open=studio.querySelector('[data-preview-open]');if(open)open.href=base+'?view='+state.view+'&locale='+state.locale;};
 const press=(selector,value)=>studio.querySelectorAll(selector).forEach(b=>b.setAttribute('aria-pressed',String(b.dataset.previewView===value||b.dataset.previewWidth===value||b.dataset.previewLocale===value)));
 studio.querySelectorAll('[data-preview-view]').forEach(b=>b.addEventListener('click',()=>{state.view=b.dataset.previewView;press('[data-preview-view]',state.view);reload();}));
 studio.querySelectorAll('[data-preview-locale]').forEach(b=>b.addEventListener('click',()=>{state.locale=b.dataset.previewLocale;press('[data-preview-locale]',state.locale);reload();}));
 studio.querySelectorAll('[data-preview-width]').forEach(b=>b.addEventListener('click',()=>{state.width=b.dataset.previewWidth;press('[data-preview-width]',state.width);if(wrap)wrap.dataset.width=state.width;}));
 // WCAG contrast, mirrored from the server (which has the final say).
 const lum=hex=>{let h=hex.trim();if(/^#[0-9a-f]{3}$/i.test(h))h='#'+h[1]+h[1]+h[2]+h[2]+h[3]+h[3];const m=/^#([0-9a-f]{6})$/i.exec(h);if(!m)return null;const c=[0,2,4].map(i=>{const v=parseInt(m[1].slice(i,i+2),16)/255;return v<=.03928?v/12.92:Math.pow((v+.055)/1.055,2.4);});return .2126*c[0]+.7152*c[1]+.0722*c[2];};
 const contrast=(a,b)=>{const la=lum(a),lb=lum(b);if(la===null||lb===null)return 0;const [hi,lo]=la>lb?[la,lb]:[lb,la];return (hi+.05)/(lo+.05);};
 const badge=studio.querySelector('[data-identity-contrast]');
 const updateContrast=()=>{if(!badge)return;const text=form.elements.namedItem('identity_text')?.value||badge.dataset.text,back=form.elements.namedItem('identity_background')?.value||badge.dataset.back;const ratio=contrast(text||'',back||'');const r=badge.querySelector('[data-contrast-ratio]'),v=badge.querySelector('[data-contrast-verdict]');if(!ratio||!r||!v)return;const pass=ratio>=4.5;r.textContent=ratio.toFixed(1)+':1';v.textContent=pass?badge.dataset.passText:badge.dataset.failText;v.dataset.pass=String(pass);badge.classList.toggle('identity-contrast-fail',!pass);};
 // Draft autosave: debounce edits, store the draft, refresh the preview.
 let timer=null,saving=false,dirty=false,submitting=false;
 const saveDraft=async()=>{if(submitting)return;if(saving){dirty=true;return;}saving=true;dirty=false;try{const body=new URLSearchParams(new FormData(form));body.set('action','draft');const resp=await fetch(form.getAttribute('action'),{method:'POST',body,credentials:'same-origin',headers:{'X-Requested-With':'fetch','Accept':'text/html'}});if(resp.status===204)reload();}catch(_){/* keep the manual buttons */}finally{saving=false;if(dirty&&!submitting)saveDraft();}};
 const schedule=()=>{updateContrast();clearTimeout(timer);timer=setTimeout(saveDraft,500);};
 for(const event of ['input','change'])form.addEventListener(event,schedule);
 // Apply/Discard/Save draft win over a pending autosave: nothing may land after them.
 form.addEventListener('submit',()=>{submitting=true;clearTimeout(timer);});
 const reloadButton=studio.querySelector('[data-preview-reload]');if(reloadButton)reloadButton.addEventListener('click',reload);
 window.addEventListener('media:selected',()=>setTimeout(schedule,0));
 updateContrast();
})();
