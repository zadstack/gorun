(function (root) {
  'use strict';
  const end = (items, i) => { let j=i+1; while(j<items.length && items[j].depth>items[i].depth) j++; return j; };
  function parentIndex(items, i) { for(let j=i-1;j>=0;j--) if(items[j].depth<items[i].depth) return j; return -1; }
  function sibling(items, i, direction) {
    if(direction>0) {const j=end(items,i);return j<items.length && items[j].depth===items[i].depth ? j : -1;}
    for(let j=i-1;j>=0;j--) {if(items[j].depth<items[i].depth) break;if(items[j].depth===items[i].depth) return j;} return -1;
  }
  function moveBranch(items, from, target, mode) {
    if(!items[from] || !['before','after','inside','root'].includes(mode)) return items;
    const stop=end(items,from);
    if(mode!=='root' && (!items[target] || (target>=from && target<stop))) return items;
    const depth=mode==='root'?0:items[target].depth+(mode==='inside'?1:0);
    const delta=depth-items[from].depth;
    if(items.slice(from,stop).some(i=>i.depth+delta>5)) return items;
    let at=mode==='root'?items.length:mode==='before'?target:end(items,target);
    if(at>from) at-=stop-from;
    const next=items.map(i=>({...i}));const branch=next.splice(from,stop-from).map(i=>({...i,depth:i.depth+delta}));
    next.splice(at,0,...branch);return next;
  }
  function removeItem(items,i) { const stop=end(items,i);return items.filter((_,j)=>j!==i).map((row,j)=>({...row,depth:row.depth-(j>=i && j<stop-1?1:0)})); }
  function toTree(items) {
    const result=[], stack=[];
    for(const it of items) {
      const node={id:it.id||'',label:it.label||'',kind:it.kind||'custom',ref_id:it.ref_id||'',url:it.url||'',css_class:it.css_class||'',open_in_new:!!it.open_in_new,children:[]};
      while(stack.length>it.depth) stack.pop();
      (stack.length?stack[stack.length-1].children:result).push(node);stack.push(node);
    } return result;
  }
  if(typeof module!=='undefined' && module.exports) module.exports={moveBranch,sibling,parentIndex,removeItem,toTree};
  root.menuEditor = () => ({
    element:null,labels:{},items:[],open:-1,dirty:false,seq:0,maxDepth:5,dragKey:null,dropKey:'',announcement:'',submitting:false,
    init() {
      this.element=this.$el;this.labels={...this.$el.dataset};this.dirty=this.labels.invalid==='true';
      this.items=JSON.parse(this.$el.dataset.items||'[]').map(it=>({...it,_k:++this.seq}));
      this.beforeLeave=e=>{if(this.dirty&&!this.submitting){e.preventDefault();e.returnValue='';}};
      window.addEventListener('beforeunload',this.beforeLeave);
    },
    destroy(){window.removeEventListener('beforeunload',this.beforeLeave);},
    block(i){return end(this.items,i)-i;},
    title(it){return it.label||it.hint||this.labels.untitled;},
    parent(i){return parentIndex(this.items,i);},
    parentTitle(i){const p=this.parent(i);return p<0?this.labels.root:this.title(this.items[p]);},
    sibling(i,d){return sibling(this.items,i,d);},
    canIndent(i){const p=sibling(this.items,i,-1);return p>=0 && Math.max(...this.items.slice(i,end(this.items,i)).map(x=>x.depth))<5;},
    parentOptions(i){const stop=end(this.items,i),height=Math.max(...this.items.slice(i,stop).map(x=>x.depth))-this.items[i].depth;return this.items.filter((it,j)=>(j<i||j>=stop)&&it.depth+1+height<=5);},
    change(next,key){if(next===this.items){this.announcement=this.labels.invalidMove;return;}this.items=next;this.open=-1;this.dirty=true;this.announcement=this.labels.changed;this.$nextTick(()=>this.element.querySelector(`[data-menu-key="${key}"] [data-handle]`)?.focus({preventScroll:true}));},
    move(i,target,mode){this.change(moveBranch(this.items,i,target,mode),this.items[i]._k);},
    up(i){const j=this.sibling(i,-1);if(j>=0)this.move(i,j,'before');},
    down(i){const j=this.sibling(i,1);if(j>=0)this.move(i,j,'after');},
    indent(i){if(this.canIndent(i))this.move(i,this.sibling(i,-1),'inside');},
    outdent(i){const p=this.parent(i);if(p>=0)this.move(i,p,'after');},
    reparent(i,key){if(key==='root'){if(this.items[i].depth!==0)this.move(i,-1,'root');}else{const target=this.items.findIndex(it=>String(it._k)===key);if(target!==this.parent(i))this.move(i,target,'inside');}},
    remove(i){const key=this.items[Math.max(0,i-1)]?._k;this.change(removeItem(this.items,i),key);},
    startDrag(event,it){this.dragKey=it._k;event.dataTransfer.effectAllowed='move';event.dataTransfer.setData('application/x-gorun-menu-item',String(it._k));},
    dragOver(event,i,mode){const from=this.items.findIndex(it=>it._k===this.dragKey);if(from<0||moveBranch(this.items,from,i,mode)===this.items)return;event.preventDefault();event.dataTransfer.dropEffect='move';this.dropKey=`${this.items[i]._k}:${mode}`;},
    drop(event,i,mode){event.preventDefault();const from=this.items.findIndex(it=>it._k===this.dragKey);if(from>=0&&event.dataTransfer.getData('application/x-gorun-menu-item')===String(this.dragKey))this.move(from,i,mode);this.endDrag();},
    endDrag(){this.dragKey=null;this.dropKey='';},
    add(it){if(this.items.length>=500)return;this.items.push({id:'',label:it.label||'',kind:it.kind||'custom',ref_id:it.ref_id||'',url:it.url||'',css_class:'',open_in_new:false,depth:0,hint:it.hint||'',_k:++this.seq});this.dirty=true;this.open=this.items.length-1;this.announcement=this.labels.changed;this.$nextTick(()=>this.element.querySelector(`#label-${this.seq}`)?.focus());},
    toTree(){return toTree(this.items);},
    save(event){if(this.dragKey!==null){event.preventDefault();return;}this.$refs.json.value=JSON.stringify(this.toTree());this.submitting=true;}
  });
})(typeof window!=='undefined'?window:globalThis);
