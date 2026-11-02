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
    let text;
    if (minutes < 60) text = fmt.format(minutes, 'minute');
    else if (minutes < 1440) text = fmt.format(Math.round(minutes / 60), 'hour');
    else text = fmt.format(Math.round(minutes / 1440), 'day');
    return {kind: 'in', text};
  }
  return Object.freeze({nodeStates, doneExpiry, isActive, edgeStates, scheduleGroups, nextRunLabel});
});
