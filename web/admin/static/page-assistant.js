(() => {
  'use strict';
  const selectedPortion = (source, start, end) => {
    const text = source.slice(start, end);
    return text && source.split(text).length === 2 ? text : null;
  };
  if (typeof module !== 'undefined') module.exports = {selectedPortion};
  if (typeof document === 'undefined') return;
  const dialog = document.getElementById('page-assistant-dialog');
  if (!dialog) return;
  const el = name => dialog.querySelector(`[data-pa-${name}]`), labels = dialog.dataset;
  let meta = null, studio = null, review = null, input = null, busy = false, dispatched = false, conflicted = false, opener = null;
  const url = () => `/admin/x/page-assistant/${encodeURIComponent(meta.id)}`;
  const studioURL = () => `/admin/x/studio/${encodeURIComponent(meta.id)}`;
  const textProviders = () => (studio?.providers || []).filter(p => p.kind === 'text');
  function status(key, error = false) { el('status').textContent = labels[key] || labels.error; el('status').dataset.error = String(error); }
  function option(value, title) { const item = document.createElement('option'); item.value = value; item.textContent = title; return item; }
  function invalidate() { review = null; input = null; dispatched = false; el('review').hidden = true; el('consent').checked = false; paint(); }
  function paint() {
    const edit = el('mode').value === 'edit';
    el('field-label').hidden = !edit;
    el('apply-panel').hidden = !edit || !meta;
    el('apply').disabled = busy || conflicted || !meta?.can_apply || !el('apply-consent').checked;
    el('send').disabled = busy || dispatched || !review || !el('consent').checked;
    el('preview').disabled = busy || !textProviders().length || !el('prompt').value.trim();
    el('refresh').disabled = busy || !meta;
    el('close').disabled = busy;
    dialog.setAttribute('aria-busy', String(busy));
  }
  async function request(path, data) {
    const controller = new AbortController(), timer = setTimeout(() => controller.abort(), 20000);
    try {
      const response = await fetch(path, {method: data ? 'POST' : 'GET', redirect: 'error', credentials: 'same-origin', signal: controller.signal, headers: data ? {'Content-Type': 'application/json', 'X-CSRF-Token': document.querySelector('meta[name=csrf-token]')?.content || ''} : {}, body: data ? JSON.stringify(data) : undefined});
      const result = await response.json();
      if (!response.ok) { const error = new Error(response.status === 409 ? 'conflict' : (result.error || 'error')); error.status = response.status; throw error; }
      return result;
    } finally { clearTimeout(timer); }
  }
  async function task(work) {
    if (busy) return;
    busy = true; dialog.querySelectorAll('input,select,textarea,button').forEach(n => n.disabled = true); paint();
    try { await work(); } catch (error) { if (error.message === 'conflict') conflicted = true; status(Object.hasOwn(labels, error.message) ? error.message : 'error', true); }
    finally { busy = false; dialog.querySelectorAll('input,select,textarea,button').forEach(n => n.disabled = false); paint(); }
  }
  function fieldChanged() {
    el('previous').value = meta?.[el('field').value] || '';
    el('replacement').value = '';
    el('apply-consent').checked = false;
    el('selection').hidden = el('field').value !== 'body';
    invalidate();
  }
  function populate() {
    el('page').textContent = meta.title;
    const editor = new URL(meta.editor_url, location.origin);
    if (editor.origin !== location.origin || !editor.pathname.startsWith('/admin/content/')) throw new Error('error');
    el('editor').href = editor.pathname;
    el('field').replaceChildren(...meta.fields.map(field => option(field, labels[field] || field)));
    el('provider').replaceChildren(...textProviders().map(p => option(p.id, p.id === 'native-text' ? labels.native : p.name)));
    const noPreset = el('preset').firstElementChild.cloneNode(true);
    el('preset').replaceChildren(noPreset, ...(studio.presets || []).filter(p => p.kind === 'text').map(p => option(p.id, p.name)));
    el('work').hidden = textProviders().length === 0;
    el('config').hidden = !studio.can_configure;
    el('published').hidden = !meta.published;
    el('builder').hidden = meta.fields.includes('body');
    el('result').hidden = true;
    el('answer').value = '';
    fieldChanged(); history();
    if (!textProviders().length) status('unconfigured'); else el('status').textContent = '';
  }
  function history() {
    const box = el('history'); box.replaceChildren();
    const items = (studio.items || []).filter(item => item.input?.kind === 'text');
    if (!items.length) { const p = document.createElement('p'); p.textContent = labels.empty; box.append(p); }
    for (const item of items) {
      const article = document.createElement('article'), title = document.createElement('p'), state = document.createElement('span');
      title.textContent = item.input.prompt;
      state.textContent = labels[`state${item.status[0]?.toUpperCase()}${item.status.slice(1)}`] || labels.notReady;
      article.append(title, state);
      if (item.status === 'ready') {
        const button = document.createElement('button'); button.type = 'button'; button.textContent = labels.use;
        button.addEventListener('click', () => { el('answer').value = item.text || ''; el('result').hidden = false; if (el('mode').value === 'edit') { el('replacement').value = item.text || ''; el('apply-consent').checked = false; } paint(); el('result').scrollIntoView({block: 'nearest'}); });
        article.append(button);
      }
      box.append(article);
    }
  }
  document.addEventListener('click', event => {
    const button = event.target.closest('[data-page-assistant]');
    if (!button || busy) return;
    const id = button.dataset.pageAssistant;
    if (!/^[0-9A-Z]{26}$/.test(id)) return;
    opener = button; meta = {id}; studio = null; conflicted = false; review = null; input = null; dispatched = false;
    el('work').hidden = true; el('result').hidden = true; el('apply-panel').hidden = true; el('history').replaceChildren(); el('config').hidden = true;
    el('mode').value = 'question'; el('prompt').value = ''; el('page').textContent = ''; el('editor').hidden = true;
    dialog.showModal(); status('loading');
    task(async () => { [meta, studio] = await Promise.all([request(url()), request(studioURL())]); el('editor').hidden = false; populate(); });
  });
  el('close').addEventListener('click', () => { if (!busy) dialog.close(); });
  dialog.addEventListener('cancel', event => { if (busy) { event.preventDefault(); status('pendingClose'); } });
  dialog.addEventListener('close', () => opener?.focus());
  el('mode').addEventListener('change', () => { fieldChanged(); paint(); });
  el('field').addEventListener('change', fieldChanged);
  el('provider').addEventListener('change', invalidate);
  el('prompt').addEventListener('input', invalidate);
  el('preset').addEventListener('change', () => { const preset = studio.presets.find(p => p.id === el('preset').value); if (preset) el('prompt').value = preset.prompt; invalidate(); });
  el('consent').addEventListener('change', paint); el('apply-consent').addEventListener('change', paint);
  el('replacement').addEventListener('input', () => { el('apply-consent').checked = false; paint(); });
  el('selection').addEventListener('click', () => {
    const area = el('previous'), portion = selectedPortion(area.value, area.selectionStart, area.selectionEnd);
    if (!portion || meta.body.split(portion).length !== 2) { status('selection', true); return; }
    area.value = portion; el('apply-consent').checked = false; invalidate();
  });
  el('preview').addEventListener('click', () => task(async () => {
    const editing = el('mode').value === 'edit';
    input = {kind: 'text', provider: el('provider').value, preset: el('preset').value, title: meta.title, text: editing ? el('previous').value : meta.body, prompt: `${editing ? labels.editTask + ' (' + el('field').value + ')' : labels.questionTask}\n${el('prompt').value}`};
    review = await request(studioURL() + '/preview', input); dispatched = false; el('payload').textContent = JSON.stringify(review.arguments, null, 2); el('review').hidden = false; el('consent').checked = false; el('review').scrollIntoView({block: 'nearest'});
  }));
  el('send').addEventListener('click', () => task(async () => {
    if (!review || dispatched || !el('consent').checked) return;
    dispatched = true;
    const candidate = await request(studioURL() + '/generate', {...input, review_hash: review.review_hash, request_id: review.request_id, reviewed: true});
    studio.items = [candidate, ...studio.items.filter(item => item.id !== candidate.id)]; history(); status('sent');
  }));
  el('refresh').addEventListener('click', () => task(async () => { studio = await request(studioURL()); history(); }));
  el('reload').addEventListener('click', () => task(async () => { meta = await request(url()); conflicted = false; populate(); }));
  el('apply').addEventListener('click', () => task(async () => {
    if (!meta.can_apply || conflicted || !el('apply-consent').checked) return;
    meta = await request(url() + '/apply', {version: meta.version, field: el('field').value, previous: el('previous').value, replacement: el('replacement').value, reviewed: true});
    el('page').textContent = meta.title; fieldChanged(); status('saved');
    const card = opener?.closest('.ov-page'), frame = card?.querySelector('iframe');
    if (frame) frame.src = frame.src;
  }));
})();
