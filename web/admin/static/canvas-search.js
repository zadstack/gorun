(function (factory) {
  'use strict';
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (typeof document !== 'undefined') api.init(document);
})(function () {
  'use strict';

  function isShortcut(event) {
    return !!((event.ctrlKey || event.metaKey) && !event.altKey && !event.shiftKey && !event.isComposing &&
      (event.code === 'KeyK' || String(event.key || '').toLowerCase() === 'k'));
  }

  // Build local destinations from identities, never from a result's arbitrary URL.
  function resultHref(item) {
    if (!item || !/^[0-9A-HJKMNP-TV-Z]{26}$/.test(item.board_id || '')) return null;
    if (item.card_id && !/^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/i.test(item.card_id)) return null;
    return '/admin/canvases/' + item.board_id + (item.card_id ? '#card-' + item.card_id : '');
  }

  function queryState(value) {
    if (!value.trim()) return 'empty';
    const normalized = value.normalize('NFKD').toLowerCase().replace(/\p{M}/gu, '').replace(/ـ/g, '')
      .replace(/[^\p{L}\p{N}]+/gu, ' ').trim();
    if (new TextEncoder().encode(value).length > 1000 || [...normalized].length > 160) return 'long';
    return [...normalized.replace(/ /g, '')].length >= 2 ? 'ready' : 'short';
  }

  function init(doc) {
    const root = doc.querySelector('[data-canvas-search]');
    if (!root || root.dataset.searchBound) return;
    const dialog = root.querySelector('[data-canvas-search-dialog]');
    if (!dialog || typeof dialog.showModal !== 'function') return;
    root.dataset.searchBound = 'true';
    const trigger = root.querySelector('[data-canvas-search-open]');
    const input = root.querySelector('[data-canvas-search-input]');
    const list = root.querySelector('[data-canvas-search-results]');
    const status = root.querySelector('[data-canvas-search-status]');
    const retry = root.querySelector('[data-canvas-search-retry]');
    const labels = Object.fromEntries([...root.querySelectorAll('[data-canvas-search-label]')].map(el => [el.dataset.canvasSearchLabel, el.textContent]));
    const t = key => labels[key] || key;
    const number = new Intl.NumberFormat(doc.documentElement.lang || undefined);
    let controller = null, debounce = null, timeout = null, serial = 0;
    let options = [], cursor = 0, returnFocus = null, opened = false;
    trigger.hidden = false;
    if (/Mac|iPhone|iPad/.test(navigator.platform || '')) root.querySelector('[data-canvas-search-shortcut]').textContent = '⌘ K';

    function stopRequest() {
      serial++;
      clearTimeout(debounce);
      clearTimeout(timeout);
      controller?.abort();
      controller = null;
    }

    function clearResults() {
      options = [];
      cursor = 0;
      list.replaceChildren();
      input.removeAttribute('aria-activedescendant');
    }

    function showStatus(key, busy = false) {
      status.textContent = t(key);
      status.dataset.state = key;
      list.setAttribute('aria-busy', String(busy));
      retry.hidden = key !== 'error';
    }

    function active(index, scroll = false) {
      if (!options.length) return;
      cursor = Math.max(0, Math.min(index, options.length - 1));
      options.forEach((el, at) => el.setAttribute('aria-selected', String(at === cursor)));
      input.setAttribute('aria-activedescendant', options[cursor].id);
      if (scroll) options[cursor].scrollIntoView({block: 'nearest'});
    }

    function render(items) {
      clearResults();
      for (const item of items.slice(0, 20)) {
        const href = resultHref(item);
        if (!href || typeof item.title !== 'string' || typeof item.board_title !== 'string') continue;
        const option = doc.createElement('a');
        option.href = href;
        option.id = 'canvas-search-result-' + options.length;
        option.setAttribute('role', 'option');
        option.setAttribute('tabindex', '-1');
        option.className = 'canvas-search-result';
        const title = doc.createElement('strong');
        title.dir = 'auto';
        title.textContent = (item.title.trim() || (item.card_id ? t('untitled') : item.board_title)).slice(0, 2000);
        const context = doc.createElement('span');
        context.className = 'canvas-search-context';
        context.dir = 'auto';
        context.textContent = item.card_id ? item.board_title.slice(0, 2000) : t('canvas');
        option.append(title, context);
        if (typeof item.snippet === 'string' && item.snippet) {
          const snippet = doc.createElement('span');
          snippet.className = 'canvas-search-snippet';
          snippet.dir = 'auto';
          snippet.textContent = item.snippet.slice(0, 2000);
          option.append(snippet);
        }
        options.push(option);
        list.append(option);
      }
      list.setAttribute('aria-busy', 'false');
      retry.hidden = true;
      if (options.length) {
        status.dataset.state = 'results';
        status.textContent = t('results').replace('{count}', number.format(options.length));
        active(0);
      } else showStatus('empty');
    }

    async function search(query, ticket) {
      controller = new AbortController();
      const ownController = controller;
      timeout = setTimeout(() => ownController.abort(), 10000);
      try {
        const response = await fetch('/admin/canvases/search?q=' + encodeURIComponent(query), {
          credentials: 'same-origin', cache: 'no-store', redirect: 'error', signal: ownController.signal,
          headers: {'Accept': 'application/json'},
        });
        if (ticket !== serial || !opened) return;
        if (response.status === 401 || response.status === 403) {
          showStatus('denied');
          return;
        }
        if (response.status === 422) {
          showStatus('long');
          return;
        }
        if (!response.ok) throw new Error('search');
        const data = await response.json();
        if (ticket !== serial || !opened) return;
        if (!Array.isArray(data.items)) throw new Error('results');
        render(data.items);
      } catch (_) {
        if (ticket === serial && opened) showStatus('error');
      } finally {
        // A late completion cannot cancel the timeout of a newer query.
        if (ticket === serial) {
          clearTimeout(timeout);
          if (controller === ownController) controller = null;
        }
      }
    }

    function schedule(immediate = false) {
      stopRequest();
      clearResults();
      const state = queryState(input.value);
      if (state !== 'ready') {
        showStatus(state === 'long' ? 'long' : 'prompt');
        return;
      }
      if (input.dataset.composing === 'true') {
        showStatus('prompt');
        return;
      }
      showStatus('loading', true);
      const ticket = serial, query = input.value.trim();
      if (immediate) search(query, ticket);
      else debounce = setTimeout(() => search(query, ticket), 200);
    }

    function closed(restore = true) {
      if (!opened) return;
      opened = false;
      stopRequest();
      input.setAttribute('aria-expanded', 'false');
      clearResults();
      if (restore && returnFocus?.isConnected) returnFocus.focus({preventScroll: true});
      returnFocus = null;
    }

    function close(restore = true) {
      closed(restore);
      if (dialog.open) dialog.close();
    }

    function open() {
      if (opened) {
        input.focus();
        return;
      }
      // A card edit or execution review owns focus until it is closed. Do not
      // cover unsubmitted input or an approval with another modal.
      if (doc.querySelector('dialog[open]')) return;
      returnFocus = doc.activeElement;
      input.value = '';
      clearResults();
      showStatus('prompt');
      dialog.showModal();
      opened = true;
      input.setAttribute('aria-expanded', 'true');
      input.focus();
    }

    trigger.addEventListener('click', open);
    root.querySelector('[data-canvas-search-close]').addEventListener('click', () => close());
    dialog.addEventListener('cancel', event => {event.preventDefault(); close();});
    dialog.addEventListener('close', () => {if (!dialog.open) closed();});
    dialog.addEventListener('keydown', event => {
      if (event.key !== 'Escape' || event.isComposing) return;
      event.preventDefault();
      event.stopPropagation();
      close();
    });
    dialog.addEventListener('click', event => {
      if (event.target !== dialog) return;
      const rect = dialog.getBoundingClientRect();
      if (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom) close();
    });
    input.addEventListener('input', () => schedule());
    input.addEventListener('compositionstart', () => {input.dataset.composing = 'true'; stopRequest(); clearResults();});
    input.addEventListener('compositionend', () => {delete input.dataset.composing; schedule();});
    input.addEventListener('keydown', event => {
      if (event.isComposing) return;
      if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
        event.preventDefault();
        active(cursor + (event.key === 'ArrowDown' ? 1 : -1), true);
      } else if (event.key === 'Enter') {
        event.preventDefault();
        options[cursor]?.click();
      }
    });
    list.addEventListener('pointermove', event => {
      const option = event.target.closest('[role=option]');
      const index = options.indexOf(option);
      if (index >= 0) active(index);
    });
    list.addEventListener('click', event => {
      if (event.target.closest('[role=option]')) close(false);
    });
    retry.addEventListener('click', () => {input.focus(); schedule(true);});
    const onKey = event => {
      if (!root.isConnected || !isShortcut(event)) return;
      if (!opened && doc.querySelector('dialog[open]')) return;
      event.preventDefault();
      event.stopPropagation();
      open();
    };
    doc.addEventListener('keydown', onKey, true);
    // HTMX may replace the page content without replacing the document.
    const onCleanup = event => {
      if (event.detail?.elt !== root && !event.detail?.elt?.contains(root)) return;
      close(false);
      doc.removeEventListener('keydown', onKey, true);
      doc.removeEventListener('htmx:beforeCleanupElement', onCleanup);
    };
    doc.addEventListener('htmx:beforeCleanupElement', onCleanup);
  }

  return {isShortcut, resultHref, queryState, init};
});
