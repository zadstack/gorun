(() => {
  const root = document.querySelector('[data-edge-instances]');
  if (!root) return;
  const labels = new Map(Array.from(root.querySelectorAll('[data-edge-text]'), node => [node.dataset.edgeText, node.textContent]));
  const text = key => labels.get(key) || labels.get('unknown');
  const message = root.querySelector('[data-edge-message]');
  const rows = root.querySelector('[data-edge-rows]');
  let revision = root.dataset.revision;
  let busy = false;
  const status = (text) => { message.textContent = text; };
  async function request(path, body) {
    const response = await fetch('/api/v1/site/edge/instances' + path, {
      method: body === undefined ? 'GET' : 'POST', credentials: 'same-origin',
      headers: {'Accept':'application/json','Content-Type':'application/json','X-CSRF-Token':document.querySelector('meta[name="csrf-token"]').content},
      body: body === undefined ? undefined : JSON.stringify(body)
    });
    const result = await response.json();
    if (!response.ok || result.ok === false) throw new Error(root.dataset.error + ' (' + response.status + ')');
    return result;
  }
  async function refresh() {
    const result = await request(''); revision = result.revision;
    rows.replaceChildren();
    for (const record of result.data) {
      const row = document.createElement('tr');
      for (const value of [record.label, record.origin, text('edge_state.' + record.state) + (record.error ? ' · ' + text('identity_failed') : ''), record.checked_at || '—']) {
        const cell = document.createElement('td'); cell.textContent = value; row.append(cell);
      }
      const cell = document.createElement('td'); const button = document.createElement('button');
      button.type = 'button'; button.dataset.edgeCheck = record.id; button.textContent = root.dataset.check;
      cell.append(button); row.append(cell); rows.append(row);
    }
    if (!result.data.length) {const row = rows.insertRow(); const cell = row.insertCell(); cell.colSpan = 5; cell.textContent = root.dataset.empty;}
  }
  async function action(task) {
    if (busy) return; busy = true;
    root.querySelectorAll('button').forEach(button => { button.disabled = true; });
    try { await task(); status(root.dataset.done); } catch (error) { status(error.message); }
    finally { busy = false; root.querySelectorAll('button').forEach(button => { button.disabled = false; }); }
  }
  const plans = root.querySelector('[data-edge-plans]');
  function field(list, label, content) {
    if (!content) return;
    const term = document.createElement('dt'); term.textContent = text(label);
    const value = document.createElement('dd'); value.textContent = content;
    list.append(term, value);
  }
  function planFields(plan) {
    const list = document.createElement('dl'); list.className = 'onboarding-plan-fields';
    field(list, 'account', plan.request.account_id);
    field(list, 'worker', plan.resources.worker);
    field(list, 'database', plan.resources.d1_name);
    field(list, 'bucket', plan.resources.r2_bucket);
    field(list, 'origin', plan.resources.origin);
    field(list, 'expires', plan.expires_at);
    field(list, 'recorded_state', text('plan_state.' + plan.state));
    field(list, 'permissions', (plan.inspection.required_permissions || []).join(', '));
    if (plan.step) field(list, 'step', text('step.' + plan.step));
    return list;
  }
  function technicalDetails(plan) {
    const details = document.createElement('details');
    const summary = document.createElement('summary'); summary.textContent = text('technical');
    const list = document.createElement('dl'); list.className = 'onboarding-plan-fields';
    field(list, 'artifact', plan.artifact_sha);
    field(list, 'job', plan.job_id);
    field(list, 'instance', plan.instance_id);
    details.append(summary, list);
    return details;
  }
  function approvalControl(plan, article) {
    const label = document.createElement('label');
    const checkbox = document.createElement('input'); checkbox.type = 'checkbox'; checkbox.required = true;
    label.append(checkbox, document.createTextNode(plans.dataset.approve));
    const button = document.createElement('button'); button.type = 'button'; button.textContent = plans.dataset.execute;
    button.addEventListener('click', () => {
      if (!checkbox.checked) { checkbox.reportValidity(); checkbox.focus(); return; }
      action(async () => {
        await request('/plans/' + encodeURIComponent(plan.id) + '/execute', {approved:true,fingerprint:plan.fingerprint});
        await refreshPlans();
      });
    });
    article.append(label, button);
  }
  function showPlans(items) {
    plans.replaceChildren();
    for (const plan of items) {
      const article = document.createElement('section');
      const heading = document.createElement('h3'); heading.textContent = plan.request.label;
      article.append(heading, planFields(plan));
      if (plan.error) {const error = document.createElement('p'); error.textContent = text('plan_error'); article.append(error);}
      if (plan.state === 'planned') approvalControl(plan, article);
      article.append(technicalDetails(plan));
      plans.append(article);
    }
  }
  async function refreshPlans() { const result = await request('/plans'); showPlans(result.data); }
  root.querySelector('[data-edge-plans-refresh]').addEventListener('click', () => action(refreshPlans));
  root.querySelector('[data-edge-plan]').addEventListener('submit', event => {
    event.preventDefault(); const values = Object.fromEntries(new FormData(event.currentTarget));
    action(async () => {const result = await request('/plans', {request:values,registry_revision:revision}); showPlans([result.data]);});
  });
  // Capability discovery reads local adapter availability; it never inspects a provider.
  request('/capabilities').then(result => {root.querySelector('[data-edge-create]').hidden = !result.data.create || !root.dataset.accountId;}).catch(error => status(error.message));
  root.querySelector('[data-edge-connect]').addEventListener('submit', event => {
    event.preventDefault(); const form = event.currentTarget;
    const values = Object.fromEntries(new FormData(form));
    action(async () => {try { await request('', {...values, revision}); await refresh(); form.reset(); } finally {form.elements.key.value = '';}});
  });
  rows.addEventListener('click', event => {
    const button = event.target.closest('[data-edge-check]'); if (!button) return;
    action(async () => {await request('/' + encodeURIComponent(button.dataset.edgeCheck) + '/check', {revision}); await refresh();});
  });
})();
