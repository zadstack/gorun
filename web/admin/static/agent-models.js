(() => {
  function init() {
    document.querySelectorAll('[data-agent-model-form]').forEach(form => {
      if (form.dataset.bound) return;
      form.dataset.bound = '1';
      const profile = form.elements.connection_id;
      const version = form.elements.connection_version;
      const model = form.elements.model;
      const mode = form.elements.mode;
      const button = form.querySelector('[data-agent-model-discover]');
      const status = form.querySelector('[data-agent-model-status]');
      let sequence = 0, controller;
      const option = () => profile.selectedOptions[0];
      const update = () => {
        button.disabled = !profile.value;
        form.querySelectorAll('[data-agent-mode-help]').forEach(el => {
          el.hidden = el.dataset.agentModeHelp !== mode.value;
        });
      };
      const placeholder = () => model.replaceChildren(new Option(form.dataset.empty, ''));
      mode.addEventListener('change', update);
      profile.addEventListener('change', () => {
        sequence++;
        controller?.abort();
        version.value = option()?.dataset.version || '';
        placeholder();
        status.textContent = profile.value ? form.dataset.refresh : '';
        update();
      });
      button.addEventListener('click', async () => {
        const selected = option();
        if (!profile.value || !selected) return;
        const ticket = ++sequence;
        controller?.abort();
        controller = new AbortController();
        const expectedID = profile.value, expectedVersion = selected.dataset.version;
        const current = model.value;
        button.disabled = true;
        status.textContent = form.dataset.loading;
        const body = new URLSearchParams({
          _csrf: form.elements._csrf.value,
          id: expectedID, version: expectedVersion, kind: 'ai',
          provider: selected.dataset.provider, endpoint: selected.dataset.endpoint
        });
        try {
          const response = await fetch('/admin/settings/connections/models', {
            method: 'POST', body, credentials: 'same-origin',
            signal: controller.signal, headers: {Accept: 'application/json'}
          });
          const data = await response.json();
          if (ticket !== sequence) return;
          if (!response.ok) {
            status.textContent = typeof data.message === 'string' ? data.message : form.dataset.error;
            return;
          }
          if (data.profile_id !== expectedID || data.version !== expectedVersion || !Array.isArray(data.models) || data.models.length > 1000) {
            throw new Error('invalid');
          }
          const excluded = data.excluded_count === undefined ? 0 : data.excluded_count;
          if (!Number.isInteger(excluded) || excluded < 0 || excluded > 1000 || data.models.length + excluded > 1000) throw new Error('invalid');
          placeholder();
          data.models.forEach(row => {
            if (typeof row.id !== 'string' || typeof row.label !== 'string') throw new Error('invalid');
            model.add(new Option(row.label, row.id));
          });
          model.value = data.models.some(row => row.id === current) ? current : '';
          version.value = expectedVersion;
          status.textContent = `${data.models.length} · ${form.dataset.ready}${excluded ? ` · ${form.dataset.excluded} ${excluded}` : ''}`;
        } catch (error) {
          if (ticket === sequence && error.name !== 'AbortError') status.textContent = form.dataset.error;
        } finally {
          if (ticket === sequence) update();
        }
      });
      update();
    });
  }
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
  else init();
  document.addEventListener('htmx:afterSwap', init);
})();
