(() => {
  'use strict';
  const config = document.querySelector('[data-dashboard-pin-config]');
  if (!config) return;
  let workspace = document.querySelector('[data-dashboard-pins]');
  let grid = workspace?.querySelector('[data-pin-grid]');
  const boundPanels = new WeakSet();
  const requests = new Set();
  let epoch = 0, refreshVersion = 0, pinned = new Set();
  const canonical = id => id === 'builder.identity' ? 'site-builder' : id;
  let drag = null, busy = false;
  const labels = config.dataset;
  const status = (() => {
    const el = document.createElement('p'); el.className = 'dashboard-pin-global-status'; el.hidden = true; el.setAttribute('role', 'status'); el.setAttribute('aria-live', 'polite'); document.body.append(el); return el;
  })();
  function report(key, failed = false) {
    status.hidden = false; status.textContent = labels[key] || labels.error; status.dataset.state = failed ? 'error' : key;
    if (failed) { const reload = document.createElement('button'); reload.type = 'button'; reload.textContent = labels.reload; reload.addEventListener('click', () => location.reload()); status.append(document.createTextNode(' '), reload); }
  }
  function setBusy(value) {
    busy = value;
    document.querySelectorAll('[data-sidebar-pin-add],[data-quick-remove],[data-pin-remove],[data-pin-move],[data-pin-drag]').forEach(el => { el.disabled = value; });
    workspace?.setAttribute('aria-busy', String(value));
    if (!value) refreshMoves();
  }
  function refreshMoves() {
    if (!grid) return;
    const panels = [...grid.querySelectorAll('[data-pin-id]')];
    panels.forEach((panel, index) => { panel.querySelector('[data-pin-move=up]').disabled = index === 0; panel.querySelector('[data-pin-move=down]').disabled = index === panels.length - 1; });
  }
  async function mutate(path, fields) {
    if (busy) return false;
    const generation = epoch, origin = document.activeElement;
    setBusy(true); report('saving');
    const controller = new AbortController(); requests.add(controller); const timeout = setTimeout(() => controller.abort(), 15000);
    try {
      const response = await fetch(path, {method: 'POST', credentials: 'same-origin', redirect: 'error', signal: controller.signal, headers: {'X-CSRF-Token': labels.csrf, 'Content-Type': 'application/x-www-form-urlencoded'}, body: new URLSearchParams(fields)});
      if(generation!==epoch)return false;
      if (!response.ok) { report([403, 409, 422].includes(response.status) ? 'stale' : 'error', true); return false; }
      const result = await response.json();
      if (result.ok !== true) throw new Error('not_saved');
      if (generation !== epoch) return false;
      if (!(await refresh(origin))) throw new Error('projection');
      if (generation !== epoch) return false;
      report('saved'); return true;
    } catch (_) { if (generation === epoch) report('error', true); return false; }
    finally { clearTimeout(timeout); requests.delete(controller); if (generation === epoch) { setBusy(false); if (origin?.isConnected && !origin.disabled) origin.focus({preventScroll:true}); else if(!document.activeElement||document.activeElement===document.body)document.querySelector('[data-quick-access] h2')?.focus({preventScroll:true}); } }
  }
  function updateButtons(ids) {
    pinned = new Set(ids.map(canonical));
    document.querySelectorAll('[data-sidebar-pin-add]').forEach(button => {
      const row = button.closest('[data-nav-id]'), selected = pinned.has(canonical(row.dataset.navId));
      const title = row.querySelector('a > span')?.textContent || '';
      button.dataset.pinned = String(selected); button.setAttribute('aria-pressed',String(selected));
      button.setAttribute('aria-label',`${selected ? labels.remove : labels.add}: ${title}`);
      button.title = selected ? labels.remove : labels.add; button.textContent = selected ? '−' : '＋';
    });
  }
  async function refresh(preferredFocus) {
    const version=++refreshVersion, generation=epoch, controller=new AbortController(); requests.add(controller);
    const timer=setTimeout(()=>controller.abort(),15000), nav=document.querySelector('[data-admin-navigation]');
    try {
      const query=new URLSearchParams({page:location.pathname});if(workspace)query.set('widget','1');
      const response=await fetch(`/admin/dashboard/pins?${query}`,{credentials:'same-origin',redirect:'error',signal:controller.signal,headers:{Accept:'application/json'}});
      if(!response.ok)throw new Error('unavailable');const text=await response.text();if(text.length>2097152)throw new Error('size');const data=JSON.parse(text);
      if(version!==refreshVersion||generation!==epoch)return false;
      if(!Array.isArray(data.ids)||!data.ids.every(id=>typeof id==='string'&&id.length<=256)||typeof data.quick_html!=='string'||typeof data.widget_html!=='string')throw new Error('projection');
      const top=nav?.scrollTop, left=scrollX, pageTop=scrollY, origin=preferredFocus||document.activeElement;
      const quickID=origin?.closest('[data-quick-id]')?.dataset.quickId, quickRemove=origin?.matches('[data-quick-remove]');
      const panelID=origin?.closest('[data-pin-id]')?.dataset.pinId;
      const panelControl=origin?.matches('[data-pin-drag]')?'[data-pin-drag]':origin?.matches('[data-pin-remove]')?'[data-pin-remove]':origin?.matches('[data-pin-move=up]')?'[data-pin-move=up]':origin?.matches('[data-pin-move=down]')?'[data-pin-move=down]':origin?.matches('a')?'footer a':null;
      const root=document.querySelector('[data-sidebar-quick-access]');if(root)root.innerHTML=data.quick_html;
      if(workspace&&data.widget_html){const template=document.createElement('template');template.innerHTML=data.widget_html;const replacement=template.content.querySelector('[data-dashboard-pins]');if(!replacement)throw new Error('widget');workspace.replaceWith(replacement);workspace=replacement;grid=workspace.querySelector('[data-pin-grid]');bindPanels();}
      updateButtons(data.ids);if(nav&&Number.isFinite(top))nav.scrollTop=top;window.scrollTo(left,pageTop);
      if(quickID){const row=[...document.querySelectorAll('[data-quick-id]')].find(row=>row.dataset.quickId===quickID);(row?.querySelector(quickRemove?'[data-quick-remove]':'a')||document.querySelector('[data-quick-access] h2'))?.focus({preventScroll:true});}
      else if(panelID&&panelControl){const panel=[...(grid?.querySelectorAll('[data-pin-id]')||[])].find(panel=>panel.dataset.pinId===panelID);(panel?.querySelector(panelControl)||document.querySelector('[data-quick-access] h2'))?.focus({preventScroll:true});}
      document.dispatchEvent(new CustomEvent('gorun:dashboard-pins-changed',{detail:{ids:data.ids}}));return true;
    } finally { clearTimeout(timer);requests.delete(controller); }
  }
  async function pin(id) {
    id=canonical(id);
    if(pinned.has(id))await mutate(`/admin/dashboard/pins/${encodeURIComponent(id)}/remove`,[]);
    else await mutate('/admin/dashboard/pins', [['nav_id', id]]);
  }
  async function reorder(ids, focusID) {
    if (!grid || !(await mutate('/admin/dashboard/pins/order', ids.map(id => ['nav_id', id])))) return;
    const panels = new Map([...grid.querySelectorAll('[data-pin-id]')].map(el => [el.dataset.pinId, el]));
    ids.forEach(id => { if (panels.has(id)) grid.append(panels.get(id)); });
    refreshMoves(); panels.get(focusID)?.querySelector('[data-pin-drag]')?.focus();
  }
  function clearDrag() {
    drag = null; workspace?.classList.remove('is-drop-target');
    document.querySelectorAll('.is-dragging,.is-reorder-target').forEach(el => el.classList.remove('is-dragging', 'is-reorder-target'));
  }
  document.querySelectorAll('[data-nav-id]').forEach(row => {
    const id = row.dataset.navId;
    row.querySelector('[data-sidebar-pin-add]')?.addEventListener('click', () => pin(id));
    row.querySelector('[data-sidebar-pin-drag]')?.addEventListener('dragstart', event => {
      if (busy || !workspace) { event.preventDefault(); return; }
      drag = {kind: 'nav', id}; event.dataTransfer.effectAllowed = 'move'; event.dataTransfer.setData('application/x-gorun-nav-id', id);
      workspace.classList.add('is-drop-target');
    });
  });
  if (workspace) {
    document.addEventListener('dragover', event => { if (drag?.kind === 'nav' && !busy && event.target.closest('[data-dashboard-pins]')) { event.preventDefault(); event.dataTransfer.dropEffect = 'move'; } });
    document.addEventListener('drop', event => {
      if (drag?.kind !== 'nav' || busy || !event.target.closest('[data-dashboard-pins]')) return;
      event.preventDefault(); const id = drag.id;
      const offered = [...document.querySelectorAll('[data-nav-id]')].some(el => el.dataset.navId === id);
      if (offered && event.dataTransfer.getData('application/x-gorun-nav-id') === id) { clearDrag(); pin(id); }
    });
  }
  function bindPanels(){
    if(!grid)return;
    grid.querySelectorAll('[data-pin-id]').forEach(panel => {
      if(boundPanels.has(panel))return;boundPanels.add(panel);
      const id = panel.dataset.pinId;
      panel.querySelector('[data-pin-remove]').addEventListener('click', async () => {
        await mutate(`/admin/dashboard/pins/${encodeURIComponent(id)}/remove`, []);
      });
      panel.querySelectorAll('[data-pin-move]').forEach(control => control.addEventListener('click', () => {
        const ids = [...grid.querySelectorAll('[data-pin-id]')].map(el => el.dataset.pinId);
        const index = ids.indexOf(id), next = index + (control.dataset.pinMove === 'up' ? -1 : 1);
        if (next < 0 || next >= ids.length) return;
        [ids[index], ids[next]] = [ids[next], ids[index]];
        reorder(ids, id);
      }));
      panel.querySelector('[data-pin-drag]').addEventListener('dragstart', event => {
        if (busy) { event.preventDefault(); return; }
        drag = {kind: 'panel', id}; event.dataTransfer.effectAllowed = 'move'; event.dataTransfer.setData('application/x-gorun-panel-id', id); panel.classList.add('is-dragging');
      });
      panel.addEventListener('dragover', event => { if (drag?.kind === 'panel' && drag.id !== id && !busy) { event.preventDefault(); event.dataTransfer.dropEffect = 'move'; panel.classList.add('is-reorder-target'); } });
      panel.addEventListener('dragleave', event => { if (!panel.contains(event.relatedTarget)) panel.classList.remove('is-reorder-target'); });
      panel.addEventListener('drop', event => {
        if (drag?.kind !== 'panel' || drag.id === id || busy) return;
        event.preventDefault(); event.stopPropagation(); const moving = drag.id;
        if (event.dataTransfer.getData('application/x-gorun-panel-id') !== moving) { clearDrag(); return; }
        const ids = [...grid.querySelectorAll('[data-pin-id]')].map(el => el.dataset.pinId);
        const from = ids.indexOf(moving), to = ids.indexOf(id);
        if (from < 0 || to < 0) { clearDrag(); return; }
        ids.splice(from, 1); ids.splice(to, 0, moving); clearDrag(); reorder(ids, moving);
      });
    });
  }
  document.addEventListener('dragend', clearDrag);
  document.addEventListener('keydown', event => { if (event.key === 'Escape') clearDrag(); });
  document.addEventListener('click',event=>{const button=event.target.closest('[data-quick-remove]');if(button)mutate(`/admin/dashboard/pins/${encodeURIComponent(button.dataset.quickRemove)}/remove`,[]);});
  const readRefresh=()=>{const generation=epoch;if(!busy&&document.visibilityState!=='hidden')refresh().catch(()=>{if(generation===epoch)report('error',true);});};
  window.addEventListener('focus',readRefresh);document.addEventListener('visibilitychange',readRefresh);document.addEventListener('gorun:dashboard-pins-refresh',readRefresh);
  window.addEventListener('pagehide',()=>{epoch++;refreshVersion++;requests.forEach(request=>request.abort());requests.clear();setBusy(false);clearDrag();});
  window.addEventListener('pageshow',event=>{if(!event.persisted)return;epoch++;refreshVersion++;requests.forEach(request=>request.abort());requests.clear();setBusy(false);clearDrag();const generation=epoch;refresh().then(()=>{if(generation===epoch)report('refreshed');}).catch(()=>{if(generation===epoch)report('error',true);});});
  updateButtons([...document.querySelectorAll('[data-quick-id]')].map(row=>row.dataset.quickId));bindPanels();refreshMoves();
})();
