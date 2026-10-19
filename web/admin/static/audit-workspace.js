(() => {
  'use strict';
  const keyForm=document.querySelector('[data-key-create]');
  if(keyForm){
    document.querySelector('[data-key-create-open]')?.addEventListener('click',event=>{event.preventDefault();keyForm.closest('details').open=true;keyForm.querySelector('#k-name').focus();});
    const fields=[...keyForm.querySelectorAll('input[name=scopes]')],all=keyForm.querySelector('input[name=all]'),summary=keyForm.querySelector('[data-key-summary]');
    const paint=()=>{const selected=fields.filter(i=>i.checked);summary.textContent=all.checked?'* — '+all.closest('label').innerText.trim():selected.length?summary.dataset.count+' '+selected.map(i=>i.parentElement.querySelector('.block')?.textContent||i.value).join(' · '):summary.dataset.empty;};
    keyForm.querySelectorAll('[data-key-preset]').forEach(button=>button.addEventListener('click',()=>{
      const sets={read:['content.view'],draft:['content.view','canvas.read','canvas.append','canvas.run'],custom:[]};
      all.checked=false;all.dispatchEvent(new Event('change',{bubbles:true}));
      const selected=sets[button.dataset.keyPreset]||[];fields.forEach(i=>{i.checked=selected.includes(i.value);i.dispatchEvent(new Event('change',{bubbles:true}));});
      keyForm.querySelector('[data-key-permissions]').open=true;paint();
    }));keyForm.addEventListener('change',paint);paint();
  }
  const select=document.querySelector('[data-preset-select]'),cards=document.querySelector('[data-preset-cards]');
  if(select&&cards){
    for(const option of select.options){const button=document.createElement('button');button.type='button';button.dataset.presetChoice=option.value;const title=document.createElement('strong'),help=document.createElement('small');title.textContent=option.textContent;help.textContent=!option.value?cards.dataset.blank:option.value==='story-video'?cards.dataset.media:option.value==='evidence-worksheet'?cards.dataset.evidence:cards.dataset.content;button.append(title,help);button.addEventListener('click',()=>{select.value=option.value;select.dispatchEvent(new Event('change',{bubbles:true}));});cards.append(button);}
    const paint=()=>cards.querySelectorAll('button').forEach(button=>button.setAttribute('aria-pressed',String(button.dataset.presetChoice===select.value)));select.addEventListener('change',paint);paint();
    const safety=document.createElement('p');safety.textContent=cards.dataset.help;cards.after(safety);
  }
  const search=document.querySelector('[data-board-search]');if(search)search.addEventListener('input',()=>{const rows=[...document.querySelectorAll('.canvas-board-list>li')],q=search.value.trim().toLocaleLowerCase();rows.forEach(row=>row.hidden=!row.textContent.toLocaleLowerCase().includes(q));document.querySelector('[data-board-search-empty]').hidden=!rows.length||rows.some(row=>!row.hidden);});
})();
