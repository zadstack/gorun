'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const state = require('./canvas-state.js');
const now = 1_000_000_000;
const run = (node_id, status, extra) => ({id: `${node_id}-${status}`, node_id, status, created_at: '', finished_at: null, ...extra});
test('nodeStates maps every run status', () => {
  const runs = [
    run('n1','requested'), run('n2','queued'), run('n3','running'), run('n4','waiting_agent'),
    run('n5','waiting'), run('n6','waiting_tool'), run('n7','succeeded',{finished_at: new Date(now-1000).toISOString()}),
    run('n8','failed'), run('n9','uncertain'), run('n10','reviewed'), run('n11','cancelled'),
  ];
  assert.deepEqual(state.nodeStates(runs, now), {n1:'queued',n2:'queued',n3:'running',n4:'running',n5:'waiting',n6:'waiting',n7:'done',n8:'failed',n9:'failed'});
});
test('nodeStates uses only the newest run per node', () => {
  assert.deepEqual(state.nodeStates([run('a','failed'), run('a','running')], now), {a:'failed'});
});
test('done lapses after 60s', () => {
  const at = ms => [run('a','succeeded',{finished_at: new Date(ms).toISOString()})];
  assert.deepEqual(state.nodeStates(at(now-59_000), now), {a:'done'});
  assert.deepEqual(state.nodeStates(at(now-61_000), now), {});
  assert.equal(state.doneExpiry(at(now-59_000), now), now-59_000+60_000);
  assert.equal(state.doneExpiry(at(now-61_000), now), null);
  assert.equal(state.doneExpiry([], now), null);
});
test('isActive', () => {
  assert.equal(state.isActive([run('a','waiting_agent')]), true);
  assert.equal(state.isActive([run('a','waiting_tool')]), false);
  assert.equal(state.isActive([run('a','requested')]), false);
  assert.equal(state.isActive([run('a','queued')]), true);
  assert.equal(state.isActive([run('a','succeeded'), run('a','running')]), false);
});
test('edgeStates precedence', () => {
  const cards = [{id:'s',kind:'schedule'},{id:'n',kind:'note'},{id:'g',kind:'agent'}];
  const edges = [{from:'s',to:'g'},{from:'n',to:'g'}];
  const sch = {s:{enabled:true,status:'active'}};
  assert.deepEqual(state.edgeStates(cards, edges, {g:'running'}, sch), ['flowing','flowing']);
  assert.deepEqual(state.edgeStates(cards, edges, {g:'failed'}, sch), ['failed','failed']);
  assert.deepEqual(state.edgeStates(cards, edges, {}, sch), ['scheduled','idle']);
  assert.deepEqual(state.edgeStates(cards, edges, {}, {s:{enabled:false,status:'disabled'}}), ['idle','idle']);
  assert.deepEqual(state.edgeStates(cards, edges, {}, {}), ['idle','idle']);
});
test('scheduleGroups: unconnected', () => {
  const cards = [{id:'s1',kind:'schedule'},{id:'s2',kind:'schedule'},{id:'g',kind:'agent'}];
  const groups = state.scheduleGroups(cards, [{from:'s2',to:'missing'}], {});
  assert.equal(groups.length, 1);
  assert.equal(groups[0].target, null);
  assert.deepEqual(groups[0].items.map(i => i.card.id), ['s1','s2']);
  assert.deepEqual(groups[0].items[0].state, {enabled:false,status:'disabled'});
});
test('scheduleGroups groups two schedules on one agent', () => {
  const cards = [{id:'s1',kind:'schedule'},{id:'g1',kind:'agent'},{id:'s2',kind:'schedule'},{id:'g2',kind:'agent'},{id:'s3',kind:'schedule'},{id:'n',kind:'note'}];
  const edges = [{from:'s1',to:'g2'},{from:'s2',to:'g1'},{from:'s3',to:'g2'}];
  const sch = {s2:{enabled:true,status:'active'}};
  const groups = state.scheduleGroups(cards, edges, sch);
  assert.deepEqual(groups.map(g => g.target.id), ['g1','g2']);
  assert.deepEqual(groups[0].items.map(i => i.card.id), ['s2']);
  assert.deepEqual(groups[0].items[0].state, {enabled:true,status:'active'});
  assert.deepEqual(groups[1].items.map(i => i.card.id), ['s1','s3']);
});
test('nextRunLabel: past is due', () => {
  assert.deepEqual(state.nextRunLabel(new Date(now-1).toISOString(), now, 'en'), {kind:'due'});
  assert.equal(state.nextRunLabel('', now, 'en'), null);
  assert.equal(state.nextRunLabel('nope', now, 'en'), null);
  assert.deepEqual(state.nextRunLabel(new Date(now+42*60_000).toISOString(), now, 'en'), {kind:'in', text:'in 42 minutes'});
  assert.deepEqual(state.nextRunLabel(new Date(now+3*3_600_000).toISOString(), now, 'en'), {kind:'in', text:'in 3 hours'});
  assert.deepEqual(state.nextRunLabel(new Date(now+2*86_400_000).toISOString(), now, 'en'), {kind:'in', text:'in 2 days'});
});
test('lanes assigns kinds', () => {
  const kinds = ['source','content','link','checklist','widget','note','prompt','schedule','agent','provider','result','mystery'];
  const cards = kinds.map((kind, i) => ({id: kind, kind, x: 24, y: i * 24}));
  cards.push({id: 'attributed', kind: 'note', x: 24, y: 999});
  const heights = Object.fromEntries(cards.map(card => [card.id, 100]));
  const out = state.lanes(cards, heights, {attributed: {actor_name: 'Agent'}}, 'ltr');
  const laneOf = id => (out.positions[id].x - 24) / 336;
  assert.deepEqual(Object.fromEntries(cards.map(card => [card.id, laneOf(card.id)])), {
    source: 0, content: 0, link: 0, checklist: 0, widget: 0, note: 0, mystery: 0,
    prompt: 1, schedule: 1, agent: 2, provider: 2, result: 3, attributed: 3,
  });
  assert.deepEqual(out.columns, [
    {lane: 0, x: 24, count: 7}, {lane: 1, x: 360, count: 2}, {lane: 2, x: 696, count: 2}, {lane: 3, x: 1032, count: 2},
  ]);
});
test('lanes mirrors in rtl', () => {
  const cards = [{id: 'a', kind: 'agent', x: 0, y: 0}];
  assert.deepEqual(state.lanes(cards, {a: 100}, {}, 'ltr').positions.a, {x: 24 + 2 * 336, y: 72});
  assert.deepEqual(state.lanes(cards, {a: 100}, {}, 'rtl').positions.a, {x: 24 + 1 * 336, y: 72});
  assert.deepEqual(state.lanes(cards, {a: 100}, {}, 'rtl').columns.map(c => c.x), [24 + 3 * 336, 24 + 2 * 336, 24 + 336, 24]);
});
test('lanes keeps saved order and stacks by height', () => {
  const cards = [{id: 'low', kind: 'prompt', x: 0, y: 500}, {id: 'high', kind: 'prompt', x: 0, y: 100}];
  const {positions} = state.lanes(cards, {low: 200, high: 120}, {}, 'ltr');
  assert.deepEqual(positions, {high: {x: 360, y: 72}, low: {x: 360, y: 72 + 120 + 24}});
  const ties = [{id: 'b', kind: 'note', x: 10, y: 0}, {id: 'c', kind: 'note', x: 5, y: 0}, {id: 'a', kind: 'note', x: 5, y: 0}];
  const tied = state.lanes(ties, {a: 50, b: 50, c: 50}, null, 'ltr').positions;
  assert.deepEqual([tied.a.y, tied.c.y, tied.b.y], [72, 146, 220]);
});
test('lanes does not mutate cards', () => {
  const cards = [{id: 'p', kind: 'prompt', x: 48, y: 96, title: 'P'}, {id: 'n', kind: 'note', x: 0, y: 0}];
  const before = structuredClone(cards);
  state.lanes(cards, {p: 80, n: 80}, {n: {actor_name: 'x'}}, 'rtl');
  assert.deepEqual(cards, before);
});
test('laneOrder sorts by lane then saved order', () => {
  const cards = [
    {id: 'r', kind: 'result', x: 0, y: 0}, {id: 'g', kind: 'agent', x: 0, y: 0}, {id: 'p2', kind: 'prompt', x: 0, y: 300},
    {id: 'n', kind: 'note', x: 0, y: 50}, {id: 'p1', kind: 'prompt', x: 0, y: 100}, {id: 's', kind: 'source', x: 0, y: 900},
  ];
  assert.deepEqual(state.laneOrder(cards, {n: {actor_name: 'x'}}).map(card => card.id), ['s', 'p1', 'p2', 'g', 'r', 'n']);
});
test('nextRunLabel picks the unit after rounding (never "24 hours" or "60 minutes")', () => {
  const label = minutes => state.nextRunLabel(new Date(now + minutes * 60_000).toISOString(), now, 'en').text;
  // One day in the day unit; numeric:'auto' (kept from the existing formatter) words it "tomorrow".
  const oneDay = new Intl.RelativeTimeFormat('en', {numeric: 'auto'}).format(1, 'day');
  for (const m of [1410, 1425, 1439, 1440]) assert.equal(label(m), oneDay, `${m} minutes`);
  for (const m of [1410, 1439]) assert.doesNotMatch(label(m), /hour/);
  assert.equal(label(1409), 'in 23 hours');
  assert.equal(label(59.6), 'in 1 hour');
  assert.equal(label(59.4), 'in 59 minutes');
});
test('atLabel: time today, weekday this week, date beyond', () => {
  const at = ms => state.atLabel(new Date(now + ms).toISOString(), now, 'en-GB', 'UTC');
  assert.equal(at(42 * 60_000), '14:28');
  assert.match(at(2 * 86_400_000), /^Wed,? 13:46$/);
  assert.match(at(10 * 86_400_000), /^22 Jan,? 13:46$/);
  assert.equal(state.atLabel('', now, 'en', 'UTC'), null);
  assert.equal(state.atLabel('nope', now, 'en', 'UTC'), null);
});
test('atLabel compares calendar days, not 24-hour spans', () => {
  const late = Date.parse('2026-11-03T23:50:00Z');
  assert.match(state.atLabel('2026-11-04T00:10:00Z', late, 'en-GB', 'UTC'), /^Wed,? 00:10$/);
});
test('laneCardWidth fits four columns, between 240 and 300', () => {
  assert.equal(state.laneCardWidth(2000), 300);
  assert.equal(state.laneCardWidth(1150), 250);
  assert.equal(state.laneCardWidth(600), 240);
  const width = state.laneCardWidth(1150), {columns} = state.lanes([], {}, {}, 'ltr', width + 36);
  assert.ok(columns[3].x + width + 18 <= 1150);
});
