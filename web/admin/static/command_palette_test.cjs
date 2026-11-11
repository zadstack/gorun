'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const palette = require('./command-palette.js');

test('normalize folds Arabic marks, alef, ya, ta marbuta and tatweel, and Latin case and accents', () => {
  assert.equal(palette.normalize('الإعدادات'), palette.normalize('الاعدادات'));
  assert.equal(palette.normalize('أحمد'), 'احمد');
  assert.equal(palette.normalize('آمن'), 'امن');
  assert.equal(palette.normalize('ٱلبيت'), 'البيت');
  assert.equal(palette.normalize('مُحَمَّد'), 'محمد');
  assert.equal(palette.normalize('مدرسة'), 'مدرسه');
  assert.equal(palette.normalize('مستشفى'), 'مستشفي');
  assert.equal(palette.normalize('كتـــاب'), 'كتاب');
  assert.equal(palette.normalize('مسؤول'), 'مسوول');
  assert.equal(palette.normalize('Café  Settings!'), 'cafe settings');
  assert.equal(palette.normalize(null), '');
});

test('score prefers exact, prefix and word matches over substrings and fuzzy letters', () => {
  const s = palette.score;
  assert.equal(s('settings', 'Settings'), 100);
  assert.ok(s('sett', 'Settings') > s('ings', 'Settings'));
  assert.ok(s('keys', 'API keys') > s('pi', 'API keys'));
  assert.ok(s('nwpst', 'New post') > 0, 'letters in order match');
  assert.equal(s('xyz', 'New post'), 0);
  assert.equal(s('', 'New post'), 0);
  assert.equal(s('a', 'Media'), 45, 'one letter only matches as a substring');
  assert.equal(s('q', 'Media'), 0);
  assert.ok(s('اعدادات', 'الإعدادات') > 0, 'Arabic without hamza finds the hamza spelling');
  assert.ok(s('مستخدم جديد', 'مستخدم جديد') === 100);
  assert.ok(s('جديد مستخدم', 'مستخدم جديد') > 0, 'every word anywhere');
});

test('rank orders matches, keeps ties stable and searches labels in the other language', () => {
  const items = [
    {label: 'Media', href: '/admin/media'},
    {label: 'Settings', alt: ['الإعدادات'], href: '/admin/settings/general'},
    {label: 'Site settings', href: '/admin/settings/site'},
    {label: 'Users', alt: ['المستخدمون'], href: '/admin/users'},
  ];
  assert.deepEqual(palette.rank('settings', items).map(i => i.href), ['/admin/settings/general', '/admin/settings/site']);
  assert.deepEqual(palette.rank('اعدادات', items).map(i => i.href), ['/admin/settings/general']);
  assert.deepEqual(palette.rank('مستخدمون', items).map(i => i.href), ['/admin/users']);
  assert.equal(palette.rank('s', items, 2).length, 2);
});

test('the palette shortcut is Ctrl/Cmd+K by key or physical position, never with extra modifiers or composition', () => {
  const ev = {key: 'k', code: 'KeyK', ctrlKey: true};
  assert.equal(palette.isShortcut(ev), true);
  assert.equal(palette.isShortcut({key: 'ن', code: 'KeyK', metaKey: true}), true);
  for (const extra of [{altKey: true}, {shiftKey: true}, {isComposing: true}, {ctrlKey: false}]) {
    assert.equal(palette.isShortcut({...ev, ...extra}), false);
  }
});

test('only admin links on this origin are followed or remembered', () => {
  const origin = 'https://cms.example';
  assert.equal(palette.adminHref('/admin/content/post?x=1#top', origin), '/admin/content/post?x=1#top');
  assert.equal(palette.adminHref('/admin', origin), '/admin');
  for (const bad of ['https://evil.example/admin', 'javascript:alert(1)', '/administrator', '/', '//evil.example/admin', '']) {
    assert.equal(palette.adminHref(bad, origin), null, bad);
  }
});

test('recent items survive storage errors and ignore tampered entries', () => {
  const origin = 'https://cms.example';
  const store = new Map();
  const storage = {getItem: k => store.get(k) ?? null, setItem: (k, v) => store.set(k, v)};
  for (let i = 0; i < 7; i++) palette.remember(storage, origin, {label: 'Page ' + i, href: '/admin/p' + i});
  palette.remember(storage, origin, {label: 'Page 3 again', href: '/admin/p3'});
  palette.remember(storage, origin, {label: 'Outside', href: 'https://evil.example/admin'});
  assert.deepEqual(palette.readRecent(storage, origin).map(r => r.href), ['/admin/p3', '/admin/p6', '/admin/p5', '/admin/p4', '/admin/p2']);
  store.set('gorun.palette.recent', '[{"label":"x","href":"javascript:alert(1)"},{"label":"ok","href":"/admin/ok"},5]');
  assert.deepEqual(palette.readRecent(storage, origin).map(r => r.href), ['/admin/ok']);
  store.set('gorun.palette.recent', '{not json');
  assert.deepEqual(palette.readRecent(storage, origin), []);
  const broken = {getItem() { throw new Error('denied'); }, setItem() { throw new Error('denied'); }};
  assert.deepEqual(palette.readRecent(broken, origin), []);
  assert.doesNotThrow(() => palette.remember(broken, origin, {label: 'x', href: '/admin/x'}));
});
