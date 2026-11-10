(() => {
  'use strict';
  const menu = document.querySelector('[data-workspace-mobile-tools]');
  if (!menu) return;
  const slot = menu.querySelector('[data-workspace-mobile-slot]');
  const parts = [...document.querySelectorAll('.workspace-rail-tools,.workspace-rail-footer')];
  const homes = new Map(parts.map(part => [part, part.parentElement]));
  const mobile = matchMedia('(max-width:767px)');
  function placeTools() {
    for (const part of parts) (mobile.matches ? slot : homes.get(part)).append(part);
    menu.hidden = !mobile.matches || !parts.some(part => part.querySelector('a'));
    if (menu.hidden) menu.open = false;
  }
  mobile.addEventListener('change', placeTools);
  document.addEventListener('click', event => { if (!menu.contains(event.target)) menu.open = false; });
  menu.addEventListener('keydown', event => { if (event.key === 'Escape') { menu.open = false; menu.querySelector('summary').focus(); } });
  let controller;
  const selectors = ['.workspace-top-tools','.workspace-sidebar-tools','.workspace-rail-tools','.workspace-rail-footer','.workspace-tool-groups','[data-navigation-sections]','[data-workspace-context-navigation]'];
  // Swap every shortcut region from a rendered projection, keeping focus on the
  // same destination when the focused control was inside a replaced region.
  function applyProjection(doc) {
    const focused = document.activeElement;
    const restoreFocus = selectors.some(selector => document.querySelector(selector)?.contains(focused));
    const focusedHref = focused?.getAttribute('href');
    const focusedPin = focused?.matches('[data-sidebar-pin-add]') ? focused.closest('[data-nav-id]')?.dataset.navId : null;
    for (const selector of selectors) {
      const current = document.querySelector(selector), next = doc.querySelector(selector);
      if (current && next) current.replaceChildren(...[...next.childNodes].map(node => document.importNode(node, true)));
    }
    placeTools();
    if (restoreFocus) {
      let target;
      if (focusedPin) target = [...document.querySelectorAll('[data-nav-id]')].find(row => row.dataset.navId === focusedPin)?.querySelector('[data-sidebar-pin-add]');
      if (!target && focusedHref) target = [...document.querySelectorAll('a[href]')].find(link => link.getAttribute('href') === focusedHref && link.checkVisibility());
      if (!target?.checkVisibility()) target = document.querySelector('[data-navigation-sections] a');
      target?.focus({preventScroll:true});
    }
  }
  async function refreshTools(event) {
    controller?.abort();
    controller = undefined;
    // The pin refresh already rendered the regions for this page: apply them
    // synchronously so the pin buttons painted next see the new rows.
    const html = event?.detail?.navigationHTML;
    if (typeof html === 'string' && html) {
      applyProjection(new DOMParser().parseFromString(`<!doctype html><body>${html}</body>`, 'text/html'));
      return;
    }
    const request = new AbortController();
    controller = request;
    // Hide duplicates immediately, including during an unavailable projection.
    const pinned = new Set([...document.querySelectorAll('[data-quick-id] a')].map(a => a.getAttribute('href')));
    for (const link of document.querySelectorAll('[data-workspace-tool]')) link.hidden = pinned.has(link.getAttribute('href'));
    try {
      const response = await fetch(location.href, {signal: request.signal, headers: {Accept: 'text/html'}});
      if (!response.ok || new URL(response.url).pathname !== location.pathname) throw new Error('shortcut projection unavailable');
      const doc = new DOMParser().parseFromString(await response.text(), 'text/html');
      if (request.signal.aborted) return;
      applyProjection(doc);
    } catch (error) {
      if (error.name !== 'AbortError') console.warn('Workspace shortcuts will refresh on the next navigation.', error);
    }
  }
  document.addEventListener('gorun:dashboard-pins-changed', refreshTools);
  window.addEventListener('pagehide', () => controller?.abort());
  placeTools();
})();
