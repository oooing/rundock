import {build} from 'esbuild';
import assert from 'node:assert/strict';
import test from 'node:test';
const built=await build({entryPoints:['src/utils/releaseContent.ts'],bundle:true,platform:'node',format:'esm',write:false});
const {releaseContentState:state}=await import(`data:text/javascript;base64,${Buffer.from(built.outputFiles[0].text).toString('base64')}`);
test('unchanged code disables a new version even with a nonempty notes template',()=>{
 assert.equal(state(true,[],[],['v2.0.4'],{'v2.0.4':0}),'none');
});
test('only selected actual changes count',()=>{
 assert.equal(state(true,[],['new.ts'],['v2.0.4'],{'v2.0.4':0}),'none');
 assert.equal(state(true,['new.ts'],['new.ts'],['v2.0.4'],{'v2.0.4':0}),'new');
 assert.equal(state(true,['stale.ts'],[],['v2.0.4'],{'v2.0.4':0}),'none');
});
test('already committed and first-release content stays publishable',()=>{
 assert.equal(state(true,[],[],['v2.0.4'],{'v2.0.4':1}),'new');
 assert.equal(state(true,[],[],[''],{'':1}),'new');
});
test('selected version groups and unavailable comparison are handled separately',()=>{
 const counts={'web/v1.0.0':1,'desktop/v1.0.0':0};
 assert.equal(state(true,[],[],['desktop/v1.0.0'],counts),'none');
 assert.equal(state(true,[],[],Object.keys(counts),counts),'new');
 assert.equal(state(true,[],[],['v1.0.0'],undefined),'unknown');
});
test('no-Tag build and push flow remains unchanged',()=>{
 assert.equal(state(false,[],[],[],undefined),'new');
});
