const test = require('node:test');
const assert = require('node:assert/strict');
const {moveBranch, sibling, parentIndex, removeItem, toTree} = require('../../web/admin/static/menu-editor.js');
const rows = () => ['A:0','A1:1','A11:2','A2:1','B:0','B1:1','C:0'].map(s => {const [label,depth]=s.split(':');return {_k:label,label,depth:+depth,kind:'custom',url:'/'+label};});
const shape = items => items.map(i => `${i.label}:${i.depth}`);
test('whole branch moves after another branch without adopting siblings', () => {
 const input=rows(); const next=moveBranch(input,0,4,'after');
 assert.deepEqual(shape(next),['B:0','B1:1','A:0','A1:1','A11:2','A2:1','C:0']);
 assert.deepEqual(shape(input),shape(rows()));
});
test('inside preserves subtree and changing parent makes it the last child', () => {
 assert.deepEqual(shape(moveBranch(rows(),1,4,'inside')),['A:0','A2:1','B:0','B1:1','A1:1','A11:2','C:0']);
});
test('self and descendant targets cannot create cycles', () => {
 for(const target of [0,1,2,3]) assert.deepEqual(moveBranch(rows(),0,target,'inside'),rows());
});
test('nesting rejects any subtree exceeding six levels', () => {
 const input=Array.from({length:6},(_,depth)=>({_k:depth,label:String(depth),depth})).concat({_k:'root',label:'root',depth:0});
 assert.deepEqual(moveBranch(input,6,5,'inside'),input);
 assert.deepEqual(moveBranch(rows(),0,999,'inside'),rows());
});
test('outdent after parent keeps remaining children attached to their parent', () => {
 const items=rows(); assert.equal(parentIndex(items,1),0);
 assert.deepEqual(shape(moveBranch(items,1,parentIndex(items,1),'after')),['A:0','A2:1','A1:0','A11:1','B:0','B1:1','C:0']);
});
test('sibling moves never accidentally reparent an item', () => {
 const items=rows();assert.equal(sibling(items,3,-1),1);assert.equal(sibling(items,1,1),3);
 assert.equal(sibling(items,1,-1),-1);assert.equal(sibling(items,3,1),-1);
 assert.deepEqual(shape(moveBranch(items,3,1,'before')),['A:0','A2:1','A1:1','A11:2','B:0','B1:1','C:0']);
});
test('root parent choice and removal preserve descendants', () => {
 assert.deepEqual(shape(moveBranch(rows(),1,-1,'root')),['A:0','A2:1','B:0','B1:1','C:0','A1:0','A11:1']);
 assert.deepEqual(shape(removeItem(rows(),1)),['A:0','A11:1','A2:1','B:0','B1:1','C:0']);
});
test('nested saved payload retains IDs, references and metadata without editor state', () => {
 const items=rows();items[1]={...items[1],id:'stable-id',ref_id:'ref',kind:'content',css_class:'highlight',open_in_new:true};
 const payload=toTree(items);
 assert.equal(payload[0].children[0].id,'stable-id');assert.equal(payload[0].children[0].ref_id,'ref');
 assert.equal(payload[0].children[0].open_in_new,true);assert.equal(payload[0].children[0].children[0].label,'A11');
 assert.equal('_k' in payload[0],false);assert.equal('depth' in payload[0],false);
});
