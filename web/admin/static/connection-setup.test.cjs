const {test}=require('node:test'),assert=require('node:assert/strict'),{fill}=require('./connection-setup.js');
test('official endpoint fill is explicit, AI-only and never overwrites existing URLs',()=>{
 assert.equal(fill('ai','openai',''),'https://api.openai.com/v1');assert.equal(fill('ai','anthropic',''),'https://api.anthropic.com/v1');
 for(const kind of ['mcp','s3'])assert.equal(fill(kind,'openai',''),'');
 assert.equal(fill('ai','unknown',''),'');
 for(const provider of ['openai','anthropic'])assert.equal(fill('ai',provider,'https://custom.example/v1'),'https://custom.example/v1');
});
