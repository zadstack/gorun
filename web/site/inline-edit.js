/* Edit in place: the admin bar (internal/theme/admin_bar.go) links this
   script into a published builder page the signed-in staff member may edit.
   It adds one button to the bar; pressing it loads the builder draft
   (/admin/builder/<id>/draft.json), matches each section's texts to the
   elements that show them (by section id and text), and makes those
   elements editable where they stand. Every change saves to the same
   private draft the editor uses (POST .../save, debounced), with the same
   tokens, so the editor, revisions, conflicts and validation stay one
   system. Publish (when permitted) is the editor's publish; the page then
   reloads. A page whose draft already differs from what was published is
   not edited here: the bar says so and links the editor instead. */
(function () {
  'use strict';
  var bar = document.querySelector('[data-gorun-admin-bar][data-admin-bar-builder]');
  if (!bar || !window.fetch) return;
  var pageID = bar.getAttribute('data-admin-bar-builder');
  var csrf = '';
  var canPublish = bar.hasAttribute('data-admin-bar-publish');
  var L = {};
  try { L = JSON.parse(bar.getAttribute('data-admin-bar-labels') || '{}'); } catch (e) { L = {}; }
  var t = function (key) { return L[key] || key; };
  var editLink = bar.querySelector('[data-admin-bar-edit]');
  var base = '/admin/builder/' + encodeURIComponent(pageID);

  var toggle = el('button', 'gp-admin-bar-button', t('edit'));
  toggle.type = 'button';
  toggle.setAttribute('data-admin-bar-inline', '');
  toggle.setAttribute('aria-pressed', 'false');
  var status = el('span', 'gp-admin-bar-status', '');
  status.setAttribute('role', 'status');
  status.setAttribute('aria-live', 'polite');
  var publish = el('button', 'gp-admin-bar-button gp-admin-bar-publish', t('publish'));
  publish.type = 'button';
  publish.hidden = true;
  publish.setAttribute('data-admin-bar-inline-publish', '');
  (editLink || bar.firstElementChild).insertAdjacentElement('afterend', toggle);
  toggle.insertAdjacentElement('afterend', status);
  status.insertAdjacentElement('afterend', publish);

  var state = { active: false, loading: false, doc: null, token: null, bindings: [], timer: null, dirty: false, saving: false, changed: false };

  toggle.addEventListener('click', function () { if (state.active) deactivate(); else activate(); });
  publish.addEventListener('click', doPublish);
  document.addEventListener('keydown', function (ev) { if (state.active && ev.key === 'Escape') { ev.preventDefault(); deactivate(); } });
  // While editing, a link inside an editable section is text, not navigation.
  document.addEventListener('click', function (ev) {
    if (!state.active) return;
    var a = ev.target.closest && ev.target.closest('a');
    if (a && a.closest('section[id^="gp-"]') && !bar.contains(a)) ev.preventDefault();
  }, true);
  window.addEventListener('beforeunload', function (ev) { if (state.dirty || state.saving) { ev.preventDefault(); ev.returnValue = ''; } });

  function el(tag, cls, text) { var n = document.createElement(tag); n.className = cls; if (text) n.textContent = text; return n; }
  function say(text, kind) { status.textContent = text || ''; status.className = 'gp-admin-bar-status' + (kind ? ' is-' + kind : ''); while (status.nextSibling && status.nextSibling.hasAttribute && status.nextSibling.hasAttribute('data-admin-bar-inline-link')) status.nextSibling.remove(); }
  function sayLink(text, href, kind) { say(text, kind); var a = el('a', 'gp-admin-bar-link', href === '#reload' ? t('reload') : t('open_editor')); a.href = href === '#reload' ? location.href : href; a.setAttribute('data-admin-bar-inline-link', ''); if (href === '#reload') a.addEventListener('click', function (ev) { ev.preventDefault(); location.reload(); }); status.insertAdjacentElement('afterend', a); }
  function headers() { return { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf, 'X-Requested-With': 'fetch', 'Accept': 'application/json' }; }
  function norm(s) { return String(s == null ? '' : s).replace(/\s+/g, ' ').trim(); }

  function activate() {
    if (state.loading) return;
    state.loading = true;
    say(t('loading'));
    fetch(base + '/draft.json', { credentials: 'same-origin', headers: { 'Accept': 'application/json' } })
      .then(function (r) { if (!r.ok) throw new Error(String(r.status)); return r.json(); })
      .then(function (res) {
        state.loading = false;
        if (!res.in_sync) { sayLink(t('pending'), base, 'warn'); return; }
        csrf = res.csrf || '';
        state.doc = res.draft.document;
        state.token = res.draft.updated_at;
        canPublish = canPublish && !!res.can_publish;
        bind();
        if (!state.bindings.length) { sayLink(t('pending'), base, 'warn'); state.doc = null; return; }
        state.active = true;
        document.documentElement.classList.add('gp-inline-editing');
        toggle.textContent = t('done');
        toggle.setAttribute('aria-pressed', 'true');
        say(t('hint'));
      })
      .catch(function () { state.loading = false; say(t('error'), 'error'); });
  }

  function deactivate() {
    if (state.dirty) flush();
    state.active = false;
    state.bindings.forEach(function (b) {
      b.el.removeAttribute('contenteditable');
      b.el.classList.remove('gp-inline-field');
      b.el.removeEventListener('input', b.onInput);
      b.el.removeEventListener('keydown', b.onKey);
      b.el.removeEventListener('paste', b.onPaste);
    });
    state.bindings = [];
    document.documentElement.classList.remove('gp-inline-editing');
    toggle.textContent = t('edit');
    toggle.setAttribute('aria-pressed', 'false');
    if (!state.saving && !state.changed) say('');
  }

  // Which texts of a section can be edited here, as [label, get, set].
  function fields(s) {
    var out = [];
    var prop = function (obj, key) { return [function () { return obj[key]; }, function (v) { obj[key] = v; }]; };
    out.push(prop(s, 'title'), prop(s, 'text'), prop(s, 'link_label'));
    if (s.native) {
      out.push(prop(s.native, 'eyebrow'), prop(s.native, 'caption'), prop(s.native, 'secondary_label'));
      (s.native.facts || []).forEach(function (f) { out.push(prop(f, 'label'), prop(f, 'value')); });
    }
    (s.items || []).forEach(function (it) { out.push(prop(it, 'title'), prop(it, 'text'), prop(it, 'price')); });
    return out;
  }

  // A leaf element: text inside, no element children, not a control.
  function leaves(root) {
    var out = [];
    var walker = document.createTreeWalker(root, NodeFilter.SHOW_ELEMENT);
    var n;
    while ((n = walker.nextNode())) {
      if (n.children.length || !norm(n.textContent)) continue;
      if (/^(SCRIPT|STYLE|INPUT|TEXTAREA|SELECT|BUTTON|OPTION|SUMMARY|TIME)$/.test(n.tagName)) continue;
      if (n.closest('form, [data-gp-dynamic], .gp-task-search')) continue;
      out.push(n);
    }
    return out;
  }

  function bind() {
    state.bindings = [];
    (state.doc.sections || []).forEach(function (s) {
      if (s.hidden) return;
      var root = document.getElementById('gp-' + s.id);
      if (!root) return;
      var free = leaves(root);
      fields(s).forEach(function (f) {
        var want = norm(f[0]());
        if (!want) return;
        for (var i = 0; i < free.length; i++) {
          if (norm(free[i].textContent) === want) {
            attach(free.splice(i, 1)[0], f[1]);
            return;
          }
        }
      });
    });
  }

  function attach(node, set) {
    var b = { el: node, set: set };
    b.onInput = function () { set(node.textContent.replace(/ /g, ' ').replace(/\n+/g, ' ').trim()); markDirty(); };
    b.onKey = function (ev) { if (ev.key === 'Enter') { ev.preventDefault(); node.blur(); } };
    b.onPaste = function (ev) {
      if (!ev.clipboardData) return;
      ev.preventDefault();
      var text = ev.clipboardData.getData('text/plain');
      var sel = window.getSelection();
      if (!sel.rangeCount) return;
      sel.deleteFromDocument();
      sel.getRangeAt(0).insertNode(document.createTextNode(text));
      sel.collapseToEnd();
      b.onInput();
    };
    try { node.contentEditable = 'plaintext-only'; } catch (e) { /* fall through */ }
    if (node.contentEditable !== 'plaintext-only') node.contentEditable = 'true';
    node.classList.add('gp-inline-field');
    node.addEventListener('input', b.onInput);
    node.addEventListener('keydown', b.onKey);
    node.addEventListener('paste', b.onPaste);
    state.bindings.push(b);
  }

  function markDirty() {
    state.dirty = true;
    say(t('saving'));
    clearTimeout(state.timer);
    state.timer = setTimeout(flush, 700);
  }

  function flush() {
    clearTimeout(state.timer);
    if (!state.dirty || state.saving || !state.doc) return;
    state.dirty = false;
    state.saving = true;
    var body = JSON.stringify({ document: state.doc, base_updated_at: state.token });
    fetch(base + '/save', { method: 'POST', credentials: 'same-origin', headers: headers(), body: body })
      .then(function (r) {
        state.saving = false;
        if (r.status === 409) { sayLink(t('conflict'), '#reload', 'error'); state.doc = null; deactivate(); return; }
        if (!r.ok) { say(t('error'), 'error'); state.dirty = true; return; }
        return r.json().then(function (draft) {
          state.token = draft.updated_at;
          state.changed = true;
          say(t('saved'), 'ok');
          publish.hidden = !canPublish;
          if (state.dirty) flush();
        });
      })
      .catch(function () { state.saving = false; state.dirty = true; say(t('error'), 'error'); });
  }

  function doPublish() {
    if (state.dirty || state.saving) { flush(); setTimeout(doPublish, 300); return; }
    publish.disabled = true;
    say(t('saving'));
    fetch(base + '/publish', { method: 'POST', credentials: 'same-origin', headers: headers(), body: JSON.stringify({ updated_at: state.token }) })
      .then(function (r) {
        if (r.status === 409) { sayLink(t('conflict'), '#reload', 'error'); return; }
        if (!r.ok) { say(t('error'), 'error'); publish.disabled = false; return; }
        state.changed = false;
        state.active = false;
        say(t('published'), 'ok');
        location.reload();
      })
      .catch(function () { say(t('error'), 'error'); publish.disabled = false; });
  }
})();
