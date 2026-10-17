'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const graph = require('./canvas-graph.js');
const cards = [{id:'source',kind:'source'},{id:'content',kind:'content'},{id:'note',kind:'note'},{id:'prompt',kind:'prompt'},{id:'provider',kind:'provider'},{id:'other',kind:'provider'},{id:'schedule',kind:'schedule'},{id:'link',kind:'link'},{id:'agent',kind:'agent'}];
test('connects supported context and schedules only to a provider', () => {
  for (const from of ['source','content','note','prompt','schedule']) for (const to of ['provider','agent']) assert.equal(graph.validate(cards,[{from,to}]), null);
  for (const edge of [{from:'provider',to:'other'},{from:'link',to:'provider'},{from:'source',to:'prompt'},{from:'provider',to:'provider'},{from:'missing',to:'provider'}]) assert.ok(graph.validate(cards,[edge]));
});
test('rejects duplicate edges and multi-target schedules without mutation', () => {
  const edges=[{from:'schedule',to:'provider'}];
  assert.equal(graph.validate(cards,[...edges,...edges]),'duplicate');
  assert.equal(graph.validate(cards,[...edges,{from:'schedule',to:'other'}]),'schedule_target');
  assert.deepEqual(edges,[{from:'schedule',to:'provider'}]);
});
test('enforces node and edge limits and malformed boundaries', () => {
  assert.ok(graph.validate(null,[])); assert.ok(graph.validate(cards,null));
  assert.ok(graph.validate([...cards,cards[0]],[]));
  assert.equal(graph.validate(Array.from({length:101},(_,i)=>({id:String(i),kind:'note'})),[]),'limit');
  assert.equal(graph.validate(cards,Array.from({length:201},()=>({from:'source',to:'provider'}))),'limit');
  assert.ok(graph.validate(cards,[{from:'source',to:'provider',extra:true}]));
});
test('remove node prunes only its edges, leaving input unchanged', () => {
  const edges=[{from:'source',to:'provider'},{from:'note',to:'other'}];
  assert.deepEqual(graph.withoutNode(edges,'provider'),[{from:'note',to:'other'}]);
  assert.equal(edges.length,2);
});
test('context is only directly connected eligible nodes, stable card order', () => {
  const edges=[{from:'prompt',to:'provider'},{from:'schedule',to:'provider'},{from:'note',to:'provider'},{from:'source',to:'other'}];
  assert.deepEqual(graph.context(cards,edges,'provider').map(c=>c.id),['note','prompt']);
});
test('add is immutable and refuses invalid or duplicate links', () => {
  const edges=[]; const added=graph.add(cards,edges,{from:'source',to:'provider'});
  assert.equal(edges.length,0); assert.equal(added.length,1);
  assert.throws(()=>graph.add(cards,added,{from:'source',to:'provider'}),/duplicate/);
});
