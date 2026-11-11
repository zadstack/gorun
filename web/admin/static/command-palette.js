// Command palette: Ctrl/Cmd+K on every admin page (partials/command_palette).
// It lists the admin pages and quick actions the user may use (GET
// /admin/x/palette, loaded once per page) and live content and user results
// from the global search (/admin/x/webmcp/search). Matching folds Arabic
// spelling variants and marks, so "اعدادات" finds "الإعدادات". On canvas pages
// Ctrl/Cmd+K stays with canvas search; the topbar Commands button still opens
// this palette there. Without JavaScript the topbar search form remains.
(function (factory) {
  'use strict';
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (typeof document !== 'undefined') api.init(document);
})(function () {
  'use strict';

  const RECENT_KEY = 'gorun.palette.recent';
  const RECENT_MAX = 5;
  const GROUP_MAX = 8;
  const SEARCH_MIN = 2;
  const DEBOUNCE_MS = 200;

  // normalize folds case, Latin accents, Arabic marks (tashkeel, hamza and
  // madda above/below) and tatweel, and unifies alef, ya, ta marbuta and
  // Persian kaf/ya, so spelling variants compare equal.
  function normalize(value) {
    return String(value == null ? '' : value)
      .normalize('NFKD')
      .replace(/\p{M}/gu, '')
      .toLowerCase()
      .replace(/ـ/g, '')
      .replace(/[ٱٲٳٵ]/g, 'ا')
      .replace(/[ىی]/g, 'ي')
      .replace(/ة/g, 'ه')
      .replace(/ک/g, 'ك')
      .replace(/[^\p{L}\p{N}]+/gu, ' ')
      .trim();
  }

  // score rates how well query matches text (0 = no match): whole text,
  // prefix, word start, substring, every word somewhere, then the query's
  // letters in order (fuzzy).
  function score(query, text) {
    const q = normalize(query), t = normalize(text);
    if (!q || !t) return 0;
    if (t === q) return 100;
    if (t.startsWith(q)) return 80;
    if ((' ' + t).includes(' ' + q)) return 60;
    if (t.includes(q)) return 45;
    const words = q.split(' ');
    if (words.length > 1 && words.every(w => t.includes(w))) return 35;
    const letters = [...q.replace(/ /g, '')];
    if (letters.length < 2) return 0;
    let at = 0, gaps = 0, last = -1;
    const chars = [...t];
    for (let i = 0; i < chars.length && at < letters.length; i++) {
      if (chars[i] === letters[at]) {
        if (last >= 0 && i - last > 1) gaps++;
        last = i;
        at++;
      }
    }
    return at === letters.length ? Math.max(1, 20 - gaps * 3) : 0;
  }

  // rank returns the items that match query, best first (stable), scoring
  // the label and, slightly lower, the labels in other languages.
  function itemScore(query, item) {
    if (!item) return 0;
    let best = score(query, item.label);
    for (const alt of item.alt || []) best = Math.max(best, score(query, alt) - 1);
    return best;
  }

  function rank(query, items, limit = GROUP_MAX) {
    const out = [];
    items.forEach((item, index) => {
      const best = itemScore(query, item);
      if (best > 0) out.push({item, best, index});
    });
    out.sort((a, b) => b.best - a.best || a.index - b.index);
    return out.slice(0, limit).map(entry => entry.item);
  }

  function isShortcut(event) {
    return !!((event.ctrlKey || event.metaKey) && !event.altKey && !event.shiftKey && !event.isComposing &&
      (event.code === 'KeyK' || String(event.key || '').toLowerCase() === 'k'));
  }

  // adminHref accepts only links inside this site's admin.
  function adminHref(value, origin) {
    try {
      const url = new URL(String(value || ''), origin);
      if (url.origin !== origin || !(url.pathname === '/admin' || url.pathname.startsWith('/admin/'))) return null;
      return url.pathname + url.search + url.hash;
    } catch (_) {
      return null;
    }
  }

  function readRecent(storage, origin) {
    try {
      const list = JSON.parse(storage.getItem(RECENT_KEY) || '[]');
      if (!Array.isArray(list)) return [];
      return list.filter(x => x && typeof x.label === 'string' && adminHref(x.href, origin)).slice(0, RECENT_MAX)
        .map(x => ({label: x.label.slice(0, 200), href: adminHref(x.href, origin), alt: []}));
    } catch (_) {
      return [];
    }
  }

  function remember(storage, origin, item) {
    const href = adminHref(item.href, origin);
    if (!href) return;
    try {
      const list = readRecent(storage, origin).filter(x => x.href !== href);
      list.unshift({label: String(item.label).slice(0, 200), href});
      storage.setItem(RECENT_KEY, JSON.stringify(list.slice(0, RECENT_MAX).map(x => ({label: x.label, href: x.href}))));
    } catch (_) { /* private mode or storage disabled */ }
  }

  function init(doc) {
    const dialog = doc.querySelector('[data-gp-palette]');
    if (!dialog || dialog.dataset.bound || typeof dialog.showModal !== 'function') return;
    dialog.dataset.bound = 'true';
    const win = doc.defaultView;
    const origin = win.location.origin;
    const input = dialog.querySelector('[data-gp-palette-input]');
    const list = dialog.querySelector('[data-gp-palette-list]');
    const status = dialog.querySelector('[data-gp-palette-status]');
    const labels = Object.fromEntries([...dialog.querySelectorAll('[data-gp-palette-label]')].map(el => [el.dataset.gpPaletteLabel, el.textContent]));
    const t = key => labels[key] || key;
    const triggers = [...doc.querySelectorAll('[data-gp-palette-open]')];
    let storage = null;
    try { storage = win.localStorage; } catch (_) { storage = null; }
    let data = null, loading = null, controller = null, debounce = null, serial = 0;
    let remote = {content: [], users: []}, remoteState = '';
    let options = [], cursor = -1, moved = false, returnFocus = null;

    if (/Mac|iPhone|iPad/.test(win.navigator.platform || '')) doc.querySelectorAll('[data-gp-palette-key]').forEach(k => { k.textContent = '⌘ K'; });
    triggers.forEach(b => { b.hidden = false; b.addEventListener('click', () => open()); });

    function load() {
      if (data) return Promise.resolve(data);
      if (!loading) {
        loading = fetch('/admin/x/palette', {credentials: 'same-origin', redirect: 'error', headers: {Accept: 'application/json'}})
          .then(r => { if (!r.ok) throw new Error('palette ' + r.status); return r.json(); })
          .then(json => {
            data = {pages: Array.isArray(json.pages) ? json.pages : [], actions: Array.isArray(json.actions) ? json.actions : []};
            return data;
          })
          .catch(error => { loading = null; throw error; });
      }
      return loading;
    }

    function stopSearch() {
      serial++;
      clearTimeout(debounce);
      if (controller) controller.abort();
      controller = null;
    }

    function searchRemote(query) {
      stopSearch();
      remote = {content: [], users: []};
      remoteState = '';
      if ([...normalize(query).replace(/ /g, '')].length < SEARCH_MIN || input.dataset.composing === 'true') return;
      remoteState = 'loading';
      const ticket = serial;
      debounce = setTimeout(async () => {
        controller = new AbortController();
        const own = controller;
        try {
          const response = await fetch('/admin/x/webmcp/search?' + new URLSearchParams({q: query.trim()}), {
            credentials: 'same-origin', redirect: 'error', signal: own.signal, headers: {Accept: 'application/json'}});
          if (!response.ok) throw new Error('search ' + response.status);
          const json = await response.json();
          if (ticket !== serial || !dialog.open) return;
          const hits = (arr, kind) => (Array.isArray(arr) ? arr : []).slice(0, GROUP_MAX)
            .filter(h => h && typeof h.title === 'string' && adminHref(h.href, origin))
            .map(h => ({label: h.title || '—', detail: typeof h.detail === 'string' ? h.detail : '', href: adminHref(h.href, origin), kind}));
          remote = {content: hits(json.content, 'content'), users: hits(json.users, 'users')};
          remoteState = 'done';
        } catch (error) {
          if (ticket !== serial || (error && error.name === 'AbortError')) return;
          remoteState = 'error';
        } finally {
          if (controller === own) controller = null;
        }
        if (ticket === serial && dialog.open) render();
      }, DEBOUNCE_MS);
    }

    function groups() {
      const query = input.value.trim();
      if (!data) return [];
      if (!normalize(query)) {
        return [
          ['recent', storage ? readRecent(storage, origin) : []],
          ['actions', data.actions],
          ['pages', data.pages],
        ];
      }
      // The group holding the better match leads ("media" opens Media before "Upload media").
      const actions = rank(query, data.actions), pages = rank(query, data.pages);
      const local = itemScore(query, pages[0]) > itemScore(query, actions[0])
        ? [['pages', pages], ['actions', actions]] : [['actions', actions], ['pages', pages]];
      const out = [
        ...local,
        ['content', remote.content],
        ['users', remote.users],
      ];
      const href = adminHref('/admin/search?' + new URLSearchParams({q: query}), origin);
      out.push(['search', [{label: t('search_all').replace('{query}', query), href, kind: 'search'}]]);
      return out;
    }

    function setActive(index, scroll) {
      if (!options.length) {
        cursor = -1;
        input.removeAttribute('aria-activedescendant');
        return;
      }
      cursor = Math.max(0, Math.min(index, options.length - 1));
      options.forEach((el, at) => el.setAttribute('aria-selected', String(at === cursor)));
      input.setAttribute('aria-activedescendant', options[cursor].id);
      if (scroll) options[cursor].scrollIntoView({block: 'nearest'});
    }

    function render() {
      const keep = cursor >= 0 && options[cursor] ? options[cursor].dataset.href : null;
      options = [];
      list.replaceChildren();
      let count = 0;
      for (const [key, items] of groups()) {
        if (!items.length) continue;
        const group = doc.createElement('div');
        group.setAttribute('role', 'group');
        group.className = 'gp-palette-group';
        const heading = doc.createElement('div');
        heading.className = 'gp-palette-group-label';
        heading.id = 'gp-palette-group-' + key;
        heading.textContent = t('group.' + key);
        heading.setAttribute('role', 'presentation');
        group.setAttribute('aria-labelledby', heading.id);
        group.append(heading);
        for (const item of items) {
          const href = adminHref(item.href, origin);
          if (!href) continue;
          const option = doc.createElement('div');
          option.setAttribute('role', 'option');
          option.id = 'gp-palette-option-' + options.length;
          option.className = 'gp-palette-option';
          option.dataset.href = href;
          option.dataset.label = item.label;
          const label = doc.createElement('bdi');
          label.className = 'gp-palette-label';
          label.textContent = item.label;
          option.append(label);
          const detail = item.detail || item.section || '';
          if (detail) {
            const sub = doc.createElement('bdi');
            sub.className = 'gp-palette-detail';
            sub.textContent = detail;
            option.append(sub);
          }
          option.addEventListener('click', () => choose(option));
          option.addEventListener('pointermove', () => { const at = options.indexOf(option); if (at !== cursor) { moved = true; setActive(at, false); } });
          options.push(option);
          group.append(option);
          if (key !== 'search') count++;
        }
        list.append(group);
      }
      input.setAttribute('aria-expanded', String(options.length > 0));
      // Late search results keep the option the user moved to; otherwise the best match leads.
      const again = moved && keep ? options.findIndex(o => o.dataset.href === keep) : -1;
      setActive(again >= 0 ? again : 0, false);
      if (!data) status.textContent = t(loading ? 'loading' : 'load_error');
      else if (remoteState === 'loading') status.textContent = t('searching');
      else if (remoteState === 'error') status.textContent = t('error');
      else if (input.value.trim() && !count) status.textContent = t('empty');
      else if (input.value.trim()) status.textContent = t('count').replace('{count}', new Intl.NumberFormat(doc.documentElement.lang || undefined).format(count));
      else status.textContent = '';
    }

    function choose(option) {
      const href = adminHref(option.dataset.href, origin);
      if (!href) return;
      if (storage && !href.startsWith('/admin/search?')) remember(storage, origin, {label: option.dataset.label, href});
      close(false);
      win.location.assign(href);
    }

    function update() {
      moved = false;
      searchRemote(input.value);
      render();
    }

    function open() {
      if (dialog.open) { input.focus(); return; }
      // Another modal (a confirmation, canvas search, an editor dialog) keeps focus.
      if (doc.querySelector('dialog[open]')) return;
      returnFocus = doc.activeElement;
      input.value = '';
      moved = false;
      remote = {content: [], users: []};
      remoteState = '';
      dialog.showModal();
      input.focus();
      render();
      load().then(() => { if (dialog.open) render(); }, () => { if (dialog.open) render(); });
    }

    function close(restore = true) {
      stopSearch();
      // Never leave focus in the closed dialog's field (the page's own keys would treat it as typing).
      if (dialog.contains(doc.activeElement)) doc.activeElement.blur();
      if (dialog.open) dialog.close();
      list.replaceChildren();
      options = [];
      cursor = -1;
      input.setAttribute('aria-expanded', 'false');
      input.removeAttribute('aria-activedescendant');
      if (restore && returnFocus && returnFocus.isConnected) returnFocus.focus({preventScroll: true});
      returnFocus = null;
    }

    input.addEventListener('input', update);
    input.addEventListener('compositionstart', () => { input.dataset.composing = 'true'; });
    input.addEventListener('compositionend', () => { delete input.dataset.composing; update(); });
    input.addEventListener('keydown', event => {
      if (event.isComposing) return;
      const moves = {ArrowDown: cursor + 1, ArrowUp: cursor - 1, Home: 0, End: options.length - 1};
      if (event.key in moves && options.length) {
        // Shift+Home/End still select text in the field.
        if ((event.key === 'Home' || event.key === 'End') && input.value && event.shiftKey) return;
        event.preventDefault();
        moved = true;
        setActive(moves[event.key], true);
      } else if (event.key === 'Enter') {
        event.preventDefault();
        if (options[cursor]) choose(options[cursor]);
      }
    });
    dialog.querySelector('[data-gp-palette-close]')?.addEventListener('click', () => close());
    dialog.addEventListener('cancel', event => { event.preventDefault(); close(); });
    dialog.addEventListener('keydown', event => {
      if (event.key === 'Escape' && !event.isComposing) {
        event.preventDefault();
        event.stopPropagation();
        close();
        return;
      }
      if (event.key !== 'Tab') return;
      // Focus trap: Tab cycles through the dialog's own controls.
      const controls = [...dialog.querySelectorAll('input, button:not([disabled])')].filter(el => el.getClientRects().length);
      if (!controls.length) return;
      const first = controls[0], last = controls[controls.length - 1];
      if (event.shiftKey && doc.activeElement === first) { event.preventDefault(); last.focus(); }
      else if (!event.shiftKey && doc.activeElement === last) { event.preventDefault(); first.focus(); }
    });
    dialog.addEventListener('click', event => {
      if (event.target !== dialog) return;
      const rect = dialog.getBoundingClientRect();
      if (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom) close();
    });
    doc.addEventListener('keydown', event => {
      if (!isShortcut(event)) return;
      if (dialog.open) { event.preventDefault(); close(); return; }
      // Canvas pages keep Ctrl/Cmd+K for canvas search (canvas-search.js).
      if (doc.querySelector('[data-canvas-search]')) return;
      event.preventDefault();
      open();
    });
    win.gpPalette = {open, close};
  }

  return {normalize, score, itemScore, rank, isShortcut, adminHref, readRecent, remember, init};
});
