'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const shortcuts = require('./shortcuts.js');

const el = (tagName, extra = {}) => ({tagName, getAttribute: name => (extra.attrs || {})[name] ?? null, ...extra});
const body = el('BODY');

test('typing targets are text fields, selects, textareas, contenteditable and text roles', () => {
  for (const type of ['text', 'search', 'email', 'url', 'number', 'datetime-local', 'password', undefined]) {
    assert.equal(shortcuts.isTyping(el('INPUT', {type})), true, String(type));
  }
  for (const type of ['checkbox', 'radio', 'button', 'submit', 'range', 'file']) {
    assert.equal(shortcuts.isTyping(el('INPUT', {type})), false, type);
  }
  assert.equal(shortcuts.isTyping(el('TEXTAREA')), true);
  assert.equal(shortcuts.isTyping(el('SELECT')), true);
  assert.equal(shortcuts.isTyping(el('DIV', {isContentEditable: true})), true);
  assert.equal(shortcuts.isTyping(el('DIV', {attrs: {role: 'textbox'}})), true);
  assert.equal(shortcuts.isTyping(el('A')), false);
  assert.equal(shortcuts.isTyping(null), false);
});

test('plain-key shortcuts never fire in a field, with modifiers or during IME composition', () => {
  const input = el('INPUT', {type: 'text'});
  for (const key of ['/', '?', 'g']) {
    assert.ok(shortcuts.classify({key, code: ''}, body, false), key);
    assert.equal(shortcuts.classify({key}, input, false), null, key + ' in a field');
    assert.equal(shortcuts.classify({key}, el('TEXTAREA'), false), null);
    assert.equal(shortcuts.classify({key}, el('DIV', {isContentEditable: true}), false), null);
    assert.equal(shortcuts.classify({key, isComposing: true}, body, false), null, key + ' composing');
    assert.equal(shortcuts.classify({key, keyCode: 229}, body, false), null, key + ' IME process key');
    assert.equal(shortcuts.classify({key, ctrlKey: true}, body, false), null);
    assert.equal(shortcuts.classify({key, altKey: true}, body, false), null);
  }
  assert.equal(shortcuts.classify({key: 'x'}, body, false), null);
});

test('help, search and the g sequence follow the physical key on an Arabic layout', () => {
  assert.deepEqual(shortcuts.classify({key: '?', code: 'Slash', shiftKey: true}, body, false), {type: 'help'});
  assert.deepEqual(shortcuts.classify({key: '؟', code: 'Slash', shiftKey: true}, body, false), {type: 'help'});
  assert.deepEqual(shortcuts.classify({key: '/', code: 'Slash'}, body, false), {type: 'search'});
  assert.deepEqual(shortcuts.classify({key: 'ظ', code: 'Slash'}, body, false), {type: 'search'});
  assert.deepEqual(shortcuts.classify({key: 'ل', code: 'KeyG'}, body, false), {type: 'g'});
  assert.deepEqual(shortcuts.classify({key: 'ؤ', code: 'KeyC'}, body, true), {type: 'go', key: 'c'});
  assert.deepEqual(shortcuts.classify({key: 'h'}, body, true), {type: 'go', key: 'h'});
  assert.deepEqual(shortcuts.classify({key: '1', code: 'Digit1'}, body, true), {type: 'cancel'});
  assert.deepEqual(shortcuts.classify({key: 'G', code: 'KeyG', shiftKey: true}, body, false), null);
  assert.equal(shortcuts.classify({key: 'c', code: 'KeyC'}, el('INPUT', {type: 'search'}), true), null, 'g then typing');
});

test('Ctrl/Cmd+S saves even from a field, but not with extra modifiers or while composing', () => {
  const field = el('TEXTAREA');
  assert.deepEqual(shortcuts.classify({key: 's', code: 'KeyS', ctrlKey: true}, field, false), {type: 'save'});
  assert.deepEqual(shortcuts.classify({key: 'س', code: 'KeyS', metaKey: true}, body, false), {type: 'save'});
  assert.equal(shortcuts.classify({key: 's', code: 'KeyS', ctrlKey: true, shiftKey: true}, field, false), null);
  assert.equal(shortcuts.classify({key: 's', code: 'KeyS', ctrlKey: true, altKey: true}, field, false), null);
  assert.equal(shortcuts.classify({key: 's', code: 'KeyS', ctrlKey: true, isComposing: true}, field, false), null);
  assert.equal(shortcuts.classify({key: 's', code: 'KeyS'}, body, false), null);
});
