// Analytics dashboard: turns each stacked table group into tabs and refreshes
// the "Visitors now" count every 30 s while the tab is visible. Without this
// script the page still works: the groups stay stacked sections.
(function () {
  'use strict';

  function tabs(root, n) {
    var panels = Array.prototype.slice.call(root.querySelectorAll('[data-tab-panel]'));
    if (panels.length < 2 || root.querySelector('[role="tablist"]')) return;
    var list = document.createElement('div');
    list.setAttribute('role', 'tablist');
    list.setAttribute('aria-label', root.getAttribute('data-tabs-label') || '');
    list.className = 'mb-2 flex flex-wrap gap-1';
    var buttons = panels.map(function (panel, i) {
      var b = document.createElement('button');
      b.type = 'button';
      b.id = 'an-tab-' + n + '-' + i;
      b.setAttribute('role', 'tab');
      b.setAttribute('aria-controls', panel.id);
      b.className = 'h-8 px-3 rounded border border-line text-sm aria-selected:border-line-strong aria-selected:bg-panel-2 aria-selected:font-medium [@media(pointer:coarse)]:h-11';
      b.textContent = panel.getAttribute('data-tab-label') || '';
      panel.setAttribute('role', 'tabpanel');
      panel.setAttribute('aria-labelledby', b.id);
      panel.tabIndex = 0;
      var heading = panel.querySelector('[data-tab-heading]');
      if (heading) heading.hidden = true;
      b.addEventListener('click', function () { select(i, false); });
      b.addEventListener('keydown', function (e) {
        var rtl = getComputedStyle(root).direction === 'rtl';
        var next = { ArrowRight: rtl ? -1 : 1, ArrowLeft: rtl ? 1 : -1 }[e.key];
        var to = null;
        if (next) to = (i + next + panels.length) % panels.length;
        else if (e.key === 'Home') to = 0;
        else if (e.key === 'End') to = panels.length - 1;
        if (to === null) return;
        e.preventDefault();
        select(to, true);
      });
      list.appendChild(b);
      return b;
    });
    function select(i, focus) {
      buttons.forEach(function (b, j) {
        var on = i === j;
        b.setAttribute('aria-selected', on ? 'true' : 'false');
        b.tabIndex = on ? 0 : -1;
        panels[j].hidden = !on;
      });
      if (focus) buttons[i].focus();
    }
    root.insertBefore(list, panels[0]);
    // Open the panel a "Show all" link points at (#t-<dim>), else the first.
    var start = panels.findIndex(function (p) { return '#' + p.id === location.hash; });
    select(start < 0 ? 0 : start, false);
  }

  var timer = window.__gorunAnalyticsNow;
  if (timer) clearInterval(timer);

  function now(el) {
    var url = el.getAttribute('data-now-url');
    var out = el.querySelector('[data-now-value]');
    if (!url || !out) return;
    var id = null;
    function tick() {
      if (!document.body.contains(el)) { stop(); return; }
      fetch(url, { credentials: 'same-origin', headers: { Accept: 'application/json' } })
        .then(function (r) { return r.ok ? r.json() : null; })
        .then(function (d) { if (d && typeof d.label === 'string') out.textContent = d.label; })
        .catch(function () {});
    }
    function start() { if (id === null) { id = setInterval(tick, 30000); window.__gorunAnalyticsNow = id; } }
    function stop() { if (id !== null) { clearInterval(id); id = null; } }
    document.addEventListener('visibilitychange', function () {
      if (document.hidden) { stop(); return; }
      if (document.body.contains(el)) { tick(); start(); }
    });
    if (!document.hidden) start();
  }

  function init() {
    Array.prototype.forEach.call(document.querySelectorAll('[data-tabs]'), tabs);
    var el = document.querySelector('[data-now]');
    if (el) now(el);
  }
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
  else init();
})();
