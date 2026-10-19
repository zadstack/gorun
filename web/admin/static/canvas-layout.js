(function (global, factory) {
  'use strict';
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  else global.CanvasLayout = api;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';
  const WIDTH = 300, GAP = 24, COLUMN = 336, LIMIT = 10000;
  const height = (heights, id) => Math.max(80, heights[id] || 240);
  const overlap = (a, b, gap = GAP) => a.x < b.x + b.width + gap && a.x + a.width + gap > b.x && a.y < b.y + b.height + gap && a.y + a.height + gap > b.y;
  function resolve(cards, heights = {}) {
    const placed = [], positions = {};
    for (const card of [...cards].sort((a,b) => a.y-b.y || a.x-b.x || a.id.localeCompare(b.id))) {
      const box = {x: Math.max(0,Math.min(LIMIT,card.x)), y: Math.max(0,Math.min(LIMIT,card.y)), width:WIDTH, height:height(heights,card.id)};
      let tries = 0;
      while (tries++ < 500) {
        const blockers = placed.filter(other => overlap(box,other));
        if (!blockers.length) break;
        box.y = Math.max(...blockers.map(other => other.y+other.height+GAP));
        if (box.y+box.height > LIMIT) { box.x = Math.min(LIMIT,box.x+COLUMN); box.y=GAP; }
      }
      positions[card.id] = {x:box.x,y:box.y}; placed.push(box);
    }
    return positions;
  }
  function arrange(cards, edges, heights = {}, provenance = {}) {
    const targets = new Set(edges.map(edge=>edge.to));
    const inputs = new Set(edges.map(edge=>edge.from));
    const hasWork = targets.size || cards.some(card=>['agent','provider'].includes(card.kind));
    const next = new Map(), positions={};
    const rank=card=>provenance[card.id]||card.kind==='result'?-1:card.kind==='checklist'?1:0;
    const ordered=hasWork?[...cards].sort((a,b)=>rank(a)-rank(b)):cards;
    ordered.forEach((card,index) => {
      let column = !hasWork ? index%3 : (targets.has(card.id) || ['agent','provider'].includes(card.kind) ? 1 : provenance[card.id] || card.kind==='result' || (!inputs.has(card.id) && card.kind==='checklist') ? 2 : 0);
      let y = next.get(column) || GAP;
      while (y+height(heights,card.id)>LIMIT) { column+=3; y=next.get(column)||GAP; }
      positions[card.id]={x:GAP+column*COLUMN,y};
      next.set(column,y+height(heights,card.id)+GAP);
    });
    return positions;
  }
  function ports(boxes, edges) {
    const byID = new Map(boxes.map(box=>[box.id,box]));
    const groups = new Map(), result=[];
    edges.forEach((edge,index)=>{
      const from=byID.get(edge.from),to=byID.get(edge.to); if(!from||!to)return;
      const fx=from.x+from.width/2,tx=to.x+to.width/2;
      const sides=Math.abs(fx-tx)>WIDTH/2 ? (tx>fx?['right','left']:['left','right']) : (to.y>from.y?['bottom','top']:['top','bottom']);
      const pair={edge,index,from:null,to:null};result.push(pair);
      [[from,to,sides[0],'from'],[to,from,sides[1],'to']].forEach(([box,other,side,end])=>{
        const key=box.id+':'+side; if(!groups.has(key))groups.set(key,[]);
        groups.get(key).push({box,other,side,end,pair});
      });
    });
    for(const group of groups.values()) {
      const horizontal=['top','bottom'].includes(group[0].side);
      group.sort((a,b)=>(horizontal?a.other.x-b.other.x:a.other.y-b.other.y)||a.pair.index-b.pair.index);
      group.forEach((entry,index)=>{
        const {box,side}=entry, fraction=(index+1)/(group.length+1);
        const point=horizontal?{x:box.x+box.width*fraction,y:side==='top'?box.y:box.y+box.height}:{x:side==='left'?box.x:box.x+box.width,y:box.y+box.height*fraction};
        entry.pair[entry.end]={...point,side};
      });
    }
    return result;
  }
  const stub = point => ({x:point.x+({right:12,left:-12}[point.side]||0),y:point.y+({bottom:12,top:-12}[point.side]||0)});
  function clear(a,b,boxes) {
    return boxes.every(box=>{
      const left=box.x-5,right=box.x+box.width+5,top=box.y-5,bottom=box.y+box.height+5;
      if(a.y===b.y) return !(a.y>top&&a.y<bottom&&Math.max(a.x,b.x)>left&&Math.min(a.x,b.x)<right);
      if(a.x===b.x) return !(a.x>left&&a.x<right&&Math.max(a.y,b.y)>top&&Math.min(a.y,b.y)<bottom);
      return false;
    });
  }
  function compact(points) {
    const out=[];
    for(const p of points) {
      if(out.length&&out.at(-1).x===p.x&&out.at(-1).y===p.y)continue;
      if(out.length>1) {const a=out.at(-2),b=out.at(-1);if((a.x===b.x&&b.x===p.x)||(a.y===b.y&&b.y===p.y))out.pop();}
      out.push(p);
    }
    return out;
  }
  function route(pair, boxes) {
    const start=stub(pair.from),end=stub(pair.to);
    const middle=(start.x+end.x)/2;
    const candidates=[
      [start,{x:middle,y:start.y},{x:middle,y:end.y},end],
      [start,{x:start.x,y:end.y},end],
      [start,{x:end.x,y:start.y},end],
    ];
    for(const points of candidates) if(points.slice(1).every((point,index)=>clear(points[index],point,boxes)))return compact([pair.from,...points,pair.to]);
    // Bounded orthogonal search around actual card boundaries. A congested
    // canvas reports a routing warning instead of drawing through its cards.
    const xs=[...new Set([0,start.x,end.x,...boxes.flatMap(b=>[Math.max(0,b.x-12),b.x,b.x+b.width,b.x+b.width+12])])].sort((a,b)=>a-b);
    const ys=[...new Set([0,start.y,end.y,...boxes.flatMap(b=>[Math.max(0,b.y-12),b.y,b.y+b.height,b.y+b.height+12])])].sort((a,b)=>a-b);
    const ny=ys.length,key=(x,y)=>x*ny+y,point=k=>({x:xs[Math.floor(k/ny)],y:ys[k%ny]});
    const first=key(xs.indexOf(start.x),ys.indexOf(start.y)),last=key(xs.indexOf(end.x),ys.indexOf(end.y));
    const distance=p=>Math.abs(p.x-end.x)+Math.abs(p.y-end.y);
    const best=new Map([[first,0]]),previous=new Map(),heap=[];
    function push(item){heap.push(item);let i=heap.length-1;while(i){const p=(i-1)>>1;if(heap[p].f<=item.f)break;heap[i]=heap[p];i=p;}heap[i]=item;}
    function pop(){const top=heap[0],tail=heap.pop();if(heap.length){let i=0;while(i*2+1<heap.length){let child=i*2+1;if(child+1<heap.length&&heap[child+1].f<heap[child].f)child++;if(heap[child].f>=tail.f)break;heap[i]=heap[child];i=child;}heap[i]=tail;}return top;}
    push({key:first,g:0,f:distance(start)});
    let visits=0;
    while(heap.length&&visits++<20000){
      const current=pop();if(best.get(current.key)!==current.g)continue;
      if(current.key===last){const points=[];let k=last;while(k!==undefined){points.push(point(k));k=previous.get(k);}return compact([pair.from,...points.reverse(),pair.to]);}
      const x=Math.floor(current.key/ny),y=current.key%ny,a=point(current.key);
      for(const [nx,nyIndex] of [[x-1,y],[x+1,y],[x,y-1],[x,y+1]]){
        if(nx<0||nx>=xs.length||nyIndex<0||nyIndex>=ys.length)continue;
        const k=key(nx,nyIndex),b=point(k);if(!clear(a,b,boxes))continue;
        const g=current.g+Math.abs(a.x-b.x)+Math.abs(a.y-b.y);
        if(g>=(best.get(k)??Infinity))continue;
        best.set(k,g);previous.set(k,current.key);push({key:k,g,f:g+distance(b)});
      }
    }
    return null;
  }
  return Object.freeze({WIDTH,overlap,resolve,arrange,ports,route});
});
