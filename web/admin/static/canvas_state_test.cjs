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
