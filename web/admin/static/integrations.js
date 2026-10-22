// Integrations hub: client-side search and filters for the discovered
// resource tables, binding helpers and bounded saved-snapshot polling.
// Provider requests are initiated only by explicit form posts.
(function () {
  'use strict';
  function norm(s) { return (s || '').toLowerCase(); }
  function initTables() { document.querySelectorAll('[data-resource-table]').forEach(function (section) {
    if (section.dataset.initialized) return;
    section.dataset.initialized = 'true';
    var search = section.querySelector('[data-resource-search]');
    var filter = section.querySelector('[data-resource-filter]');
    var count = section.querySelector('[data-resource-count]');
    var rows = Array.prototype.slice.call(section.querySelectorAll('tbody tr[data-search]'));
    var empty = section.querySelector('[data-resource-empty]');
    function apply() {
      var q = norm(search && search.value).trim();
      var f = filter ? filter.value : '';
      var shown = 0;
      rows.forEach(function (row) {
        var ok = !q || norm(row.getAttribute('data-search')).indexOf(q) !== -1;
        if (ok && f === 'bound') ok = row.getAttribute('data-bound') === 'bound';
        if (ok && f === 'candidate') ok = row.getAttribute('data-bound') !== 'bound';
        if (ok && f === 'identified') ok = row.getAttribute('data-identity') === 'passed';
        row.hidden = !ok;
        if (ok) shown++;
      });
      if (count) count.textContent = String(shown);
      if (empty) empty.hidden = shown !== 0;
    }
    if (search) search.addEventListener('input', apply);
    if (filter) filter.addEventListener('change', apply);
    apply();
  }); }
  initTables();

  var bind = document.getElementById('storage-bind');
  if (bind) {
    var r2 = bind.querySelector('[data-source-r2]');
    var s3 = bind.querySelector('[data-source-s3]');
    var s3Only = bind.querySelector('[data-s3-only]');
    var purposes = bind.querySelectorAll('[data-purpose]');
    var confirmBoth = bind.querySelector('[data-confirm-both]');
    function source() {
      var useS3 = !r2 || !r2.checked;
      if (s3Only) s3Only.hidden = !useS3;
    }
    function both() {
      var checked = Array.prototype.filter.call(purposes, function (p) { return p.checked; }).length;
      if (confirmBoth) confirmBoth.hidden = checked < 2;
    }
    [r2, s3].forEach(function (el) { if (el) el.addEventListener('change', source); });
    Array.prototype.forEach.call(purposes, function (p) { p.addEventListener('change', both); });
    source();
    both();
    document.addEventListener('click', function (event) {
      var btn = event.target.closest('[data-use-bucket]');
      if (!btn || btn.disabled) return;
      if (r2 && !r2.disabled) r2.checked = true;
      source();
      var bucket = bind.querySelector('[name=bucket]');
      if (bucket) bucket.value = btn.getAttribute('data-use-bucket');
      var name = bind.querySelector('[name=name]');
      if (name && !name.value) name.value = btn.getAttribute('data-use-bucket');
      bind.scrollIntoView({ behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'instant' : 'smooth', block: 'start' });
      var key = bind.querySelector('[name=access_key]');
      if (key) key.focus({ preventScroll: true });
    });
  }

  document.querySelectorAll('form[data-confirm]').forEach(function (form) {
    form.addEventListener('submit', function (e) {
      if (!window.confirm(form.getAttribute('data-confirm'))) e.preventDefault();
    });
  });

  var hub = document.querySelector('[data-integrations-live]');
  if (!hub || !window.fetch || !window.AbortController) return;
  var message = hub.querySelector('[data-live-message]');
  var timer, controller, failures = 0, requests = 0, stopped = false;
  var MAX_REQUESTS = 90, MAX_FAILURES = 5, POLL_MS = 2000;
  function pending() { return !!hub.querySelector('[data-refresh-pending="true"]'); }
  function available() { return !document.hidden && navigator.onLine !== false && hub.isConnected; }
  function say(key) {
    if (!message) return;
    message.hidden = !key;
    message.textContent = key ? message.dataset[key] : '';
  }
  function schedule(delay) {
    window.clearTimeout(timer);
    if (!stopped && available() && pending()) timer = window.setTimeout(poll, delay);
  }
  function patch(next) {
    // Reflect failures without replacing any unfinished configuration fields.
    hub.querySelectorAll('[data-section-fieldset]').forEach(function (fieldset) {
      var fresh = next.querySelector('[data-section-fieldset="' + fieldset.dataset.sectionFieldset + '"]');
      fieldset.disabled = !fresh || fresh.disabled;
    });
    hub.querySelectorAll('[data-live-region]').forEach(function (current) {
      var fresh = next.querySelector('[data-live-region="' + current.dataset.liveRegion + '"]');
      if (!fresh) { current.remove(); return; }
      // Keep filter choices, unfinished identity forms and keyboard position.
      var active = document.activeElement, activeKey = '';
      var sameRevision = current.dataset.configRevision === fresh.dataset.configRevision;
      function controlKey(control) {
        var form = control.closest('form');
        return (form ? form.getAttribute('action') : '') + ':' +
          (control.name || control.getAttribute('data-resource-filter') !== null && 'resource-filter' || '');
      }
      var oldControls = current.querySelectorAll('input, select, textarea');
      var newControls = fresh.querySelectorAll('input, select, textarea');
      var saved = new Map();
      oldControls.forEach(function (old) {
        var key = controlKey(old);
        if (old === active) activeKey = key;
        if (old.name !== '_csrf') saved.set(key, old);
      });
      newControls.forEach(function (replacement) {
        var old = saved.get(controlKey(replacement));
        if (!old || old.name === '_csrf' || old.type !== replacement.type) return;
        // Draft identity credentials belong to the exact account/token revision.
        if (!sameRevision && replacement.closest('form[method="post"]')) return;
        if (replacement.tagName === 'SELECT' && !Array.from(replacement.options).some(function (option) { return option.value === old.value; })) return;
        replacement.value = old.value;
        if (old.type === 'checkbox' || old.type === 'radio') replacement.checked = old.checked;
      });
      var oldDetails = new Map();
      current.querySelectorAll('details').forEach(function (detail) {
        var form = detail.querySelector('form');
        if (form) oldDetails.set(form.getAttribute('action'), detail.open);
      });
      fresh.querySelectorAll('details').forEach(function (detail) {
        var form = detail.querySelector('form');
        if (form && sameRevision) detail.open = oldDetails.get(form.getAttribute('action')) === true;
      });
      // Skip byte-identical results to avoid unnecessary layout and focus work.
      if (sameRevision && current.innerHTML === fresh.innerHTML && current.dataset.refreshPending === fresh.dataset.refreshPending) return;
      current.replaceWith(fresh);
      if (activeKey) newControls.forEach(function (control) { if (controlKey(control) === activeKey) control.focus({ preventScroll: true }); });
    });
    initTables();
  }
  async function poll() {
    if (!available() || !pending() || stopped || controller) return;
    if (requests >= MAX_REQUESTS) { stopped = true; say('failed'); return; }
    requests++;
    controller = new AbortController();
    var timeout = window.setTimeout(function () { if (controller) controller.abort(); }, 10000);
    try {
      var response = await fetch('/admin/settings/integrations' + window.location.search, {
        headers: { 'HX-Request': 'true', 'Accept': 'text/html' },
        credentials: 'same-origin', cache: 'no-store', signal: controller.signal
      });
      if (!response.ok || response.redirected) throw new Error('Snapshot unavailable');
      var next = new DOMParser().parseFromString(await response.text(), 'text/html').querySelector('[data-integrations-live]');
      if (!next) throw new Error('Invalid snapshot');
      if (available()) patch(next);
      failures = 0;
      say('');
    } catch (error) {
      if (available()) {
        failures++;
        if (failures >= MAX_FAILURES) { stopped = true; say('failed'); }
      }
    } finally {
      window.clearTimeout(timeout);
      controller = null;
      schedule(Math.min(15000, POLL_MS * Math.pow(2, failures)));
    }
  }
  function pauseOrResume() {
    window.clearTimeout(timer);
    if (!available()) {
      if (controller) controller.abort();
      if (navigator.onLine === false) say('paused');
      return;
    }
    if (!stopped) { say(''); schedule(0); }
  }
  document.addEventListener('visibilitychange', pauseOrResume);
  window.addEventListener('offline', pauseOrResume);
  window.addEventListener('online', pauseOrResume);
  window.addEventListener('pagehide', function () {
    stopped = true;
    window.clearTimeout(timer);
    if (controller) controller.abort();
  }, { once: true });
  schedule(POLL_MS);
})();
