const {test}=require('node:test');
const assert=require('node:assert/strict');
const layout=require('./canvas-layout.js');
const boxes=(cards,positions,heights)=>cards.map(c=>({id:c.id,...positions[c.id],width:300,height:heights[c.id]}));
function noOverlap(rects){for(let i=0;i<rects.length;i++)for(let j=i+1;j<rects.length;j++)assert.equal(layout.overlap(rects[i],rects[j],0),false,`${rects[i].id} overlaps ${rects[j].id}`);}
test('resolves a hundred colliding cards with different rendered heights without mutating inputs',()=>{
 const cards=Array.from({length:100},(_,i)=>({id:String(i),x:24,y:24})),before=structuredClone(cards);
 const heights=Object.fromEntries(cards.map((c,i)=>[c.id,120+(i%7)*63]));
 const positions=layout.resolve(cards,heights);noOverlap(boxes(cards,positions,heights));
 assert.deepEqual(cards,before);
 for(const p of Object.values(positions)){assert.ok(p.x<=10000&&p.y<=10000);}
});
test('arranges input, agent and returned notes into separate columns with measured heights',()=>{
 const cards=[{id:'source',kind:'content'},{id:'brief',kind:'prompt'},{id:'agent',kind:'agent'},{id:'result',kind:'note'},{id:'check',kind:'checklist'}];
 const heights={source:170,brief:280,agent:260,result:410,check:230};
 const positions=layout.arrange(cards,[{from:'source',to:'agent'},{from:'brief',to:'agent'}],heights,{result:{actor_name:'Example'}});
 assert.equal(positions.source.x,positions.brief.x);assert.ok(positions.source.x<positions.agent.x&&positions.agent.x<positions.result.x);
 assert.ok(positions.check.y>positions.result.y+410);noOverlap(boxes(cards,positions,heights));
});
test('each incoming and outgoing link gets a distinct side port',()=>{
 const rects=[{id:'a',x:24,y:24,width:300,height:200},{id:'b',x:24,y:300,width:300,height:200},{id:'c',x:360,y:24,width:300,height:300},{id:'d',x:696,y:24,width:300,height:200}];
 const edges=[{from:'a',to:'c'},{from:'b',to:'c'},{from:'a',to:'d'}];
 const ports=layout.ports(rects,edges);
 assert.notEqual(ports[0].to.y,ports[1].to.y);assert.notEqual(ports[0].from.y,ports[2].from.y);
 assert.equal(ports[0].from.x,324);assert.equal(ports[0].to.x,360);
});
function avoidsCards(points,rects){
 for(let i=1;i<points.length;i++){
  const a=points[i-1],b=points[i];assert.ok(a.x===b.x||a.y===b.y,'nonorthogonal segment');
  for(const r of rects){
   const crosses=a.y===b.y ? a.y>r.y&&a.y<r.y+r.height&&Math.max(a.x,b.x)>r.x&&Math.min(a.x,b.x)<r.x+r.width : a.x>r.x&&a.x<r.x+r.width&&Math.max(a.y,b.y)>r.y&&Math.min(a.y,b.y)<r.y+r.height;
   assert.equal(crosses,false,'wire crosses '+r.id);
  }
 }
}
test('orthogonal routing avoids a card directly between connected nodes',()=>{
 const rects=[{id:'a',x:24,y:24,width:300,height:200},{id:'obstacle',x:360,y:24,width:300,height:200},{id:'b',x:696,y:24,width:300,height:200}];
 const pair=layout.ports(rects,[{from:'a',to:'b'}])[0];const path=layout.route(pair,rects);
 assert.ok(path);avoidsCards(path,rects);assert.ok(path.some(p=>p.y<24||p.y>224));
});
test('routing covers backward and vertical links without crossing cards',()=>{
 for(const rects of [
  [{id:'a',x:696,y:24,width:300,height:200},{id:'b',x:24,y:300,width:300,height:200}],
  [{id:'a',x:24,y:24,width:300,height:200},{id:'b',x:24,y:300,width:300,height:200}],
 ]){const pair=layout.ports(rects,[{from:'a',to:'b'}])[0],path=layout.route(pair,rects);assert.ok(path);avoidsCards(path,rects);}
});
