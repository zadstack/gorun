// Admin keyboard shortcuts (the map opens with "?", partials/shortcuts):
//   /          focus the topbar search
//   ?          show the shortcut map
//   g h|c|m|s|a  go to home, content, media, settings, approvals (only the
//              destinations the user's navigation offers, rendered as
//              [data-gp-shortcut-go] links by the layout)
//   Ctrl/Cmd+S submit the page's [data-shortcut-save] button (editor pages)
// Ctrl/Cmd+K belongs to command-palette.js. Nothing fires while focus is in
// a text field, select or contenteditable (except Ctrl/Cmd+S), during IME
// composition, or while a modal dialog is open.
(function (factory) {
  'use strict';
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (typeof document !== 'undefined') api.init(document);
})(function () {
  'use strict';

  const SEQUENCE_MS = 1500;
  const NON_TEXT_INPUTS = ['button', 'submit', 'reset', 'checkbox', 'radio', 'range', 'color', 'file', 'image'];
  const TEXT_ROLES = ['textbox', 'searchbox', 'combobox'];

  // isTyping reports whether el takes text input, so plain keys belong to it.
  function isTyping(el) {
    if (!el || typeof el !== 'object') return false;
    if (el.isContentEditable) return true;
    const tag = String(el.tagName || '').toUpperCase();
    if (tag === 'TEXTAREA' || tag === 'SELECT') return true;
    if (tag === 'INPUT') return !NON_TEXT_INPUTS.includes(String(el.type || 'text').toLowerCase());
    const role = typeof el.getAttribute === 'function' ? el.getAttribute('role') : null;
    return TEXT_ROLES.includes(role);
  }

  function composing(event) {
    return !!(event.isComposing || event.keyCode === 229 || event.key === 'Process');
  }

  // letter is the Latin letter of a key, by physical position first so the
  // shortcuts also work with an Arabic keyboard layout.
  function letter(event) {
    const m = /^Key([A-Z])$/.exec(event.code || '');
    if (m) return m[1].toLowerCase();
    const k = String(event.key || '');
    return /^[a-z]$/i.test(k) ? k.toLowerCase() : null;
  }

  // classify maps a keydown to a shortcut: {type: 'save' | 'help' | 'search'
  // | 'g' | 'go' (with key) | 'cancel'} or null when the key is not ours.
  // pending is true right after "g".
  function classify(event, target, pending) {
    if (!event || composing(event)) return null;
    const mods = event.ctrlKey || event.metaKey || event.altKey;
    if ((event.ctrlKey || event.metaKey) && !event.altKey && !event.shiftKey && letter(event) === 's') return {type: 'save'};
    if (mods || isTyping(target)) return null;
    if (pending) {
      const key = event.shiftKey ? null : letter(event);
      return key ? {type: 'go', key} : {type: 'cancel'};
    }
    if (event.key === '?' || event.key === '؟') return {type: 'help'};
    if (event.key === '/' || (event.code === 'Slash' && !event.shiftKey)) return {type: 'search'};
    if (!event.shiftKey && letter(event) === 'g') return {type: 'g'};
    return null;
  }

  function init(doc) {
    const win = doc.defaultView;
    if (!win || win.gpShortcuts) return;
    const help = doc.querySelector('[data-gp-shortcuts]');
    let pendingSince = 0, returnFocus = null;

    const visible = el => !!el && el.getClientRects().length > 0;
    const modalOpen = () => !!doc.querySelector('dialog[open]') ||
      [...doc.querySelectorAll('[role=dialog][aria-modal=true]:not(dialog)')].some(visible);

    function saveButton() {
      return [...doc.querySelectorAll('[data-shortcut-save]')].find(b => !b.disabled && b.form && visible(b)) || null;
    }

    function openHelp() {
      if (!help || typeof help.showModal !== 'function' || help.open) return;
      returnFocus = doc.activeElement;
      help.showModal();
      help.querySelector('[data-gp-shortcuts-close]')?.focus();
    }

    if (help) {
      help.querySelector('[data-gp-shortcuts-close]')?.addEventListener('click', () => help.close());
      help.addEventListener('close', () => {
        if (returnFocus && returnFocus.isConnected) returnFocus.focus({preventScroll: true});
        returnFocus = null;
      });
      help.addEventListener('click', event => { if (event.target === help) help.close(); });
    }

    doc.addEventListener('keydown', event => {
      if (event.defaultPrevented || ['Shift', 'Control', 'Meta', 'Alt'].includes(event.key)) return;
      const pending = pendingSince > 0 && Date.now() - pendingSince < SEQUENCE_MS;
      pendingSince = 0;
      const action = classify(event, event.target, pending);
      if (!action || action.type === 'cancel') return;
      if (action.type === 'save') {
        const button = modalOpen() ? null : saveButton();
        if (!button) return; // no editor here: leave the browser's own Ctrl/Cmd+S
        event.preventDefault();
        if (typeof button.form.requestSubmit === 'function') button.form.requestSubmit(button);
        else button.click();
        return;
      }
      if (modalOpen()) return;
      if (action.type === 'g') {
        pendingSince = Date.now();
        return;
      }
      if (action.type === 'go') {
        const link = doc.querySelector('[data-gp-shortcut-go="' + action.key + '"]');
        if (!link) return;
        event.preventDefault();
        win.location.assign(link.getAttribute('href'));
        return;
      }
      if (action.type === 'help') {
        if (!help) return;
        event.preventDefault();
        openHelp();
        return;
      }
      if (action.type === 'search') {
        const search = doc.getElementById('gp-search');
        if (!visible(search)) return;
        event.preventDefault();
        search.focus();
        search.select?.();
      }
    });
    win.gpShortcuts = {openHelp};
  }

  return {isTyping, classify, letter, init};
});
