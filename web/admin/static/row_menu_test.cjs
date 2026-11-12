'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const menu = require('./row-menu.js');

test('nextIndex moves with arrows, wraps at both ends and jumps with Home/End', () => {
  assert.equal(menu.nextIndex(-1, 'ArrowDown', 3), 0);
  assert.equal(menu.nextIndex(-1, 'ArrowUp', 3), 2);
  assert.equal(menu.nextIndex(0, 'ArrowDown', 3), 1);
  assert.equal(menu.nextIndex(2, 'ArrowDown', 3), 0);
  assert.equal(menu.nextIndex(0, 'ArrowUp', 3), 2);
  assert.equal(menu.nextIndex(1, 'Home', 3), 0);
  assert.equal(menu.nextIndex(1, 'End', 3), 2);
  assert.equal(menu.nextIndex(1, 'Enter', 3), -1);
  assert.equal(menu.nextIndex(0, 'ArrowDown', 0), -1);
});

const viewport = {width: 390, height: 844};
const size = {width: 200, height: 120};

test('placement opens below and aligns to the inline end of the button', () => {
  const button = {top: 100, bottom: 130, left: 300, right: 340};
  assert.deepEqual(menu.placement(button, size, viewport, false), {top: 134, left: 140});
  // RTL: the inline end is the left edge.
  assert.deepEqual(menu.placement({top: 100, bottom: 130, left: 20, right: 60}, size, viewport, true), {top: 134, left: 20});
});

test('placement flips above the button when there is no room below', () => {
  const button = {top: 780, bottom: 810, left: 300, right: 340};
  assert.equal(menu.placement(button, size, viewport, false).top, 656);
});

test('placement keeps the menu inside the viewport on narrow screens', () => {
  assert.equal(menu.placement({top: 100, bottom: 130, left: 4, right: 40}, size, viewport, false).left, 8);
  assert.equal(menu.placement({top: 100, bottom: 130, left: 350, right: 386}, size, viewport, true).left, 182);
});
