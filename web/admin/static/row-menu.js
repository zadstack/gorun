// Row overflow menu (docs/ADMIN_COMPONENTS.md, "Row actions"). Markup:
//   <div class="row-menu" data-row-menu>
//     <button data-row-menu-button aria-haspopup="menu" aria-controls="ID" hidden>…</button>
//     <div class="row-menu-list" id="ID" data-row-menu-list> forms with <button class="row-menu-item"> </div>
//   </div>
// Without JavaScript the button stays hidden and the actions render inline, as
// ordinary forms. With it, the list becomes a menu: the button toggles it,
// ArrowDown/ArrowUp/Home/End move between items, Escape and Tab close it, and
// Escape returns focus to the button. The list is fixed-positioned so a
// scrolling table never clips it.
(() => {
  'use strict';

  // Index after a navigation key, wrapping at both ends; -1 when the key is
  // not a navigation key.
  function nextIndex(index, key, count) {
    if (count < 1) return -1;
    switch (key) {
      case 'ArrowDown': return index < 0 ? 0 : (index + 1) % count;
      case 'ArrowUp': return index < 0 ? count - 1 : (index - 1 + count) % count;
      case 'Home': return 0;
      case 'End': return count - 1;
      default: return -1;
    }
  }

  // Viewport position for the menu: below the button (above it when there is
  // no room below), aligned to the button's inline end, kept inside a margin.
  function placement(button, menu, viewport, rtl, margin = 8) {
    let top = button.bottom + 4;
    if (top + menu.height > viewport.height - margin && button.top - 4 - menu.height >= margin) top = button.top - 4 - menu.height;
    let left = rtl ? button.left : button.right - menu.width;
    left = Math.min(Math.max(left, margin), Math.max(margin, viewport.width - menu.width - margin));
    return {top: Math.round(top), left: Math.round(left)};
  }

  const api = {nextIndex, placement};
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (typeof document === 'undefined') return;

  let open = null; // {root, button, list}

  const items = list => [...list.querySelectorAll('[data-row-menu-item]')].filter(item => !item.disabled);

  function position(state) {
    // Measure at the viewport origin so the natural width is not squeezed by
    // the space left beside the button.
    state.list.style.top = '0px';
    state.list.style.left = '0px';
    const rect = state.button.getBoundingClientRect();
    const spot = placement(rect, {width: state.list.offsetWidth, height: state.list.offsetHeight},
      {width: document.documentElement.clientWidth, height: innerHeight}, getComputedStyle(state.root).direction === 'rtl');
    state.list.style.top = spot.top + 'px';
    state.list.style.left = spot.left + 'px';
  }

  function close(focusButton) {
    if (!open) return;
    const state = open;
    open = null;
    state.list.hidden = true;
    state.button.setAttribute('aria-expanded', 'false');
    if (focusButton) state.button.focus();
  }

  function show(state, focus) {
    if (open && open.list !== state.list) close(false);
    open = state;
    state.list.hidden = false;
    state.button.setAttribute('aria-expanded', 'true');
    position(state);
    const list = items(state.list);
    list[focus === 'last' ? list.length - 1 : 0]?.focus();
  }

  function enhance(root) {
    if (root.dataset.rowMenuReady) return;
    const button = root.querySelector('[data-row-menu-button]'), list = root.querySelector('[data-row-menu-list]');
    if (!button || !list) return;
    root.dataset.rowMenuReady = '1';
    const state = {root, button, list};
    list.setAttribute('role', 'menu');
    if (button.id) list.setAttribute('aria-labelledby', button.id);
    for (const form of list.querySelectorAll('form')) form.setAttribute('role', 'none');
    for (const item of list.querySelectorAll('[data-row-menu-item]')) { item.setAttribute('role', 'menuitem'); item.tabIndex = -1; }
    list.hidden = true;
    root.classList.add('is-enhanced');
    button.hidden = false;
    button.setAttribute('aria-expanded', 'false');
    button.addEventListener('click', () => (open === state ? close(true) : show(state, 'first')));
    button.addEventListener('keydown', event => {
      if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
        event.preventDefault();
        show(state, event.key === 'ArrowUp' ? 'last' : 'first');
      }
    });
    list.addEventListener('keydown', event => {
      const all = items(list), index = all.indexOf(document.activeElement);
      const next = nextIndex(index, event.key, all.length);
      if (next >= 0) { event.preventDefault(); all[next].focus(); return; }
      if (event.key === 'Escape') { event.preventDefault(); close(true); }
      else if (event.key === 'Tab') close(false);
    });
    // Choosing an item closes the menu and leaves focus on the button, so a
    // confirmation dialog returns there when it closes.
    list.addEventListener('click', event => { if (event.target.closest('[data-row-menu-item]')) close(true); });
  }

  function enhanceAll(scope) {
    for (const root of (scope || document).querySelectorAll('[data-row-menu]')) enhance(root);
  }

  document.addEventListener('pointerdown', event => { if (open && !open.root.contains(event.target)) close(false); });
  document.addEventListener('keydown', event => { if (open && event.key === 'Escape' && !open.list.contains(event.target)) close(true); });
  addEventListener('resize', () => close(false));
  addEventListener('scroll', event => { if (open && !open.list.contains(event.target)) close(false); }, true);
  document.addEventListener('htmx:afterSwap', event => enhanceAll(event.target));
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', () => enhanceAll());
  else enhanceAll();
})();
