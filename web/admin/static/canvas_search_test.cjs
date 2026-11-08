'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const search = require('./canvas-search.js');

test('search shortcut accepts the physical K key with an Arabic keyboard and ignores composition or extra modifiers', () => {
  const event = {key: 'ن', code: 'KeyK', ctrlKey: true};
  assert.equal(search.isShortcut(event), true);
  assert.equal(search.isShortcut({...event, ctrlKey: false, metaKey: true}), true);
  for (const extra of [{altKey: true}, {shiftKey: true}, {isComposing: true}, {ctrlKey: false}]) {
    assert.equal(search.isShortcut({...event, ...extra}), false);
  }
  assert.equal(search.isShortcut({key: 'k', ctrlKey: true}), true);
  assert.equal(search.isShortcut({key: 'j', code: 'KeyJ', ctrlKey: true}), false);
});

test('search result navigation uses validated board and card identities rather than an untrusted URL', () => {
  const board = '01J00000000000000000000001';
  const card = '77777777-7777-4777-8777-777777777777';
  assert.equal(search.resultHref({board_id: board, href: 'javascript:alert(1)'}), '/admin/canvases/' + board);
  assert.equal(search.resultHref({board_id: board, card_id: card, href: 'https://other.example/'}), '/admin/canvases/' + board + '#card-' + card);
  for (const board_id of ['', '../admin', board + '?q=x', '<script>', '01I00000000000000000000001']) assert.equal(search.resultHref({board_id}), null);
  for (const card_id of ['not-a-card', card + '?x', '<img>']) assert.equal(search.resultHref({board_id: board, card_id}), null);
});

test('query readiness ignores punctuation, Arabic marks and composition without changing the stored query', () => {
  assert.equal(search.queryState('  إِطْلَاق ١٢  '), 'ready');
  assert.equal(search.queryState('کِتاب'), 'ready');
  for (const value of ['', '  ', '…', 'ـَُِ', 'أَ', 'a']) assert.equal(search.queryState(value), value.trim() ? 'short' : 'empty');
  assert.equal(search.queryState('a'.repeat(161)), 'long');
});
