'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const autosave = require('./content-autosave.js');

test('only editor text, SEO and custom fields are autosaved', () => {
  for (const key of ['title', 'slug', 'excerpt', 'body', 'fields[price]', 'fields[specs][0][name]', 'seo[title]']) {
    assert.equal(autosave.autosaveKey(key), true, key);
  }
  for (const key of ['status', 'publish_at', 'author_id', 'terms[category]', 'edit_token', '_csrf', 'locale']) {
    assert.equal(autosave.autosaveKey(key), false, key);
  }
});

test('split separates autosaved pairs from decisions and ignores tokens', () => {
  const snap = autosave.split([
    ['_csrf', 'x'], ['edit_token', 't'], ['title', 'Hello'], ['status', 'draft'], ['body', '<p>a</p>'],
    ['terms[tag]', 'one'], ['fields[cover]', { name: 'file' }], ['seo[title]', 'S'],
  ]);
  assert.deepEqual(snap.pairs, [['title', 'Hello'], ['body', '<p>a</p>'], ['seo[title]', 'S']]);
  assert.equal(snap.rest, JSON.stringify([['status', 'draft'], ['terms[tag]', 'one']]));
  // A token change alone is not a user change.
  const again = autosave.split([['_csrf', 'y'], ['edit_token', 'u'], ['title', 'Hello'], ['status', 'draft'], ['body', '<p>a</p>'], ['terms[tag]', 'one'], ['seo[title]', 'S']]);
  assert.equal(again.saved, snap.saved);
  assert.equal(again.rest, snap.rest);
});

test('restore fills existing fields in order and reports missing ones', () => {
  const events = [];
  const el = (name, extra = {}) => Object.assign({
    name, value: '', type: 'text', tagName: 'INPUT', closest: () => null,
    dispatchEvent: e => events.push(name + ':' + e.type),
  }, extra);
  const fields = [el('title'), el('fields[list][]'), el('fields[list][]'), el('fields[on]', { type: 'checkbox', value: '1', checked: false })];
  const form = { querySelectorAll: () => fields };
  global.Event = class { constructor(type) { this.type = type; } };
  const skipped = autosave.restore(form, { title: ['Restored'], 'fields[list][]': ['a', 'b'], 'fields[on]': ['1'], 'fields[gone]': ['x'] });
  assert.equal(fields[0].value, 'Restored');
  assert.deepEqual([fields[1].value, fields[2].value], ['a', 'b']);
  assert.equal(fields[3].checked, true);
  assert.deepEqual(skipped, ['fields[gone]']);
  assert.ok(events.includes('title:input') && events.includes('title:change'));
});

test('timing respects the debounce and the maximum wait', () => {
  assert.ok(autosave.DEBOUNCE_MS >= 3000);
  assert.ok(autosave.MAX_WAIT_MS >= 30000);
});
