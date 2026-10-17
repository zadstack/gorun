(() => {
  document.querySelectorAll('[data-connection-setup]').forEach(form => {
    const box = form.querySelector('[data-model-discovery]');
    if (!box) return;
    const model = form.elements.model, select = box.querySelector('[data-model-choice]'), button = box.querySelector('[data-model-fetch]'), status = box.querySelector('[data-model-status]');
    let rows = [], generation = 0, pending;
    const fields = ['id', 'version', 'kind', 'provider', 'endpoint', 'token', 'clear_secret'];
    const snapshot = () => fields.map(name => { const el = form.elements[name]; return [name, !el ? '' : el.type === 'checkbox' ? (el.checked ? '1' : '') : el.value]; });
    const populate = () => {
      const value = model.value.trim();
      select.replaceChildren(new Option(box.dataset.choose, ''));
      if (value && !rows.some(row => row.id === value)) select.add(new Option(value, value));
      rows.forEach(row => select.add(new Option(row.label === row.id ? row.id : `${row.label} — ${row.id}`, row.id)));
      select.value = value;
    };
    const invalidate = () => {
      generation++; if (pending) pending.abort(); pending = null;
      rows = []; populate(); button.disabled = false; box.removeAttribute('aria-busy'); status.textContent = box.dataset.changed;
    };
    fields.forEach(name => { const el = form.elements[name]; if (el) ['input', 'change'].forEach(event => el.addEventListener(event, invalidate)); });
    model.addEventListener('input', populate);
    select.addEventListener('change', () => { if (select.value) { model.value = select.value; model.dispatchEvent(new Event('input', {bubbles: true})); } });
    button.addEventListener('click', async () => {
      const current = ++generation, source = snapshot(), signature = JSON.stringify(source);
      if (pending) pending.abort(); pending = new AbortController();
      const request = new URLSearchParams(source); request.set('_csrf', form.elements._csrf.value);
      button.disabled = true; box.setAttribute('aria-busy', 'true'); status.textContent = box.dataset.loading;
      try {
        const response = await fetch('/admin/settings/connections/models', {method: 'POST', credentials: 'same-origin', headers: {'Content-Type': 'application/x-www-form-urlencoded'}, body: request, signal: pending.signal});
        const data = await response.json();
        if (current !== generation || signature !== JSON.stringify(snapshot())) return;
        if (!response.ok) { status.textContent = typeof data.message === 'string' ? data.message : box.dataset.network; return; }
        if (!Array.isArray(data.models) || data.models.length > 1000 || data.profile_id !== form.elements.id.value || data.version !== form.elements.version.value || !data.models.every(row => typeof row.id === 'string' && typeof row.label === 'string')) throw Error('Invalid catalog');
        const excluded = data.excluded_count === undefined ? 0 : data.excluded_count;
        if (!Number.isInteger(excluded) || excluded < 0 || excluded > 1000 || data.models.length + excluded > 1000) throw Error('Invalid catalog count');
        rows = data.models; populate(); status.textContent = `${box.dataset.loaded} ${rows.length}${excluded ? ` · ${box.dataset.excluded} ${excluded}` : ''}`;
      } catch (error) { if (current === generation && error.name !== 'AbortError') status.textContent = box.dataset.network; }
      finally { if (current === generation) { button.disabled = false; box.removeAttribute('aria-busy'); pending = null; } }
    });
    populate();
  });
})();
