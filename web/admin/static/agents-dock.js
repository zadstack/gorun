(() => {
  'use strict';
  const root = document.querySelector('[data-agents-dock]');
  if (!root) return;
  const dialog = root.querySelector('dialog'), launcher = root.querySelector('[data-dock-open]');
  const list = root.querySelector('[data-dock-list]'), detail = root.querySelector('[data-dock-detail]');
  const status = root.querySelector('[data-dock-status]'), shortcuts = root.querySelector('[data-dock-shortcuts]');
  const labels = Object.fromEntries([...root.querySelectorAll('[data-label]')].map(el => [el.dataset.label, el.textContent]));
  let entries = [], generation = 0, selected = '', returnFocus, previousOverflow, active = true, activeTab = 'integrations';
  const el = (tag, text) => { const node = document.createElement(tag); if (text !== undefined) node.textContent = text; return node; };
  const identity = item => item.href;
  const title = item => item.title || labels.unnamed;
  const valid = item => item && typeof item.id === 'string' && typeof item.title === 'string' && typeof item.board === 'string' && /^\/admin\/canvases\/[A-Za-z0-9]+#card-[a-f0-9-]{36}$/i.test(item.href) && ['agent','provider'].includes(item.kind);
  function showDetail(item, focus = false) {
    const focusedTab = detail.contains(document.activeElement) ? document.activeElement.dataset.dockTab : '';
    selected = identity(item); detail.replaceChildren(); detail.hidden = false;
    const heading = el('h3', title(item)); heading.tabIndex = -1; detail.append(heading);
    detail.append(el('p', item.configured ? labels.configured : labels.setup));
    const tabs = el('div'); tabs.className = 'agents-dock-tabs'; tabs.setAttribute('role','tablist'); tabs.setAttribute('aria-label',labels.details);
    const panels = new Map(), buttons = new Map(), order = ['integrations','instructions','defaults'];
    const activate = (name, moveFocus = false) => {
      activeTab = name;
      for (const [id,button] of buttons) { button.setAttribute('aria-selected',String(id === name)); button.tabIndex = id === name ? 0 : -1; panels.get(id).hidden = id !== name; }
      if (moveFocus) buttons.get(name).focus();
    };
    for (const name of order) {
      const button = el('button',labels[name]); button.type = 'button'; button.id = 'agents-dock-tab-'+name;
      button.dataset.dockTab = name; button.setAttribute('role','tab'); button.setAttribute('aria-controls','agents-dock-panel-'+name);
      const panel = el('div'); panel.id = 'agents-dock-panel-'+name; panel.setAttribute('role','tabpanel'); panel.setAttribute('aria-labelledby',button.id); panel.tabIndex = 0;
      button.addEventListener('click',()=>activate(name));
      button.addEventListener('keydown',event=>{
        let index = order.indexOf(name);
        if (event.key === 'Home') index = 0;
        else if (event.key === 'End') index = order.length-1;
        else if (event.key === 'ArrowRight' || event.key === 'ArrowLeft') { const step = (event.key === 'ArrowRight' ? 1 : -1)*(document.documentElement.dir === 'rtl' ? -1 : 1); index = (index+step+order.length)%order.length; }
        else return;
        event.preventDefault(); activate(order[index],true);
      });
      buttons.set(name,button); panels.set(name,panel); tabs.append(button);
    }
    detail.append(tabs,...panels.values());
    const fields = el('dl');
    const field = (label, value) => { if (value) fields.append(el('dt', label), el('dd', value)); };
    field(labels.key, item.agent_id ? `${item.agent_name || ''} · ${item.agent_id}` : '');
    field(labels.model, item.model);
    field(labels.execution, labels[item.mode] || labels[item.kind === 'provider' ? 'provider' : 'external']);
    const integrations = panels.get('integrations'); integrations.append(fields);
    if (item.mode === 'external') integrations.append(el('p',labels.externalHelp));
    integrations.append(el('p',labels.limits));
    const setupLink = el('a',labels.integrationSetup); setupLink.href = item.href; integrations.append(setupLink);
    const instructions = panels.get('instructions');
    if (item.prompt) instructions.append(el('h4',labels.prompt),el('pre',item.prompt));
    else instructions.append(el('p',labels.unavailable));
    const defaults = panels.get('defaults'), identityFields = el('dl');
    identityFields.append(el('dt',labels.card),el('dd',item.id),el('dt',labels.canvas),el('dd',item.board));
    if (['text','image','audio','video'].includes(item.generation)) identityFields.append(el('dt',labels.output),el('dd',labels[item.generation]));
    defaults.append(identityFields,el('p',labels.defaultsHelp));
    activate(order.includes(activeTab) ? activeTab : order[0]);
    const link = el('a', labels.open); link.href = item.href; detail.append(link);
    for (const button of list.querySelectorAll('button')) button.setAttribute('aria-pressed', String(button.dataset.identity === selected));
    if (focus) { heading.focus(); heading.scrollIntoView({block:'nearest'}); }
    else if (focusedTab && buttons.has(focusedTab)) buttons.get(focusedTab).focus({preventScroll:true});
  }
  function render(data) {
    entries = Array.isArray(data.agents) ? data.agents.filter(valid) : [];
    list.replaceChildren(); shortcuts.replaceChildren();
    status.textContent = data.truncated ? labels.truncated : entries.length ? '' : labels.empty;
    entries.forEach((item,index) => {
      const button = el('button'); button.type = 'button'; button.dataset.identity = identity(item);
      button.setAttribute('aria-pressed', String(identity(item) === selected));
      button.append(el('strong', title(item)), el('small', `${item.board} · ${item.configured ? labels.configured : labels.setup}`));
      button.addEventListener('click', () => showDetail(item, true)); list.append(button);
      if (index < 5) {
        const shortcut = el('button', [...title(item)].slice(0,2).join('')); shortcut.type = 'button';
        shortcut.title = `${title(item)} · ${item.board}`; shortcut.setAttribute('aria-label', shortcut.title);
        shortcut.setAttribute('aria-haspopup','dialog'); shortcut.addEventListener('click', () => open(shortcut,identity(item))); shortcuts.append(shortcut);
      }
    });
    const current = entries.find(item => identity(item) === selected);
    if (current) showDetail(current); else { selected = ''; detail.hidden = true; detail.replaceChildren(); }
  }
  async function refresh() {
    const ownGeneration = ++generation;
    try {
      const response = await fetch('/admin/x/agents-dock', {credentials:'same-origin',cache:'no-store',headers:{Accept:'application/json'}});
      if (!active || ownGeneration !== generation) return;
      if (response.status === 403) { hide(); return; }
      if (!response.ok || !response.headers.get('content-type')?.includes('application/json')) throw new Error('unavailable');
      const data = await response.json();
      if (!active || ownGeneration !== generation) return;
      if (!data.available) { hide(); return; }
      root.hidden = false; document.body.classList.add('agents-dock-visible'); render(data);
    } catch (_) { if (active && ownGeneration === generation && dialog.open) status.textContent = labels.error; }
  }
  function close() { if (dialog.open) dialog.close(); }
  function hide() { close(); root.hidden = true; entries = []; render({agents:[]}); document.body.classList.remove('agents-dock-visible'); }
  function open(trigger, id = '') {
    returnFocus = trigger; selected = id;
    // closeSidebar() schedules focus back to the main menu. A handoff to this
    // modal closes only its state, keeping focus inside the new dialog.
    const shell = window.Alpine?.$data?.(document.body);
    if (shell?.sidebarOpen) shell.sidebarOpen = false;
    if (!dialog.open) { previousOverflow = document.body.style.overflow; document.body.style.overflow = 'hidden'; dialog.showModal(); }
    launcher.setAttribute('aria-expanded','true');
    const item = entries.find(entry => identity(entry) === id); if (item) showDetail(item);
    refresh();
  }
  launcher.addEventListener('click', () => open(launcher));
  root.querySelector('[data-dock-close]').addEventListener('click',close);
  dialog.addEventListener('click', event => { if (event.target === dialog) { const r=dialog.getBoundingClientRect(); if(event.clientX<r.left||event.clientX>r.right||event.clientY<r.top||event.clientY>r.bottom)close(); } });
  dialog.addEventListener('close', () => {
    ++generation; selected = ''; detail.hidden = true; detail.replaceChildren();
    launcher.setAttribute('aria-expanded','false'); document.body.style.overflow = previousOverflow || '';
    if (active) (returnFocus?.isConnected ? returnFocus : launcher).focus();
  });
  window.addEventListener('pagehide', () => { active = false; ++generation; hide(); });
  window.addEventListener('pageshow', event => { if (event.persisted) { active = true; refresh(); } });
  refresh();
})();
