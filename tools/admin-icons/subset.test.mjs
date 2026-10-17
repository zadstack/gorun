import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {createRequire} from 'node:module';
import {gzipSync} from 'node:zlib';
import {fileURLToPath} from 'node:url';
const require = createRequire(import.meta.url);
const root = fileURLToPath(new URL('../../', import.meta.url));
const full = require(root+'web/admin/static/vendor/lucide-1.55.0.js');
const subsetPath = root+'web/admin/static/vendor/lucide-core-1.55.0.js';
test('committed core icons match upstream geometry, export contract and byte budget', () => {
  const core = require(subsetPath);
  assert.ok(Object.keys(core.icons).length > 50);
  for (const [name, nodes] of Object.entries(core.icons)) {
    assert.deepEqual(nodes, full.icons[name], name);
    assert.deepEqual(core[name], nodes, name+' named export');
  }
  const bytes = readFileSync(subsetPath);
  assert.ok(bytes.length < 90000, 'core asset must remove at least80% of decoded vendor bytes');
  assert.ok(gzipSync(bytes).length < 25000, 'core asset compressed byte budget');
  assert.equal(core.createElement.toString(), full.createElement.toString());
});
test('admin head loads core with full fallback retained and pinned', () => {
  const head = readFileSync(root+'web/admin/templates/base.html','utf8');
  assert.match(head, /src="{{asset "vendor\/lucide-core-1.55.0.js"}}"/);
  assert.match(head, /data-lucide-full="{{asset "vendor\/lucide-1.55.0.js"}}"/);
  assert.match(head, /data-lucide-integrity="sha384-[^"]+"/);
});
test('derived asset provenance and head SRI identify exact bytes, not an upstream alias', () => {
  const provenance = JSON.parse(readFileSync(root+'web/admin/static/cdn-provenance.json'));
  const item = provenance.assets.find(a => a.package === 'lucide-core');
  const manifest = JSON.parse(readFileSync(root+'tools/admin-icons/manifest.json'));
  assert.equal(item.integrity,manifest.coreIntegrity);
  assert.equal(item.bytes,readFileSync(subsetPath).length);
  assert.equal(item.current_alias_matches,undefined);
  assert.equal(item.url,undefined);
  assert.equal(item.source_integrity,manifest.sourceIntegrity);
  assert.match(item.derivation,/unchanged renderer/);
  assert.ok(readFileSync(root+'web/admin/templates/base.html','utf8').includes('integrity="'+manifest.coreIntegrity+'"'));
});
