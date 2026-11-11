'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const datetime = require('./datetime.js');

test('format matches the server short UTC form', () => {
  assert.equal(datetime.format('2026-11-10T18:04:58.070Z'), '2026-11-10 18:04');
  assert.equal(datetime.format('2026-11-10T20:04:58+02:00'), '2026-11-10 18:04');
  assert.equal(datetime.format(Date.UTC(2026, 0, 2, 3, 4, 5)), '2026-01-02 03:04');
  assert.equal(datetime.full('2026-11-10T18:04:58Z'), '2026-11-10 18:04:58 UTC');
});

test('empty and unparseable values stay readable', () => {
  assert.equal(datetime.format(''), '—');
  assert.equal(datetime.format(undefined), '—');
  assert.equal(datetime.format('soon'), 'soon');
});

test('element builds a left-to-right time with the full value in title', () => {
  const made = [];
  const doc = {
    createElement(tag) { const node = { tag, attributes: {}, setAttribute(k, v) { this.attributes[k] = v; } }; made.push(node); return node; },
    createTextNode(text) { return { text }; },
  };
  const time = datetime.element('2026-11-10T18:04:58.070Z', doc);
  assert.equal(time.tag, 'time');
  assert.equal(time.dateTime, '2026-11-10T18:04:58Z');
  assert.equal(time.attributes.dir, 'ltr');
  assert.equal(time.title, '2026-11-10 18:04:58 UTC');
  assert.equal(time.textContent, '2026-11-10 18:04');
  assert.deepEqual(datetime.element('', doc), { text: '—' });
});
