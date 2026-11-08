'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const {History, Journal, snapshot, MAX_ENTRY_BYTES, RETENTION_MS} = require('./canvas-editing.js');
const doc = (title = 'Board', cards = []) => ({version: 1, title, cards, edges: [], results: {secret: 'not journaled'}});
const note = {id:'11111111-1111-4111-8111-111111111111', kind:'note',title:'Note',body:'Local work',x:24,y:24};
class Storage {
  data = new Map();
  get length() { return this.data.size; }
  key(i) { return [...this.data.keys()][i] ?? null; }
  getItem(key) { return this.data.get(key) ?? null; }
  setItem(key, value) { this.data.set(key, value); }
  removeItem(key) { this.data.delete(key); }
}
const scope = {origin:'https://example.test',owner:'owner',board:'board'};
const journal = (storage, writer='tab-a', now=()=>1000) => new Journal(storage, scope, writer, now);
test('journal captures before debounce, reload requires explicit recovery, and stores document data only', () => {
  const storage = new Storage(), first=journal(storage), ticket=first.capture(doc('Typed', [note]), doc());
  assert.ok(ticket.durable);
  const entries=journal(storage,'tab-b').entries();
  assert.equal(entries.length,1);
  assert.equal(entries[0].document.title,'Typed');
  assert.equal(entries[0].baseVersion,1);
  assert.equal(JSON.stringify(entries).includes('secret'),false);
});
test('old acknowledgment rebases but never clears newer work', () => {
  const storage=new Storage(), j=journal(storage), first=j.capture(doc('First'),doc());
  const second=j.capture(doc('Second'),doc());
  j.acknowledge(first,{...doc('First'),version:2});
  assert.equal(j.entries()[0].document.title,'Second');
  assert.equal(j.entries()[0].baseVersion,2);
  j.acknowledge(second,{...doc('Second'),version:3});
  assert.equal(j.entries().length,0);
});
test('journals are isolated by origin, owner, board and document lifetime; stale discard preserves newer revisions', () => {
  const storage=new Storage(), a=journal(storage), b=journal(storage,'tab-b');
  const first=a.capture(doc('A'),doc()); b.capture(doc('B'),doc());
  const second=a.capture(doc('A newer'),doc());
  assert.equal(a.discard(first.key,first.revision),false);
  b.acknowledge(b.current(),{...doc('B'),version:2});
  assert.equal(a.entries().length,1);
  assert.equal(a.entries()[0].document.title,'A newer');
  for (const changed of [{owner:'other'},{board:'other'},{origin:'https://other.test'}]) assert.equal(new Journal(storage,{...scope,...changed},'x',()=>1000).entries().length,0);
  assert.equal(a.discard(second.key,second.revision),true);
});
test('storage failure and size limits keep an in-memory downloadable copy without evicting another tab', () => {
  const storage=new Storage(), a=journal(storage); a.capture(doc('Protected'),doc());
  const full=journal(storage,'tab-b');
  const ticket=full.capture(doc('Too large',[{...note,body:'x'.repeat(MAX_ENTRY_BYTES)}]),doc());
  assert.equal(ticket.durable,false);
  assert.equal(full.entries().find(e=>e.writer==='tab-b').document.title,'Too large');
  assert.equal(journal(storage,'tab-c').entries().length,1);
  storage.setItem=()=>{throw Error('quota');};
  assert.equal(a.capture(doc('Still here'),doc()).durable,false);
  assert.equal(a.entries().find(e=>e.writer==='tab-a').document.title,'Still here');
});
test('expired journals are pruned, malformed or oversized storage is never restored', () => {
  const storage=new Storage(); journal(storage).capture(doc('Expired'),doc());
  assert.equal(journal(storage,'later',()=>1000+RETENTION_MS+1).entries().length,0);
  storage.setItem('gorun:canvas-draft:v1:broken','{');
  assert.equal(journal(storage).entries().length,0);
});
test('history treats arrange and edges as one action, supports redo, and settles automatic geometry into the action', () => {
  const history=new History(); const before=doc('Board',[note]);history.reset(before);
  const after=doc('Board',[{...note,x:360}]);after.edges=[{from:'a',to:'b'}];history.record(after);
  history.settle(doc('Board',[{...note,x:384}]));
  const back=history.undo(snapshot(doc('Board',[{...note,x:384}])));assert.equal(back.cards[0].x,24);
  assert.equal(history.redo(back).cards[0].x,384);
});
test('history refuses a changed current document and never reinstates a saved immutable result', () => {
  const history=new History(), result={...note,kind:'result',run_id:'run'};
  history.reset(doc('Board',[result])); history.record(doc('Board',[]));
  assert.equal(history.canUndo(doc('Board',[])),true);
  history.acknowledge(doc('Board',[]));
  assert.equal(history.canUndo(doc('Board',[])),false);
  history.reset(doc('Board',[note]));history.record(doc('Changed',[note]));
  assert.throws(()=>history.undo(doc('Remote',[note])),/changed/);
  assert.equal(history.canUndo(doc('Changed',[note])),true);
});
test('explicit same-page reload releases the active writer and preserves every old local copy',()=>{
 const storage=new Storage(), j=journal(storage);j.capture(doc('Old local',[note]),doc());j.release();
 assert.equal(j.current(),null);assert.equal(j.entries()[0].document.title,'Old local');
 j.capture(doc('New local',[note]),doc());assert.equal(j.entries().length,2);
});
test('corrupted valid JSON never enters the editor, including null cards, items and edges',()=>{
 for(const corrupt of [d=>d.cards=[null],d=>d.cards=[{...note,body:null}],d=>d.cards=[{...note,x:Infinity}],d=>d.cards=[{...note,items:[null]}],d=>d.edges=[null]]){
  const storage=new Storage(), j=journal(storage), ticket=j.capture(doc('Valid',[note]),doc());const stored=JSON.parse(storage.getItem(ticket.key));corrupt(stored.document);storage.setItem(ticket.key,JSON.stringify(stored));
  assert.equal(journal(storage,'fresh').entries().length,0);
 }
});
test('server JSON property ordering cannot keep an acknowledged draft alive',()=>{
 const storage=new Storage(),j=journal(storage),ticket=j.capture(doc('Board',[{x:24,y:24,...note}]),doc());
 j.acknowledge(ticket,{...doc('Board',[{kind:'note',body:note.body,title:note.title,id:note.id,x:24,y:24}]),version:2});
 assert.equal(j.entries().length,0);
});
test('invalid date timestamps never enter the recovery view',()=>{
 for(const changedAt of [-1,8640000000000001,Number.MAX_SAFE_INTEGER]){
  const storage=new Storage(), j=journal(storage),ticket=j.capture(doc('Valid',[note]),doc()),stored=JSON.parse(storage.getItem(ticket.key));stored.changedAt=changedAt;storage.setItem(ticket.key,JSON.stringify(stored));
  assert.equal(journal(storage,'fresh').entries().length,0);
 }
});
test('account entry and aggregate byte limits retain existing drafts and keep new text in memory',()=>{
 for(const [count,body] of [[20,'small'],[9,'x'.repeat(230000)]]){
  const storage=new Storage();for(let index=0;index<count;index++)assert.equal(journal(storage,'tab-'+index).capture(doc('Protected '+index,[{...note,body}]),doc()).durable,true);
  const next=journal(storage,'overflow'),ticket=next.capture(doc('New work',[{...note,body}]),doc());assert.equal(ticket.durable,false);
  assert.equal(journal(storage,'reader').entries().length,count);assert.equal(next.entries().length,count+1);assert.equal(next.entries().find(entry=>entry.writer==='overflow').document.title,'New work');
 }
});
test('quota-failed writing can be discarded after release without leaving its older disk revision',()=>{
 const storage=new Storage(),j=journal(storage);j.capture(doc('Old durable'),doc());storage.setItem=()=>{throw Error('quota');};const newer=j.capture(doc('New memory'),doc());
 j.release();assert.equal(j.entries()[0].document.title,'New memory');assert.equal(j.discard(newer.key,newer.revision),true);assert.equal(j.entries().length,0);assert.equal(storage.length,0);
});
test('in-memory discard still works when browser storage reads or removals fail',()=>{
 for(const failing of ['getItem','removeItem']){
  const storage=new Storage(),j=journal(storage);j.capture(doc('Old durable'),doc());storage.setItem=()=>{throw Error('quota');};const newer=j.capture(doc('New memory'),doc());j.release();storage[failing]=()=>{throw Error('blocked');};
  assert.equal(j.discard(newer.key,newer.revision),true);assert.equal(j.archived.length,0);assert.equal(j.problem,true);
 }
});
test('discarding non-durable memory never erases an unknown newer disk revision',()=>{
 const storage=new Storage(),j=journal(storage),old=j.capture(doc('Old durable'),doc());const originalSet=storage.setItem.bind(storage);storage.setItem=()=>{throw Error('quota');};const newer=j.capture(doc('New memory'),doc());
 const disk=JSON.parse(storage.getItem(old.key));disk.revision='unknown-newer';disk.document.title='Newer disk';originalSet(old.key,JSON.stringify(disk));j.release();
 assert.equal(j.discard(newer.key,newer.revision),true);assert.equal(j.entries()[0].document.title,'Newer disk');
});
