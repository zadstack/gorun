/* Local document history and recovery only. This module never queues a run, a grant or a network request. */
(function (root, factory) {
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  else root.CanvasEditing = api;
})(typeof window === 'object' ? window : globalThis, function () {
  'use strict';
  const PREFIX = 'gorun:canvas-draft:v1:', MAX_ENTRY_BYTES = 256 * 1024, MAX_TOTAL_BYTES = 2 * 1024 * 1024, MAX_ENTRIES = 20;
  const RETENTION_MS = 7 * 24 * 60 * 60 * 1000;
  const copy = value => JSON.parse(JSON.stringify(value));
  const size = value => new TextEncoder().encode(value).length;
  const snapshot = value => copy({title:value.title, cards:value.cards || [], edges:value.edges || []});
  const canonical = value => Array.isArray(value) ? value.map(canonical) : value && typeof value === 'object' ? Object.fromEntries(Object.keys(value).sort().map(key=>[key,canonical(value[key])])) : value;
  const same = (a, b) => JSON.stringify(canonical(snapshot(a))) === JSON.stringify(canonical(snapshot(b)));
  const id = () => globalThis.crypto.randomUUID();
  const resultAllowed = (desired, allowed) => desired.cards.every(card => card.kind !== 'result' || allowed.some(old => old.id === card.id && old.kind === 'result' && old.run_id === card.run_id && old.title === card.title && old.body === card.body));
  class History {
    constructor() { this.reset({title:'',cards:[],edges:[]}); }
    reset(document) { this.present=snapshot(document); this.past=[];this.future=[]; this.results=(document.cards || []).filter(c=>c.kind==='result').map(copy); }
    record(document) {
      const after=snapshot(document);
      if (same(after,this.present)) return;
      this.past.push({before:this.present,after});
      if(this.past.length>100)this.past.shift();
      this.future=[];this.present=after;
    }
    // ResizeObserver collision resolution belongs to the action that caused it, not another undo step.
    settle(document) { this.present=snapshot(document);if(this.past.length)this.past[this.past.length-1].after=this.present; }
    acknowledge(document) { this.results=(document.cards || []).filter(c=>c.kind==='result').map(copy); }
    canUndo(document) { const top=this.past.at(-1);return !!top && same(document,this.present) && resultAllowed(top.before,this.results); }
    canRedo(document) { const top=this.future.at(-1);return !!top && same(document,this.present) && resultAllowed(top.after,this.results); }
    undo(document) {
      if(!this.canUndo(document))throw Error('Document changed or a saved result cannot be restored');
      const entry=this.past.pop();this.future.push(entry);this.present=copy(entry.before);return copy(this.present);
    }
    redo(document) {
      if(!this.canRedo(document))throw Error('Document changed or a saved result cannot be restored');
      const entry=this.future.pop();this.past.push(entry);this.present=copy(entry.after);return copy(this.present);
    }
  }
  function validDocument(value) {
    if(!value||typeof value!=='object'||typeof value.title!=='string'||!Array.isArray(value.cards)||value.cards.length>100||!Array.isArray(value.edges)||value.edges.length>200)return false;
    const kinds=['note','prompt','checklist','link','content','widget','source','provider','agent','schedule','result'],ids=new Set();
    for(const card of value.cards) {
      if(!card||typeof card!=='object'||typeof card.id!=='string'||!(/^[a-f0-9]{8}-(?:[a-f0-9]{4}-){3}[a-f0-9]{12}$/).test(card.id)||ids.has(card.id)||!kinds.includes(card.kind)||typeof card.title!=='string'||typeof card.body!=='string'||!Number.isSafeInteger(card.x)||!Number.isSafeInteger(card.y)||card.x<0||card.y<0||card.x>10000||card.y>10000)return false;
      ids.add(card.id);
      for(const field of ['url','content_id','widget_id','source_type','generation_kind','preset_id','run_id','agent_id','provider_id','interval'])if(card[field]!==undefined&&typeof card[field]!=='string')return false;
      if(card.items!==undefined&&(!Array.isArray(card.items)||card.items.length>30||!card.items.every(item=>item&&typeof item.text==='string'&&typeof item.checked==='boolean')))return false;
      if(card.source_ids!==undefined&&(!Array.isArray(card.source_ids)||card.source_ids.length>5||!card.source_ids.every(item=>typeof item==='string')))return false;
      if(card.source_limit!==undefined&&!Number.isSafeInteger(card.source_limit))return false;
    }
    return value.edges.every(edge=>edge&&typeof edge.from==='string'&&typeof edge.to==='string'&&ids.has(edge.from)&&ids.has(edge.to));
  }
  class Journal {
    constructor(storage, scope, writer=id(), now=Date.now) {
      this.storage=storage;this.scope={origin:scope.origin,owner:scope.owner,board:scope.board};this.writer=writer;this.now=now;this.memory=null;this.archived=[];this.problem=false;
      this.key=PREFIX+JSON.stringify([scope.origin,scope.owner,scope.board,writer]);
    }
    decode(key, raw) {
      try {
        if(!raw||size(raw)>MAX_ENTRY_BYTES)return null;
        const value=JSON.parse(raw);
        if(value.version!==1 || !value.scope || typeof value.writer!=='string'||typeof value.revision!=='string'||!Number.isSafeInteger(value.changedAt)||value.changedAt<0||!Number.isFinite(new Date(value.changedAt).getTime())||!Number.isSafeInteger(value.baseVersion)||value.baseVersion<1 || !validDocument(value.document) || key!==PREFIX+JSON.stringify([value.scope.origin,value.scope.owner,value.scope.board,value.writer]))return null;
        if(this.now()-value.changedAt>RETENTION_MS) {this.storage.removeItem(key);return null;}
        return {...value,key,durable:true,diskRevision:value.revision};
      }catch(_){this.problem=true;return null;}
    }
    entries() {
      const found=[];
      try {
        const keys=Array.from({length:this.storage.length},(_,i)=>this.storage.key(i));
        for(const key of keys) {
          if(!key?.startsWith(PREFIX))continue;
          const entry=this.decode(key,this.storage.getItem(key));
          if(entry && JSON.stringify(entry.scope)===JSON.stringify(this.scope))found.push(entry);
        }
      }catch(_){this.problem=true;}
      for(const memory of [...this.archived,...(this.memory?[this.memory]:[])]) { const at=found.findIndex(e=>e.key===memory.key);if(at>=0)found.splice(at,1);found.push(memory); }
      return found.sort((a,b)=>b.changedAt-a.changedAt);
    }
    current() {return this.memory;}
    release() {
      if(this.memory&&!this.memory.durable)this.archived.push(this.memory);
      this.memory=null;this.writer=id();this.key=PREFIX+JSON.stringify([this.scope.origin,this.scope.owner,this.scope.board,this.writer]);
    }
    persist(entry) {
      const {key,durable,diskRevision,...stored}=entry, raw=JSON.stringify(stored);
      entry.durable=false;
      try {
        if(size(raw)>MAX_ENTRY_BYTES)throw Error('size');
        let total=size(raw),count=1;
        const keys=Array.from({length:this.storage.length},(_,i)=>this.storage.key(i));
        for(const other of keys) {
          if(other===this.key||!other?.startsWith(PREFIX))continue;
          const previous=this.decode(other,this.storage.getItem(other));
          // Bound this account on this origin, without deleting another tab's unexpired work.
          if(previous && previous.scope.origin===this.scope.origin && previous.scope.owner===this.scope.owner) {total+=size(this.storage.getItem(other)||'');count++;}
        }
        if(total>MAX_TOTAL_BYTES||count>MAX_ENTRIES)throw Error('limit');
        this.storage.setItem(this.key,raw);entry.durable=true;entry.diskRevision=entry.revision;
      }catch(_){this.problem=true;}
      return entry;
    }
    capture(document, base) {
      const previous=this.memory;
      this.memory={version:1,scope:this.scope,writer:this.writer,revision:id(),changedAt:this.now(),baseVersion:previous?.baseVersion || base.version,document:snapshot(document),key:this.key,durable:false,diskRevision:previous?.diskRevision};
      return copy(this.persist(this.memory));
    }
    acknowledge(ticket, saved) {
      if(!ticket||ticket.key!==this.key||!this.memory||!same(ticket.document,saved))return;
      if(this.memory.revision===ticket.revision) {this.discard(ticket.key,ticket.revision);return;}
      this.memory.baseVersion=Math.max(this.memory.baseVersion,saved.version);this.persist(this.memory);
    }
    discard(key, revision) {
      const local=key===this.key&&this.memory ? this.memory : this.archived.find(entry=>entry.key===key);
      const forget=()=>{if(key===this.key)this.memory=null;this.archived=this.archived.filter(entry=>entry.key!==key);return true;};
      try {
        const current=local || this.decode(key,this.storage.getItem(key));
        if(!current||current.revision!==revision||JSON.stringify(current.scope)!==JSON.stringify(this.scope))return false;
        if(!current.durable){
          // Discard memory even when storage is unavailable. Remove only the
          // revision this writer last stored; an unknown newer disk copy stays.
          try{const disk=this.decode(key,this.storage.getItem(key));if(disk&&(disk.revision===revision||disk.revision===current.diskRevision))this.storage.removeItem(key);}
          catch(_){this.problem=true;}
          return forget();
        }
        // Compare disk revision too: a stale action can never remove a newer stored revision.
        const disk=this.decode(key,this.storage.getItem(key));
        if(disk&&disk.revision!==revision)return false;
        this.storage.removeItem(key);
        return forget();
      }catch(_){this.problem=true;return false;}
    }
  }
  return {History,Journal,snapshot,same,MAX_ENTRY_BYTES,RETENTION_MS};
});
