(() => {
  'use strict';
  const config = document.querySelector('[data-dashboard-pin-config]');
  if (!config) return;
  const workspace = document.querySelector('[data-dashboard-pins]');
  const grid = workspace?.querySelector('[data-pin-grid]');
  let drag = null, busy = false;
  const labels = config.dataset;
  const status = workspace?.querySelector('[data-pin-status]') || (() => {
    const el = document.createElement('p'); el.className = 'dashboard-pin-global-status'; el.hidden = true; el.setAttribute('role', 'status'); el.setAttribute('aria-live', 'polite'); document.body.append(el); return el;
  })();
  function report(key, failed = false) {
    status.hidden = false; status.textContent = labels[key] || labels.error; status.dataset.state = failed ? 'error' : key;
    if (failed) { const reload = document.createElement('button'); reload.type = 'button'; reload.textContent = labels.reload; reload.addEventListener('click', () => location.reload()); status.append(document.createTextNode(' '), reload); }
  }
  function setBusy(value) {
    busy = value;
    document.querySelectorAll('[data-sidebar-pin-add],[data-pin-remove],[data-pin-move],[data-pin-drag]').forEach(el => { el.disabled = value; });
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
    setBusy(true); report('saving');
    const controller = new AbortController(); const timeout = setTimeout(() => controller.abort(), 15000);
    try {
      const response = await fetch(path, {method: 'POST', credentials: 'same-origin', redirect: 'error', signal: controller.signal, headers: {'X-CSRF-Token': labels.csrf, 'Content-Type': 'application/x-www-form-urlencoded'}, body: new URLSearchParams(fields)});
      if (!response.ok) { report([403, 409, 422].includes(response.status) ? 'stale' : 'error', true); return false; }
      const result = await response.json();
      if (result.ok !== true) throw new Error('not_saved');
      report('saved'); return true;
    } catch (_) { report('error', true); return false; }
    finally { clearTimeout(timeout); setBusy(false); }
  }
  async function pin(id) {
    if (await mutate('/admin/dashboard/pins', [['nav_id', id]])) location.assign('/admin');
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
    workspace.addEventListener('dragover', event => { if (drag?.kind === 'nav' && !busy) { event.preventDefault(); event.dataTransfer.dropEffect = 'move'; } });
    workspace.addEventListener('drop', event => {
      if (drag?.kind !== 'nav' || busy) return;
      event.preventDefault(); const id = drag.id;
      const offered = [...document.querySelectorAll('[data-nav-id]')].some(el => el.dataset.navId === id);
      if (offered && event.dataTransfer.getData('application/x-gorun-nav-id') === id) { clearDrag(); pin(id); }
    });
    grid.querySelectorAll('[data-pin-id]').forEach(panel => {
      const id = panel.dataset.pinId;
      panel.querySelector('[data-pin-remove]').addEventListener('click', async () => {
        if (await mutate(`/admin/dashboard/pins/${encodeURIComponent(id)}/remove`, [])) location.reload();
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
  refreshMoves();
})();
