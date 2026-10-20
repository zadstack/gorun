(() => {
  'use strict';
  const root = document.querySelector('[data-canvas-ai-suggestions]');
  if (!root) return;
  const csrf = root.querySelector('[name=_csrf]')?.value || document.querySelector('meta[name=csrf-token]')?.content || '';
  const labels = {};
  root.querySelectorAll('[data-ai-label]').forEach(el => { labels[el.dataset.aiLabel] = el.textContent; });
  const t = key => labels[key] || key;
  const status = root.querySelector('[data-ai-status]');
  const grid = root.querySelector('[data-ai-grid]');
  const target = root.querySelector('[data-ai-target]');
  const refresh = root.querySelector('[data-ai-refresh]');
  const consentWrap = root.querySelector('[data-ai-consent-wrap]');
  const consent = root.querySelector('[data-ai-consent]');
  const say = text => { if (status) status.textContent = text || ''; };

  async function request(path, body) {
    const options = { method: body ? 'POST' : 'GET', credentials: 'same-origin', cache: 'no-store', headers: { Accept: 'application/json', 'X-CSRF-Token': csrf } };
    if (body) {
      options.headers['Content-Type'] = 'application/json';
      options.body = JSON.stringify(body);
    }
    return fetch('/admin/x/canvases/ai-suggestions' + path, options);
  }

  function render(items, requestId) {
    grid.replaceChildren();
    if (!items?.length) return;
    items.forEach((item, index) => {
      const card = document.createElement('article');
      card.className = 'canvas-ai-card';
      card.innerHTML = `<h3></h3><p></p><small></small><button type="button" class="canvas-primary"></button>`;
      card.querySelector('h3').textContent = item.title;
      card.querySelector('p').textContent = item.summary;
      card.querySelector('small').textContent = `${item.cards} ${t('cards')}`;
      const btn = card.querySelector('button');
      btn.textContent = t('apply');
      btn.addEventListener('click', async () => {
        btn.disabled = true;
        const generation = epoch;
        say('');
        try {
          const response = await request('/apply', { request_id: requestId, index, title: item.title });
          if (!response.ok) throw new Error();
          const data = await response.json();
          if (generation !== epoch) return;
          if (/^\/admin\/canvases\/[0-9A-HJKMNP-TV-Z]{26}$/.test(data.href)) location.assign(data.href);
        } catch {
          if (generation === epoch) say(t('error'));
        } finally {
          btn.disabled = false;
        }
      });
      grid.appendChild(card);
    });
  }

  async function loadCached() {
    const generation = epoch;
    try {
      const response = await request('');
      if (!response.ok) throw new Error();
      const data = await response.json();
      if (generation !== epoch) return;
      root.hidden = false;
      if (!data.configured) {
        say(t('not_configured'));
        refresh.hidden = true;
        consentWrap.hidden = true;
        const link = document.createElement('a');
        link.href = '/admin/settings/ai';
        link.textContent = t('configure');
        link.className = 'canvas-ai-configure';
        if (data.can_configure) grid.replaceChildren(link);
        else grid.replaceChildren();
        return;
      }
      refresh.hidden = false;
      consentWrap.hidden = false;
      if (data.suggestions?.length) render(data.suggestions, data.request_id);
      else grid.replaceChildren();
    } catch {
      say(t('error'));
    }
  }

  const review = root.querySelector('[data-ai-review]');
  const confirm = root.querySelector('[data-ai-confirm]');
  const previewText = root.querySelector('[data-ai-preview]');
  let prepared = null, epoch = 0, busy = false;
  function invalidate() {
    epoch++; prepared = null; consent.checked = false; confirm.disabled = true;
    review.hidden = true; previewText.textContent = '';
  }
  target.addEventListener('change', invalidate);
  consent.addEventListener('change', () => { confirm.disabled = busy || !prepared || !consent.checked; });
  window.addEventListener('pagehide', () => { invalidate(); busy = false; refresh.disabled = false; target.disabled = false; });
  window.addEventListener('pageshow', event => { if (event.persisted) { invalidate(); loadCached(); } });
  refresh?.addEventListener('click', async () => {
    if (busy) return;
    invalidate(); const generation = epoch; busy = true; refresh.disabled = true; target.disabled = true;
    say(t('loading'));
    try {
      const locale = document.documentElement.lang === 'ar' ? 'ar' : 'en';
      const response = await request('/preview', {target_kind: target.value, locale});
      if (!response.ok) throw new Error();
      const data = await response.json(); if (generation !== epoch) return;
      prepared = {request_id:data.request_id, target_kind:data.target_kind, locale:data.locale};
      previewText.textContent = JSON.stringify({destination:data.destination, system:data.system, user:data.user, context:data.context, expires_at:data.expires_at}, null, 2);
      review.hidden = false; review.focus({preventScroll:true}); say('');
    } catch { if (generation === epoch) say(t('error')); }
    finally { if (generation === epoch) {busy = false; refresh.disabled = false; target.disabled = false;} }
  });
  confirm.addEventListener('click', async () => {
    if (busy || !prepared || !consent.checked) return;
    const input = {...prepared, reviewed:true}, generation = epoch;
    prepared = null; busy = true; confirm.disabled = true; refresh.disabled = true; target.disabled = true;
    say(t('loading'));
    try {
      const response = await request('/generate', input);
      if (!response.ok) throw new Error();
      const data = await response.json(); if (generation !== epoch) return;
      render(data.suggestions, data.request_id); say('');
    } catch { if (generation === epoch) say(t('error')); }
    finally { if (generation === epoch) {invalidate(); busy = false; refresh.disabled = false; target.disabled = false;} }
  });
  loadCached();
})();
