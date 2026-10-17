(function (global, factory) {
  'use strict';
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  else global.CanvasGraph = api;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';
  const sources = new Set(['source', 'content', 'prompt', 'note', 'schedule']);
  const targets = new Set(['provider', 'agent']);
  function validate(cards, edges) {
    if (!Array.isArray(cards) || !Array.isArray(edges)) return 'invalid';
    if (cards.length > 100 || edges.length > 200) return 'limit';
    const nodes = new Map();
    for (const card of cards) {
      if (!card || typeof card.id !== 'string' || !card.id || nodes.has(card.id)) return 'invalid';
      nodes.set(card.id, card);
    }
    const seen = new Set(), scheduled = new Set();
    for (const edge of edges) {
      if (!edge || Object.keys(edge).length !== 2 || typeof edge.from !== 'string' || typeof edge.to !== 'string') return 'invalid';
      const from = nodes.get(edge.from), to = nodes.get(edge.to);
      if (!from || !to || from === to || !sources.has(from.kind) || !targets.has(to.kind)) return 'direction';
      const key = `${edge.from}\0${edge.to}`;
      if (seen.has(key)) return 'duplicate';
      seen.add(key);
      if (from.kind === 'schedule') {
        if (scheduled.has(from.id)) return 'schedule_target';
        scheduled.add(from.id);
      }
    }
    return null;
  }
  function add(cards, edges, edge) {
    const result = [...edges, {from: edge.from, to: edge.to}];
    const error = validate(cards, result);
    if (error) throw new Error(error);
    return result;
  }
  const withoutNode = (edges, id) => edges.filter(edge => edge.from !== id && edge.to !== id).map(edge => ({...edge}));
  const context = (cards, edges, id) => {
    const ids = new Set(edges.filter(edge => edge.to === id).map(edge => edge.from));
    return cards.filter(card => ids.has(card.id) && card.kind !== 'schedule');
  };
  return Object.freeze({validate, add, withoutNode, context});
});
