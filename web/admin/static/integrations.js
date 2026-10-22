// Integrations hub: client-side search and filters for the discovered
// resource tables, the bind form helpers and confirmations. No data is
// fetched here; the page is a saved snapshot and refreshes are form posts.
(function () {
  'use strict';
  function norm(s) { return (s || '').toLowerCase(); }
  document.querySelectorAll('[data-resource-table]').forEach(function (section) {
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
  });

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
    document.querySelectorAll('[data-use-bucket]').forEach(function (btn) {
      btn.addEventListener('click', function () {
        if (r2 && !r2.disabled) r2.checked = true;
        source();
        var bucket = bind.querySelector('[name=bucket]');
        if (bucket) bucket.value = btn.getAttribute('data-use-bucket');
        var name = bind.querySelector('[name=name]');
        if (name && !name.value) name.value = btn.getAttribute('data-use-bucket');
        bind.scrollIntoView({ behavior: 'smooth', block: 'start' });
        var key = bind.querySelector('[name=access_key]');
        if (key) key.focus();
      });
    });
  }

  document.querySelectorAll('form[data-confirm]').forEach(function (form) {
    form.addEventListener('submit', function (e) {
      if (!window.confirm(form.getAttribute('data-confirm'))) e.preventDefault();
    });
  });
})();
