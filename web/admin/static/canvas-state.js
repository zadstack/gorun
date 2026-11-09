(function (global, factory) {
  'use strict';
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  else global.CanvasState = api;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';
  const DONE_MS = 60000;
  const MAP = {
    requested: 'queued', queued: 'queued',
    running: 'running', waiting_agent: 'running',
    waiting: 'waiting', waiting_tool: 'waiting',
    failed: 'failed', uncertain: 'failed',
  };
  const ACTIVE = new Set(['queued', 'running', 'waiting_agent']);
  function latest(runs) {
    const seen = new Map();
    for (const run of runs || []) if (run && run.node_id && !seen.has(run.node_id)) seen.set(run.node_id, run);
    return seen;
  }
  const finished = run => {
    const t = Date.parse(run.finished_at || '');
    return Number.isNaN(t) ? null : t;
  };
  function nodeStates(runs, now) {
    const out = {};
    for (const [id, run] of latest(runs)) {
      if (run.status === 'succeeded') {
        const t = finished(run);
        if (t !== null && now - t < DONE_MS) out[id] = 'done';
      } else if (MAP[run.status]) out[id] = MAP[run.status];
    }
    return out;
  }
  function doneExpiry(runs, now) {
    let best = null;
    for (const run of latest(runs).values()) {
      if (run.status !== 'succeeded') continue;
      const t = finished(run);
      if (t === null || now - t >= DONE_MS) continue;
      if (best === null || t + DONE_MS < best) best = t + DONE_MS;
    }
    return best;
  }
  const isActive = runs => [...latest(runs).values()].some(run => ACTIVE.has(run.status));
  function edgeStates(cards, edges, states, schedules) {
    const kinds = new Map((cards || []).map(card => [card.id, card.kind]));
    return (edges || []).map(edge => {
      const to = (states || {})[edge.to];
      if (to === 'running') return 'flowing';
      if (to === 'failed') return 'failed';
      if (kinds.get(edge.from) === 'schedule' && schedules && schedules[edge.from] && schedules[edge.from].enabled) return 'scheduled';
      return 'idle';
    });
  }
  function scheduleGroups(cards, edges, schedules) {
    const byId = new Map(cards.map((card, i) => [card.id, {card, i}]));
    const targetOf = new Map();
    for (const edge of edges) if (!targetOf.has(edge.from)) targetOf.set(edge.from, byId.get(edge.to));
    const groups = new Map(), orphan = [];
    for (const card of cards) {
      if (card.kind !== 'schedule') continue;
      const item = {card, state: (schedules && schedules[card.id]) || {enabled: false, status: 'disabled'}};
      const target = targetOf.get(card.id);
      if (!target) { orphan.push(item); continue; }
      if (!groups.has(target.i)) groups.set(target.i, {target: target.card, items: []});
      groups.get(target.i).items.push(item);
    }
    const out = [...groups.entries()].sort((a, b) => a[0] - b[0]).map(entry => entry[1]);
    if (orphan.length) out.push({target: null, items: orphan});
    return out;
  }
  function nextRunLabel(iso, now, lang) {
    const t = Date.parse(iso || '');
    if (!iso || Number.isNaN(t)) return null;
    if (t <= now) return {kind: 'due'};
    const minutes = Math.max(1, Math.round((t - now) / 60000));
    const fmt = new Intl.RelativeTimeFormat(lang, {numeric: 'auto'});
    // Round first, then pick the unit, so 1410-1439 minutes reads "in 1 day", never "in 24 hours".
    const hours = Math.round(minutes / 60);
    let text;
    if (minutes < 60) text = fmt.format(minutes, 'minute');
    else if (hours < 24) text = fmt.format(hours, 'hour');
    else text = fmt.format(Math.max(1, Math.round(minutes / 1440)), 'day');
    return {kind: 'in', text};
  }
  // The clock time beside a relative label: time only today, weekday and time within the week, else the date.
  // Calendar days are compared in the display time zone, so 23:50 to 00:10 reads as tomorrow, not today.
  function atLabel(iso, now, lang, timeZone) {
    const t = Date.parse(iso || '');
    if (!iso || Number.isNaN(t)) return null;
    const day = ms => Date.parse(new Intl.DateTimeFormat('en-CA', {timeZone, year: 'numeric', month: '2-digit', day: '2-digit'}).format(ms));
    const days = Math.round((day(t) - day(now)) / 86400000), time = {hour: '2-digit', minute: '2-digit', timeZone};
    if (days === 0) return new Intl.DateTimeFormat(lang, time).format(t);
    if (days > 0 && days < 7) return new Intl.DateTimeFormat(lang, {...time, weekday: 'short'}).format(t);
    return new Intl.DateTimeFormat(lang, {...time, month: 'short', day: 'numeric'}).format(t);
  }
  // Lanes view: positions are computed for display only and never written back to the cards.
  const LANE = {source: 0, content: 0, link: 0, checklist: 0, widget: 0, note: 0, prompt: 1, schedule: 1, agent: 2, provider: 2, result: 3};
  function laneOf(card, provenance) {
    if (card.kind === 'note' && provenance && provenance[card.id]) return 3;
    return LANE[card.kind] ?? 0;
  }
  const savedOrder = (a, b) => a.y - b.y || a.x - b.x || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0);
  const laneOrder = (cards, provenance) => [...(cards || [])].sort((a, b) => laneOf(a, provenance) - laneOf(b, provenance) || savedOrder(a, b));
  // Lane cards shrink from 300px to 240px so four columns fit the viewport: 24px start, 36px gutters, 18px end.
  const laneCardWidth = available => Math.max(240, Math.min(300, Math.floor((available - 150) / 4)));
  // The 240px card width is a floor, so a narrow workspace cannot fit four columns with
  // fixed 36px gutters (24 + 4 cards + 3 gutters + 18 + lane overhangs). The gutter shrinks
  // to the remaining space instead of letting the lane view scroll sideways. The last
  // column ends at 24 + 3*pitch + width + 18, so that sum is what must stay inside the viewport.
  const laneGutter = (available, width) => Math.max(12, Math.min(36, Math.floor((available - 42 - 4 * width) / 3)));
  function lanes(cards, heights, provenance, dir, pitch = 336) {
    const columns = [0, 1, 2, 3].map(lane => ({lane, x: 24 + (dir === 'rtl' ? 3 - lane : lane) * pitch, count: 0}));
    const positions = {}, next = [72, 72, 72, 72];
    for (const card of [...(cards || [])].sort(savedOrder)) {
      const lane = laneOf(card, provenance);
      positions[card.id] = {x: columns[lane].x, y: next[lane]};
      next[lane] += ((heights && heights[card.id]) || 240) + 24;
      columns[lane].count++;
    }
    return {positions, columns};
  }
  return Object.freeze({nodeStates, doneExpiry, isActive, edgeStates, scheduleGroups, nextRunLabel, atLabel, laneOf, laneOrder, laneCardWidth, laneGutter, lanes});
});
