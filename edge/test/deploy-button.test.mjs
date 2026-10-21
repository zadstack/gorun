import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';

const edgeRoot = new URL('../', import.meta.url);
const repoRoot = new URL('../../', import.meta.url);
const deployURL = 'https://deploy.workers.cloudflare.com/?url=https://github.com/zadstack/gorun/tree/main/edge';
const pkg = JSON.parse(readFileSync(new URL('package.json', edgeRoot), 'utf8'));
const wrangler = readFileSync(new URL('wrangler.jsonc', edgeRoot), 'utf8');

test('Cloudflare deploy script provisions the preview through the DB binding', () => {
  assert.equal(pkg.scripts['db:migrations:apply'], 'wrangler d1 migrations apply DB --remote');
  assert.equal(pkg.scripts['seed:preview'], 'wrangler d1 execute DB --remote --file fixtures/demo.sql');
  assert.equal(pkg.scripts.deploy, 'npm run db:migrations:apply && npm run seed:preview && wrangler deploy');
  assert.equal(pkg.scripts.deploy.includes('--local'), false);
  assert.equal(pkg.cloudflare.bindings.SITE_ID.description.includes('demo'), true);
  assert.equal(pkg.cloudflare.bindings.DB.description.includes('GoRun Full'), true);
  assert.equal(pkg.cloudflare.bindings.MEDIA.description.includes('private R2'), true);
});

test('Wrangler config keeps the placeholder resources the deploy button replaces', () => {
  assert.match(wrangler, /"binding": "DB"/);
  assert.match(wrangler, /"database_name": "gorun-edge-poc-local"/);
  assert.match(wrangler, /"database_id": "00000000-0000-0000-0000-000000000000"/);
  assert.match(wrangler, /"migrations_dir": "migrations"/);
  assert.match(wrangler, /"binding": "MEDIA"/);
  assert.match(wrangler, /"bucket_name": "gorun-edge-poc-local"/);
  assert.match(wrangler, /"SITE_ID": "demo"/);
});

test('English and Arabic READMEs show the Edge deploy button', () => {
  const en = readFileSync(new URL('README.md', repoRoot), 'utf8');
  const ar = readFileSync(new URL('README.ar.md', repoRoot), 'utf8');
  for (const text of [en, ar, readFileSync(new URL('docs/EDGE.md', repoRoot), 'utf8')]) {
    assert.equal(text.includes(deployURL), true);
    assert.equal(text.includes('https://deploy.workers.cloudflare.com/button'), true);
  }
  assert.equal(en.includes('installs it on your Cloudflare account automatically'), true);
  assert.equal(ar.includes('تُثبَّت على حساب Cloudflare الخاص بك تلقائيًا'), true);
});
