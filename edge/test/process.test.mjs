import {test} from 'node:test';
import assert from 'node:assert/strict';
import {command} from './process-helper.mjs';
test('isolated command deadline terminates descendants holding inherited output pipes',async()=>{
 const at=Date.now();
 await assert.rejects(command(['-e',`require('node:child_process').spawn(process.execPath,['-e','setInterval(()=>{},1000)'],{stdio:'inherit'});setInterval(()=>{},1000)`],{timeout:250}),/deadline exceeded/);
 assert.ok(Date.now()-at<3000);
 assert.deepEqual(await command(['-e','process.stdout.write("bounded child")']),{status:0,stdout:'bounded child'});
});
