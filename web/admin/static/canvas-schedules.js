(() => {
  'use strict';
  // This board's schedule nodes, grouped by the card each one triggers. Read-only: every action
  // reuses an existing path (the node's enable flow, the #card-<id> reveal in canvas.js).
  window.CanvasSchedules = {create};
  const KEY = 'gorun.canvas.schedules';
  const SVG = 'http://www.w3.org/2000/svg';
  function create(api) {
    const {root, t, element: el, button: btn} = api;
    const aside = root.querySelector('[data-canvas-schedules]');
    if (!aside || !window.CanvasState) return null;
    const doc = root.ownerDocument;
    const lang = () => doc.documentElement.lang || undefined;
    // Collapsed until the person opens it, so the panel never covers the board on a first visit.
    let expanded = false;
    try { expanded = sessionStorage.getItem(KEY) === 'expanded'; } catch (_) {}

    const heading = el('h2', undefined, 'canvas-schedules-heading'); heading.id = 'canvas-schedules-title';
    const toggle = btn('', () => setExpanded(!expanded), 'canvas-schedules-toggle');
    const label = el('span', undefined, 'canvas-schedules-label');
    toggle.append(icon('clock'), label, icon('chevron'));
    toggle.setAttribute('aria-controls', 'canvas-schedules-region');
    heading.append(toggle);
    const region = el('div', undefined, 'canvas-schedules-region');
    region.id = 'canvas-schedules-region'; region.setAttribute('role', 'region'); region.setAttribute('aria-labelledby', 'canvas-schedules-title');
    region.addEventListener('keydown', event => {
      if (event.key !== 'Escape') return;
      event.preventDefault(); event.stopPropagation();
      setExpanded(false); toggle.focus();
    });
    aside.append(heading, region);

    function icon(name) {
      const svg = doc.createElementNS(SVG, 'svg');
      svg.setAttribute('viewBox', '0 0 24 24'); svg.setAttribute('aria-hidden', 'true'); svg.setAttribute('focusable', 'false');
      svg.classList.add('canvas-schedules-icon', `canvas-schedules-${name}`);
      const parts = name === 'clock' ? [['circle', {cx: 12, cy: 12, r: 9}], ['path', {d: 'M12 7v5l3 2'}]] : [['path', {d: 'M6 15l6-6 6 6'}]];
      for (const [tag, attrs] of parts) { const node = doc.createElementNS(SVG, tag); for (const [k, v] of Object.entries(attrs)) node.setAttribute(k, v); svg.append(node); }
      return svg;
    }
    function setExpanded(next) {
      expanded = next;
      try { sessionStorage.setItem(KEY, next ? 'expanded' : 'collapsed'); } catch (_) {}
      toggle.setAttribute('aria-expanded', String(next)); region.hidden = !next;
      aside.dataset.expanded = String(next);
    }
    function reveal(id) {
      const hash = '#card-' + id;
      // Setting the same hash fires no hashchange, so clear it first to reveal the node again.
      if (location.hash === hash) history.replaceState(history.state, '', location.pathname + location.search);
      // Floating over the flow view, the open panel can cover the node it reveals; fold it first.
      if (getComputedStyle(aside).position === 'absolute') setExpanded(false);
      location.hash = hash;
    }
    function row(item, now) {
      const {card, state} = item, li = el('li', undefined, 'canvas-schedules-row');
      li.dataset.status = state.status;
      const info = el('div', undefined, 'canvas-schedules-info');
      const title = el('bdi', card.title, 'canvas-schedules-name'); title.id = `canvas-schedules-name-${card.id}`;
      const meta = el('p', undefined, 'canvas-schedules-meta');
      meta.append(el('span', t(card.interval)));
      const next = window.CanvasState.nextRunLabel(state.next_run_at, now, lang());
      if (next) {
        const time = el('time', next.kind === 'due' ? t('schedules_due') : next.text);
        time.dateTime = state.next_run_at; time.title = new Date(state.next_run_at).toLocaleString(lang());
        meta.append(doc.createTextNode(' · '), time);
      }
      info.append(title, meta);
      if (state.reason) info.append(el('p', state.reason, 'canvas-schedules-reason'));
      const chip = el('span', t(state.status), 'canvas-schedules-chip'); chip.dataset.status = state.status;
      const actions = el('div', undefined, 'canvas-schedules-actions');
      const show = btn(t('schedules_show'), () => reveal(card.id), 'canvas-schedules-show');
      show.dataset.action = 'show'; show.dataset.cardId = card.id; show.setAttribute('aria-describedby', title.id);
      actions.append(show);
      if (state.status === 'needs_review') {
        const review = btn(t('schedules_review'), () => api.toggle(card), 'canvas-schedules-review');
        review.dataset.action = 'review'; review.dataset.cardId = card.id; review.setAttribute('aria-describedby', title.id);
        actions.append(review);
      }
      li.append(info, chip, actions);
      return li;
    }
    function render() {
      const state = api.document();
      const groups = state ? window.CanvasState.scheduleGroups(state.cards || [], state.edges || [], state.schedules || {}) : [];
      const items = groups.flatMap(group => group.items);
      aside.hidden = !items.length;
      if (!items.length) { region.replaceChildren(); return; }
      const now = Date.now();
      // Keep focus on the same control when the list is rebuilt under it.
      const active = region.contains(doc.activeElement) ? doc.activeElement : null;
      const focusKey = active?.dataset.action ? [active.dataset.action, active.dataset.cardId] : null;
      let soonest = null;
      for (const {state: s} of items) if (s.enabled && s.next_run_at && (soonest === null || Date.parse(s.next_run_at) < Date.parse(soonest))) soonest = s.next_run_at;
      const next = soonest ? window.CanvasState.nextRunLabel(soonest, now, lang()) : null;
      let text = `${t('schedules_title')} · ${new Intl.NumberFormat(lang()).format(items.length)}`;
      if (next?.kind === 'in') text += ` · ${t('schedules_next')} ${next.text}`;
      label.textContent = text;
      const sections = groups.map(group => {
        const section = el('section', undefined, 'canvas-schedules-group');
        const h3 = el('h3');
        if (group.target) {
          section.dataset.targetKind = group.target.kind;
          h3.append(el('small', t(group.target.kind)), el('bdi', group.target.title));
        } else { section.dataset.targetKind = 'none'; h3.textContent = t('schedules_unconnected'); }
        const list = el('ul');
        list.append(...group.items.map(item => row(item, now)));
        section.append(h3, list);
        return section;
      });
      region.replaceChildren(...sections);
      setExpanded(expanded);
      if (focusKey) region.querySelector(`[data-action="${focusKey[0]}"][data-card-id="${focusKey[1]}"]`)?.focus({preventScroll: true});
    }
    // Relative times go stale on an open page; refresh them once a minute while the panel shows.
    setInterval(() => { if (!aside.hidden && !doc.hidden) render(); }, 60000);
    return {render};
  }
})();
