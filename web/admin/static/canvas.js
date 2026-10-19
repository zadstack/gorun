(() => {
  'use strict';
  const root = document.getElementById('canvas-editor');
  if (!root) return;
  const labels = Object.fromEntries([...root.querySelectorAll('[data-label]')].map(el => [el.dataset.label, el.textContent]));
  const t = key => labels[key] || key;
  const endpoint = `/admin/canvases/${encodeURIComponent(root.dataset.id)}/document`;
  const csrf = root.querySelector('[name=_csrf]').value;
  const cardsEl = root.querySelector('[data-canvas-cards]');
  const surface = root.querySelector('.canvas-surface');
  const dialog = root.querySelector('dialog[aria-labelledby=canvas-dialog-title]');
  const form = root.querySelector('#canvas-card-form');
  const titleInput = root.querySelector('#canvas-title');
  const mobile = matchMedia('(max-width:767px)');
  const widgetCache = new Map();
  const widgetLoading = new Set();
  let operations = null, lifecycle = null, inbound = null;
  let revealHash = true, revealScheduled = false;
  function revealCardHash() {
    if (!revealHash || revealScheduled || !/^#card-[a-f0-9]{8}-[a-f0-9-]{27}$/.test(location.hash)) return;
    revealScheduled = true;
    requestAnimationFrame(() => {
      revealScheduled = false;
      if (!revealHash || !/^#card-[a-f0-9]{8}-[a-f0-9-]{27}$/.test(location.hash)) return;
      // Options may have repainted the cards since scheduling this frame.
      const card = cardsEl.querySelector(`[id="${location.hash.slice(1)}"]`);
      if (!card) return;
      revealHash = false;
      card.scrollIntoView({block:'center', inline:'center'});
      card.querySelector('button,a')?.focus({preventScroll:true});
    });
  }
  window.addEventListener('hashchange', () => { revealHash = true; revealCardHash(); });
  let view = 'flow';
  try { const stored=sessionStorage.getItem('gorun.canvas.view'); if (['flow','grid','list'].includes(stored)) view=stored; } catch (_) {}
  root.dataset.view=view;
  const heights = () => Object.fromEntries([...cardsEl.children].map(el=>[el.dataset.cardId,el.offsetHeight]));
  function normalizePositions() {
    if (!documentState || mobile.matches || view !== 'flow') return;
    const positions=CanvasLayout.resolve(documentState.cards,heights());
    for (const card of documentState.cards) { Object.assign(card,positions[card.id]); const el=cardsEl.querySelector(`[data-card-id="${card.id}"]`); if(el)place(el,card); }
    updateBounds();
  }
  const resizeCards = new ResizeObserver(() => normalizePositions());
  function canPlace(card,x,y) {
    const size=heights(), box={x,y,width:CanvasLayout.WIDTH,height:size[card.id]||240};
    return documentState.cards.every(other=>other.id===card.id||!CanvasLayout.overlap(box,{x:other.x,y:other.y,width:CanvasLayout.WIDTH,height:size[other.id]||240},24));
  }
  function selectView(next) {
    view=next;root.dataset.view=view;
    try {sessionStorage.setItem('gorun.canvas.view',view);}catch(_){}
    root.querySelectorAll('[data-canvas-view]').forEach(button=>button.setAttribute('aria-pressed',String(button.dataset.canvasView===view)));
    root.querySelector('[data-action=arrange]').disabled=view!=='flow';
    if(documentState)render();
  }
  const cardOrder = (a,b) => a.x-b.x || a.y-b.y;
  const kindTitle = kind => t(kind === 'source' ? 'ops_source' : kind);
  let documentState = null, serial = 0, savedSerial = 0, timer = null, pending = null, blocked = false, editID = null, newKind = null;
  const element = (tag, text, className) => { const el = document.createElement(tag); if (text !== undefined) el.textContent = text; if (className) el.className = className; return el; };
  const button = (label, action, className) => { const el = element('button', label, className); el.type = 'button'; el.addEventListener('click', action); return el; };
  const bytes = value => new TextEncoder().encode(value).length;
  const safeURL = (value, internal = false) => {
    try { if (!value || /[\\\u0000-\u0020\u007f]/.test(value) || value.startsWith('//')) return null; const u = new URL(value, location.origin); if (u.username || u.password || !['http:', 'https:'].includes(u.protocol) || (internal && u.origin !== location.origin)) return null; return u.href; } catch (_) { return null; }
  };
  const link = (title, href, internal = false) => { const url = safeURL(href, internal); if (!url) return element('span', title); const a = element('a', title); a.href = url; if (title === href) a.dir = 'ltr'; if (!internal) { a.target = '_blank'; a.rel = 'noopener noreferrer'; } return a; };
  const dirty = () => serial !== savedSerial;
  function status(key) {
    const state = root.querySelector('.canvas-state');
    state.dataset.state = key;
    root.querySelector('[data-status]').textContent = t(key);
    root.querySelector('[data-action=retry]').hidden = !['error', 'load_error'].includes(key);
    root.querySelector('[data-action=reload]').hidden = key !== 'conflict';
  }
  function changed() {
    serial++;
    status(blocked ? 'conflict' : 'unsaved');
    clearTimeout(timer);
    if (!blocked) timer = setTimeout(save, 900);
  }
  async function request(url, options = {}) {
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 20000);
    try { return await fetch(url, { credentials: 'same-origin', redirect: 'error', cache: 'no-store', ...options, signal: controller.signal }); }
    finally { clearTimeout(timeout); }
  }
  async function load() {
    status('loading');
    try {
      const response = await request(endpoint);
      if (!response.ok) throw new Error('load');
      const data = await response.json();
      if (!Array.isArray(data.cards) || typeof data.version !== 'number') throw new Error('document');
      documentState = {...data, edges: data.edges || []}; serial = 0; savedSerial = 0; blocked = false;
      titleInput.value = data.title;
      titleInput.disabled = false;
      root.querySelectorAll('[data-action=add],[data-action=close]').forEach(el => { el.disabled = false; });
      render(); status('saved');
    } catch (_) { status('load_error'); }
  }
  async function save() {
    clearTimeout(timer);
    if (blocked || !documentState) return false;
    if (pending) { await pending; if (blocked) return false; return dirty() ? save() : true; }
    if (!dirty()) return true;
    const capturedSerial = serial;
    const payload = {version: documentState.version, title: documentState.title, cards: structuredClone(documentState.cards), edges: structuredClone(documentState.edges)};
    if (!payload.title.trim() || bytes(payload.title) > 120) { status('error'); return false; }
    status('saving');
    pending = (async () => {
      try {
        const response = await request(endpoint, {method: 'POST', headers: {'Content-Type': 'application/json', 'X-CSRF-Token': csrf}, body: JSON.stringify(payload)});
        if (response.status === 409) { blocked = true; status('conflict'); return false; }
        if (!response.ok) throw new Error('save');
        const data = await response.json();
        documentState.version = data.version;
        documentState.updated_at = data.updated_at;
        documentState.references = data.references || {};
        documentState.provenance = data.provenance || {};
        documentState.results = data.results || {};
        documentState.schedules = data.schedules || {};
        savedSerial = capturedSerial;
        status(dirty() ? 'unsaved' : 'saved');
        refreshReferences(); operations?.saved(); inbound?.saved();
        return true;
      } catch (_) { status('error'); return false; }
      finally { pending = null; }
    })();
    const success = await pending;
    if (success && dirty()) return save();
    return success;
  }
  function refreshReferences() {
    root.querySelectorAll('[data-reference]').forEach(el => {
      const ref = documentState.references?.[el.dataset.reference];
      el.replaceChildren(ref ? link(ref.title, ref.href, true) : element('p', el.dataset.reference ? t('unavailable') : t('choose_content')));
    });
  }
  function updateBounds() {
    if (view !== 'flow') { surface.style.minWidth='0';surface.style.minHeight='0';operations?.drawEdges();return; }
    const cards = documentState.cards;
    surface.style.minWidth = `${Math.max(1050, ...cards.map(card => card.x + 348))}px`;
    surface.style.minHeight = `${Math.max(850, ...cards.map(card => card.y + 500))}px`;
    operations?.drawEdges();
  }
  function place(el, card) { el.style.left = `${card.x}px`; el.style.top = `${card.y}px`; }
  function move(card, dx, dy, el) {
    if(view!=='flow')return;
    if (mobile.matches && dy) {
      const ordered = [...documentState.cards].sort(cardOrder);
      const index = ordered.findIndex(c => c.id === card.id);
      const adjacent = ordered[index + (dy < 0 ? -1 : 1)];
      if (!adjacent) return;
      [card.x, adjacent.x] = [adjacent.x, card.x];
      [card.y, adjacent.y] = [adjacent.y, card.y];
      if (card.x === adjacent.x && card.y === adjacent.y) adjacent.y = Math.min(10000, adjacent.y + 24);
      const adjacentElement = cardsEl.querySelector(`[data-card-id="${adjacent.id}"]`);
      if (adjacentElement) place(adjacentElement, adjacent);
    } else {
      const x=Math.max(0,Math.min(10000,card.x+dx)), y=Math.max(0,Math.min(10000,card.y+dy));
      if(!canPlace(card,x,y))return;
      card.x=x;card.y=y;
    }
    place(el, card); updateBounds(); changed();
    if (mobile.matches) {
      const focused = document.activeElement;
      const sorted = [...documentState.cards].sort(cardOrder);
      sorted.forEach(c => { const item = cardsEl.querySelector(`[data-card-id="${c.id}"]`); if (item) cardsEl.append(item); });
      focused?.focus({preventScroll: true});
    }
  }
  function drag(handle, el, card) {
    handle.addEventListener('keydown', event => {
      const deltas = {ArrowLeft: [-24, 0], ArrowRight: [24, 0], ArrowUp: [0, -24], ArrowDown: [0, 24]};
      if (deltas[event.key]) { event.preventDefault(); move(card, ...deltas[event.key], el); }
    });
    handle.addEventListener('pointerdown', event => {
      if (mobile.matches || view !== 'flow' || event.button !== 0) return;
      const start = {x: event.clientX, y: event.clientY, cx: card.x, cy: card.y};
      let moved = false;
      handle.setPointerCapture(event.pointerId); el.classList.add('is-dragging');
      const onMove = e => {
        const x=Math.max(0,Math.min(10000,Math.round((start.cx+e.clientX-start.x)/24)*24));
        const y=Math.max(0,Math.min(10000,Math.round((start.cy+e.clientY-start.y)/24)*24));
        if(!canPlace(card,x,y))return;
        card.x=x;card.y=y;
        moved = card.x !== start.cx || card.y !== start.cy; place(el, card); updateBounds();
      };
      const finish = () => { handle.removeEventListener('pointermove', onMove); handle.removeEventListener('pointerup', finish); handle.removeEventListener('pointercancel', finish); el.classList.remove('is-dragging'); if (moved) changed(); };
      handle.addEventListener('pointermove', onMove); handle.addEventListener('pointerup', finish); handle.addEventListener('pointercancel', finish);
    });
  }
  function widgetBody(body, id) {
    const data = widgetCache.get(id);
    body.replaceChildren();
    if (!data) body.append(element('p', t('widget_loading')));
    else if (data.error) body.append(element('p', t('widget_error')));
    else {
      const metrics = element('div', undefined, 'canvas-widget-metrics');
      (data.metrics || []).slice(0, 6).forEach(m => { const metric = element('div', undefined, 'canvas-widget-metric'); metric.append(element('strong', String(m.value)), element('small', m.label)); metrics.append(metric); });
      body.append(metrics);
      const rows = element('ul', undefined, 'canvas-widget-rows');
      (data.rows || []).slice(0, 5).forEach(row => { const li = element('li'); li.append(row.href ? link(row.title, row.href, true) : element('span', row.title)); if (row.detail) li.append(element('small', row.detail)); rows.append(li); });
      body.append(rows);
      if (data.href) body.append(link(t('source'), data.href, true));
      if (data.updated_at) body.append(element('p', `${t('updated')} · ${new Date(data.updated_at).toLocaleTimeString()}`, 'canvas-widget-time'));
    }
    const refresh = button(t('refresh'), () => fetchWidget(id, true), 'canvas-widget-refresh');
    refresh.disabled = widgetLoading.has(id); body.append(refresh);
    if (!data && !widgetLoading.has(id)) fetchWidget(id);
  }
  async function fetchWidget(id, force = false) {
    if (widgetLoading.has(id) || (!force && widgetCache.has(id))) return;
    widgetLoading.add(id);
    const repaint = () => root.querySelectorAll('[data-widget-id]').forEach(el => { if (el.dataset.widgetId === id) widgetBody(el, id); });
    repaint();
    try {
      const response = await request(`/admin/canvases/${encodeURIComponent(root.dataset.id)}/widgets/${encodeURIComponent(id)}`);
      if (!response.ok) throw new Error('widget');
      widgetCache.set(id, await response.json());
    } catch (_) { widgetCache.set(id, {error: true}); }
    finally { widgetLoading.delete(id); repaint(); }
  }
  function cardView(card) {
    const el = element('article', undefined, 'canvas-card'); el.id='card-'+card.id; el.dataset.cardId = card.id; el.dataset.kind = card.kind; place(el, card);
    const heading = element('header', undefined, 'canvas-card-heading');
    const handle = button('⠿', () => {}, 'canvas-handle'); handle.setAttribute('aria-label', `${t('move')}: ${card.title}`); handle.title = t('move_hint');
    drag(handle, el, card);
    const title = element('div'); title.append(element('small', kindTitle(card.kind)), element('h2', card.title)); heading.append(handle, title);
    const body = element('div', undefined, 'canvas-card-body');
    if (card.body) body.append(element('p', card.body));
    if (card.kind === 'checklist') (card.items || []).forEach((item, index) => {
      const label = element('label', undefined, 'canvas-check'); const checkbox = element('input'); checkbox.type = 'checkbox'; checkbox.checked = item.checked;
      checkbox.addEventListener('change', () => { card.items[index].checked = checkbox.checked; changed(); }); label.append(checkbox, element('span', item.text)); body.append(label);
    });
    if (card.kind === 'link') body.append(link(card.url, card.url));
    if (card.kind === 'content') { const ref = element('div'); ref.dataset.reference = card.content_id || ''; body.append(ref); }
    if (card.kind === 'widget') { body.dataset.widgetId = card.widget_id; widgetBody(body, card.widget_id); }
    operations?.cardBody(body, card);
    const footer = element('footer', undefined, 'canvas-card-footer');
    const actions = element('div', undefined, 'canvas-card-actions'); if (card.kind !== 'result') actions.append(button(t('edit'), () => editCard(card.id)));
    lifecycle?.cardActions(actions,card);
    const remove = button(t('remove'), () => { if (!confirm(t('remove_confirm'))) return; documentState.cards = documentState.cards.filter(c => c.id !== card.id); documentState.edges = CanvasGraph.withoutNode(documentState.edges, card.id); changed(); render(); root.querySelector('[data-action=add]').focus(); }); remove.dataset.remove = ''; actions.append(remove);
    const moves = element('div', undefined, 'canvas-move-controls');
    [['left', '←', -24, 0], ['up', '↑', 0, -24], ['down', '↓', 0, 24], ['right', '→', 24, 0]].forEach(([key, symbol, dx, dy]) => { const control = button(symbol, () => move(card, dx, dy, el)); control.dataset.direction = key; control.title = t(key); control.setAttribute('aria-label', `${t(key)}: ${card.title}`); moves.append(control); });
    footer.append(actions, moves); el.append(heading); const identity=inbound?.cardHeader(card);if(identity)el.append(identity);el.append(body,footer); return el;
  }
  function render() {
    resizeCards.disconnect();
    const cards = mobile.matches || view !== 'flow' ? [...documentState.cards].sort(cardOrder) : documentState.cards;
    cardsEl.replaceChildren(...cards.map(cardView));
    root.querySelector('.canvas-empty').hidden = cards.length !== 0;
    root.querySelector('[data-count]').textContent = `${cards.length} ${t('count')}`;
    updateBounds(); refreshReferences(); operations?.render();
    normalizePositions();
    for(const el of cardsEl.children)resizeCards.observe(el);
    revealCardHash();
  }
  function placeNewCard(card) {
    const existing = documentState.cards.filter(c => c.id !== card.id).map(c => ({...c, height: Math.max(200, cardsEl.querySelector(`[data-card-id="${c.id}"]`)?.offsetHeight || 520)}));
    let best = null;
    for (let column = 0; column < 30; column++) {
      const x = 24 + column * 336;
      if (x > 10000) break;
      let y = 24;
      for (const other of existing.filter(c => c.x < x + 324 && c.x + 324 > x).sort((a, b) => a.y - b.y)) {
        if (other.y < y + 520 && other.y + other.height + 24 > y) y = other.y + other.height + 24;
      }
      y = Math.ceil(y / 24) * 24;
      if (y <= 10000 && (!best || y < best.y)) best = {x, y};
      if (column >= 2 && best) break;
    }
    if (best) { card.x = best.x; card.y = best.y; const el = cardsEl.querySelector(`[data-card-id="${card.id}"]`); if (el) place(el, card); updateBounds(); }
  }
  function editCard(id = null) {
    if (!documentState || (!id && documentState.cards.length >= 100)) { status('limit'); return; }
    editID = id; newKind = root.querySelector('#canvas-kind').value;
    const card = id ? documentState.cards.find(c => c.id === id) : {kind: newKind};
    form.reset();
    form.elements.title.value = card.title || kindTitle(card.kind);
    form.elements.body.value = card.body || '';
    form.elements.items.value = (card.items || []).map(item => item.text).join('\n');
    form.elements.url.value = card.url || '';
    form.elements.content_query.value = '';
    form.elements.content_id.replaceChildren();
    if (card.content_id) { const option = element('option', documentState.references?.[card.content_id]?.title || t('unavailable')); option.value = card.content_id; option.selected = true; form.elements.content_id.append(option); }
    if (card.kind === 'content') searchContent(card.content_id);
    if (card.widget_id) form.elements.widget_id.value = card.widget_id;
    root.querySelectorAll('[data-field]').forEach(el => { el.hidden = el.dataset.field !== card.kind && !(el.dataset.field === 'items' && card.kind === 'checklist') && !(el.dataset.field === 'url' && card.kind === 'link') && !(el.dataset.field === 'body' && ['note', 'link', 'content', 'prompt'].includes(card.kind)); });
    operations?.editCard(form, card);
    root.querySelector('[data-form-error]').hidden = true;
    root.querySelector('#canvas-dialog-title').textContent = `${id ? t('edit') : t('add')} · ${kindTitle(card.kind)}`;
    dialog.showModal(); form.elements.title.focus(); form.elements.title.select();
  }
  let searchSerial = 0, searchTimer;
  async function searchContent(selected = '') {
    const ticket = ++searchSerial;
    const message = root.querySelector('[data-search-status]');
    message.textContent = t('loading');
    try {
      const response = await request(`/admin/canvases/${encodeURIComponent(root.dataset.id)}/references?q=${encodeURIComponent(form.elements.content_query.value)}`);
      if (!response.ok) throw new Error('search');
      const data = await response.json();
      if (ticket !== searchSerial) return;
      const select = form.elements.content_id;
      const keep = selected || select.value;
      const options = (data.items || []).map(item => { const option = element('option', item.title); option.value = item.id; return option; });
      const existing = [...select.options].find(option => option.value === keep);
      if (existing && !options.some(option => option.value === keep)) options.unshift(existing);
      select.replaceChildren(...options);
      if (keep) select.value = keep;
      message.textContent = options.length ? '' : t('no_results');
    } catch (_) { if (ticket === searchSerial) message.textContent = t('search_error'); }
  }
  form.elements.content_query.addEventListener('input', () => { clearTimeout(searchTimer); searchTimer = setTimeout(() => searchContent(), 250); });
  form.addEventListener('submit', event => {
    event.preventDefault();
    const prior = editID ? documentState.cards.find(c => c.id === editID) : null;
    const kind = prior?.kind || newKind;
    const card = prior ? structuredClone(prior) : {id: crypto.randomUUID(), kind, x: 24 + (documentState.cards.length % 3) * 336, y: 24 + Math.floor(documentState.cards.length / 3) * 360};
    card.title = form.elements.title.value.trim(); card.body = ['note', 'link', 'content', 'prompt'].includes(kind) ? form.elements.body.value : '';
    let valid = card.title && bytes(card.title) <= 120 && bytes(card.body) <= 4000;
    if (kind === 'link') { card.url = form.elements.url.value.trim(); valid = valid && (/^https?:\/\//i.test(card.url) || card.url.startsWith('/')) && !!safeURL(card.url) && bytes(card.url) <= 2048; }
    if (kind === 'content') { card.content_id = form.elements.content_id.value.trim().toUpperCase(); valid = valid && (!card.content_id || /^[0-9A-HJKMNP-TV-Z]{26}$/.test(card.content_id)); }
    if (kind === 'widget') { card.widget_id = form.elements.widget_id.value; valid = valid && !!card.widget_id; }
    if (kind === 'checklist') {
      const previous = [...(prior?.items || [])];
      card.items = form.elements.items.value.split('\n').map(text => text.trim()).filter(Boolean).map(text => { const idx = previous.findIndex(item => item.text === text); return {text, checked: idx >= 0 ? previous.splice(idx, 1)[0].checked : false}; });
      valid = valid && card.items.length > 0 && card.items.length <= 30 && card.items.every(item => bytes(item.text) <= 300);
    }
    if (operations) valid = operations.readCard(form, card) && valid;
    if (!valid) { const error = root.querySelector('[data-form-error]'); error.textContent = t('invalid'); error.hidden = false; return; }
    if (prior) documentState.cards = documentState.cards.map(c => c.id === card.id ? card : c); else documentState.cards.push(card);
    render(); if (!prior) placeNewCard(card); changed(); dialog.close();
    cardsEl.querySelector(`[data-card-id="${card.id}"] .canvas-handle`)?.focus({preventScroll: true});
  });
  root.querySelectorAll('[data-action=cancel]').forEach(el => el.addEventListener('click', () => dialog.close()));
  root.querySelector('[data-action=add]').addEventListener('click', () => editCard());
  root.querySelector('[data-action=retry]').addEventListener('click', () => root.querySelector('.canvas-state').dataset.state === 'load_error' ? load() : save());
  root.querySelector('[data-action=reload]').addEventListener('click', () => { if (confirm(t('reload_confirm'))) { clearTimeout(timer); load(); } });
  titleInput.addEventListener('input', () => { if (documentState) { documentState.title = titleInput.value; changed(); } });
  async function leave(href) { if (await save()) location.assign(href); }
  root.querySelector('[data-action=close]').addEventListener('click', () => leave('/admin'));
  root.querySelector('[data-canvas-leave]').addEventListener('click', event => { event.preventDefault(); leave('/admin/canvases'); });
  window.addEventListener('beforeunload', event => { if (dirty()) { event.preventDefault(); event.returnValue = ''; } });
  mobile.addEventListener('change', () => { if (documentState) render(); });
  operations = window.CanvasOperations?.create({root, t, element, button, request, csrf, form, surface, cardsEl, document: () => documentState, applyPreset: data => { if (!documentState || documentState.version !== 1 || documentState.cards.length || documentState.edges.length) return false; documentState.cards = data.cards; documentState.edges = data.edges; changed(); render(); return true; }, changed, render, save, reload: async () => { if (dirty() && !confirm(t('reload_confirm'))) return false; clearTimeout(timer); await load(); return true; }});
  root.querySelectorAll('[data-canvas-view]').forEach(button=>button.addEventListener('click',()=>selectView(button.dataset.canvasView)));
  root.querySelector('[data-action=arrange]').addEventListener('click',()=>{
    if(!documentState||view!=='flow')return;
    const positions=CanvasLayout.arrange(documentState.cards,documentState.edges,heights(),documentState.provenance||{});
    documentState.cards.forEach(card=>Object.assign(card,positions[card.id]));
    changed();render();
  });
  inbound = window.CanvasInbound?.create({root,t,element,button,request,csrf,save,changed,editCard,document:()=>documentState});
  lifecycle = window.CanvasLifecycle?.create({root,t,element,button,request,csrf,save,document:()=>documentState});
  selectView(view);
  load();
})();
