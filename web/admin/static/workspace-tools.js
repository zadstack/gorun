(() => {
  'use strict';
  let controller;
  // The regions the navigation projection owns (partials/workspace-navigation):
  // the dashboard directory, the sidebar sections and the contextual tabs.
  const selectors = ['.workspace-tool-groups','[data-navigation-sections]','[data-workspace-context-navigation]'];
  // Swap every navigation region from a rendered projection, keeping focus on the
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
    window.lucide?.createIcons(); // the swapped regions carry {{icon}} placeholders
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
})();
