(() => {
  const panel = document.querySelector('[data-review-sidebar]');
  if (!panel) return;
  const status = panel.querySelector('[data-review-status]');
  const select = panel.querySelector('select');
  const previewButton = panel.querySelector('[data-review-preview]');
  const approval = panel.querySelector('[data-review-approval]');
  const consent = panel.querySelector('[data-review-consent]');
  const enqueueButton = panel.querySelector('[data-review-enqueue]');
  let preview;
  let loaded = false;
  const api = async (path, body) => {
    const response = await fetch('/api/v1/workagents/' + path, {
      method: body ? 'POST' : 'GET',
      headers: {'Content-Type': 'application/json', 'X-CSRF-Token': panel.dataset.csrf},
      body: body ? JSON.stringify(body) : undefined,
    });
    const result = await response.json();
    if (!response.ok) throw new Error(result.error?.message || panel.dataset.failed);
    return result.data;
  };
  panel.addEventListener('toggle', async () => {
    if (!panel.open || loaded) return;
    previewButton.disabled = true;
    try {
      const agents = await api('agents');
      for (const agent of agents) {
        if (agent.execution_kind !== 'text' || !agent.enabled || !agent.provider_configured) continue;
        const option = document.createElement('option');
        option.value = agent.id;
        option.textContent = agent.name;
        select.append(option);
      }
      loaded = true;
      status.textContent = select.options.length > 1 ? '' : panel.dataset.unavailable;
    } catch (error) { status.textContent = error.message; }
    finally { previewButton.disabled = false; }
  });
  select.addEventListener('change', () => { preview = undefined; approval.hidden = true; consent.checked = false; });
  previewButton.addEventListener('click', async () => {
    previewButton.disabled = true;
    approval.hidden = true;
    consent.checked = false;
    try {
      preview = await api('design-reviews/preview', {page_id: panel.dataset.pageId, agent_id: select.value});
      panel.querySelector('[data-review-context]').textContent = preview.context;
      panel.querySelector('[data-review-destination]').textContent = preview.destination;
      approval.hidden = false;
      status.textContent = '';
    } catch (error) { status.textContent = error.message; }
    finally { previewButton.disabled = false; }
  });
  enqueueButton.addEventListener('click', async () => {
    if (!preview || !consent.checked) { consent.reportValidity(); return; }
    enqueueButton.disabled = true;
    try {
      await api('design-reviews', {preview_id: preview.id, approved: true, request_key: preview.id});
      status.textContent = panel.dataset.queued;
      approval.hidden = true;
    } catch (error) { status.textContent = error.message; }
    finally { enqueueButton.disabled = false; }
  });
  document.addEventListener('keydown', (event) => {
    if (event.key === 'Escape' && panel.open) {
      panel.open = false;
      panel.querySelector('summary').focus();
    }
  });
})();
