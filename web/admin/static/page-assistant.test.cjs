const{test}=require('node:test'),assert=require('node:assert/strict'),{selectedPortion}=require('./page-assistant.js');
test('body selection requires nonempty exact unambiguous range',()=>{assert.equal(selectedPortion('before selected after',7,15),'selected');assert.equal(selectedPortion('same same',0,4),null);assert.equal(selectedPortion('text',1,1),null)});
