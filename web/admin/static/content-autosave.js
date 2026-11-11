// Content editor autosave (docs/EDITING_SAFETY.md).
//
// The editor form ([data-content-editor]) carries data-autosave-url when the
// item may be autosaved. Text fields (title, slug, excerpt, body, SEO and
// custom fields) are posted in the background 3 s after the last change, at
// most every 30 s while typing goes on, and when focus leaves a field. The
// server keeps them in a per-user autosave record and never changes the item;
// it refuses (409) when the item was saved by someone else since the editor
// opened. A restore banner ([data-autosave-banner]) offers an earlier
// autosave; a beforeunload guard warns about changes that are not saved yet.
(function (root) {
  'use strict';

  const DEBOUNCE_MS = 3000;
  const MAX_WAIT_MS = 30000;
  const MIN_GAP_MS = 3000;

  // autosaveKey mirrors content.autosaveKey on the server.
  const autosaveKey = key => key === 'title' || key === 'slug' || key === 'excerpt' || key === 'body' ||
    key.startsWith('fields[') || key.startsWith('seo[');
  // Fields that never count as user changes.
  const ignoredKey = key => key === '_csrf' || key === 'edit_token' || key === 'terms_token' || key === 'intent' || key === 'preview_ttl';

  // split turns form entries into the autosaved part and the rest, each a
  // stable string for comparison, plus the autosaved pairs to post.
  function split(entries) {
    const saved = [], rest = [];
    for (const [key, value] of entries) {
      if (typeof value !== 'string' || ignoredKey(key)) continue;
      (autosaveKey(key) ? saved : rest).push([key, value]);
    }
    return { pairs: saved, saved: JSON.stringify(saved), rest: JSON.stringify(rest) };
  }

  // restore writes autosaved values ({name: [values]}) into a form and
  // returns the names it could not place (fields no longer in the form).
  function restore(form, values) {
    const skipped = [];
    for (const [name, list] of Object.entries(values || {})) {
      const els = [...form.querySelectorAll('[name]')].filter(el => el.name === name);
      if (!els.length) { skipped.push(name); continue; }
      let i = 0;
      for (const el of els) {
        if (el.type === 'checkbox' || el.type === 'radio') {
          el.checked = list.includes(el.value);
        } else if (el.tagName === 'SELECT' && el.multiple) {
          for (const opt of el.options) opt.selected = list.includes(opt.value);
        } else if (i < list.length) {
          el.value = list[i++];
        }
        el.dispatchEvent(new Event('input', { bubbles: true }));
        el.dispatchEvent(new Event('change', { bubbles: true }));
      }
      // A rich text field shows its textarea's HTML in Quill.
      const quill = els[0].closest('[data-field-type=richtext]')?.querySelector('[x-ref=editor]')?.gpQuill;
      if (quill) quill.setContents(quill.clipboard.convert({ html: els[0].value }), 'silent');
    }
    return skipped;
  }

  function init(doc) {
    const form = doc.querySelector('[data-content-editor]');
    if (!form) return;
    const status = form.querySelector('[data-autosave-status]');
    const url = form.dataset.autosaveUrl || '';
    const read = () => split(new FormData(form).entries());
    let base = read();
    let persisted = base.saved; // the text fields as last loaded or autosaved
    let userChanged = false, restDirty = false;
    let submitting = false, inFlight = false, paused = false;
    let lastRequest = 0, firstChange = 0, timer = 0;

    const show = (state, extra) => {
      if (!status) return;
      status.dataset.state = state;
      status.replaceChildren();
      const text = (state === 'conflict' || state === 'forbidden') && extra ? extra : status.dataset[state] || '';
      if (text) status.append(text);
      if (state === 'saved' && extra && root.gpDateTime) status.append(' ', root.gpDateTime.element(extra, doc));
      if (state === 'failed') {
        const retry = doc.createElement('button');
        retry.type = 'button';
        retry.className = 'btn btn-secondary btn-sm';
        retry.dataset.autosaveRetry = '';
        retry.textContent = status.dataset.retry || 'Retry';
        retry.addEventListener('click', () => save(true));
        status.append(' ', retry);
      }
    };

    function schedule(delay) {
      if (!url || paused || submitting) return;
      const now = Date.now();
      if (!firstChange) firstChange = now;
      // Debounce, but never let continuous typing postpone a save past MAX_WAIT_MS.
      const due = Math.min(now + delay, firstChange + MAX_WAIT_MS);
      clearTimeout(timer);
      timer = setTimeout(() => save(false), Math.max(0, due - now));
    }

    async function save(force) {
      if (!url || submitting || paused || !userChanged) return;
      clearTimeout(timer);
      timer = 0;
      const now = Date.now();
      if (inFlight) { schedule(MIN_GAP_MS); return; }
      if (!force && now - lastRequest < MIN_GAP_MS) { schedule(MIN_GAP_MS - (now - lastRequest)); return; }
      const snap = read();
      if (snap.saved === persisted) { firstChange = 0; return; }
      inFlight = true;
      lastRequest = now;
      show('saving');
      const body = new URLSearchParams();
      body.set('_csrf', form.querySelector('[name=_csrf]')?.value || '');
      body.set('edit_token', form.querySelector('[name=edit_token]')?.value || '');
      for (const [key, value] of snap.pairs) body.append(key, value);
      try {
        const resp = await fetch(url, { method: 'POST', body, credentials: 'same-origin', headers: { Accept: 'application/json' } });
        const data = await resp.json().catch(() => ({}));
        if (resp.ok && data.ok) {
          persisted = snap.saved;
          firstChange = 0;
          show('saved', data.data && data.data.saved_at);
        } else if (resp.status === 409 || resp.status === 403) {
          // Never retried: the item moved on, or the user lost access. The text stays in the editor.
          paused = true;
          show(resp.status === 409 ? 'conflict' : 'forbidden', data.error && data.error.message);
        } else {
          show('failed');
        }
      } catch (_) {
        show('failed');
      } finally {
        inFlight = false;
        if (!paused && firstChange && read().saved !== persisted) schedule(DEBOUNCE_MS);
      }
    }

    const changed = event => {
      if (event && event.isTrusted === false && !event.gpUser) return;
      const name = event && event.target && event.target.name;
      if (name && !autosaveKey(name) && !ignoredKey(name)) restDirty = true;
      userChanged = true;
      if (read().saved === persisted) return;
      if (!paused && status && status.dataset.state !== 'saving') show('pending');
      schedule(DEBOUNCE_MS);
    };
    form.addEventListener('input', changed);
    form.addEventListener('change', changed);
    // Leaving a field saves soon (still at most one request every MIN_GAP_MS).
    form.addEventListener('focusout', () => { if (userChanged && read().saved !== persisted) schedule(0); });
    // Inputs swapped in later (taxonomy pickers) are part of the loaded state until the user edits.
    form.addEventListener('htmx:afterSettle', () => { if (!userChanged) { base = read(); persisted = base.saved; } });
    // The visual body editor writes its textarea without an input event.
    const hookQuill = tries => {
      const editors = [...form.querySelectorAll('[data-field-type=richtext] [x-ref=editor]')];
      if (editors.length && editors.every(el => el.gpQuill)) {
        editors.forEach(el => el.gpQuill.on('text-change', (_d, _o, source) => { if (source === 'user') changed({ gpUser: true }); }));
        return;
      }
      if (editors.length && tries > 0) setTimeout(() => hookQuill(tries - 1), 250);
    };
    hookQuill(40);
    // The periodic check catches anything the events missed.
    if (url) setInterval(() => { if (userChanged && read().saved !== persisted) save(false); }, MAX_WAIT_MS);

    form.addEventListener('submit', () => { submitting = true; clearTimeout(timer); });
    root.addEventListener('beforeunload', event => {
      if (submitting || !userChanged) return;
      if (read().saved === persisted && !restDirty) return;
      event.preventDefault();
      event.returnValue = '';
    });

    const banner = form.querySelector('[data-autosave-banner]');
    if (!banner) return;
    const restoreBtn = banner.querySelector('[data-autosave-restore]');
    const discardBtn = banner.querySelector('[data-autosave-discard]');
    if (restoreBtn) restoreBtn.hidden = false;
    restoreBtn?.addEventListener('click', () => {
      let values = {};
      try { values = JSON.parse(banner.dataset.autosavePayload || '{}'); } catch (_) { values = {}; }
      restore(form, values);
      // The restored text is what the server already holds as this user's autosave.
      userChanged = true;
      persisted = read().saved;
      banner.querySelector('[data-autosave-actions]')?.remove();
      const done = banner.querySelector('[data-autosave-restored]');
      if (done) done.hidden = false;
      banner.dataset.state = 'restored';
      const saveState = form.querySelector('[data-save-state]');
      if (saveState) saveState.textContent = saveState.dataset.dirty || '';
      form.querySelector('#title')?.focus();
    });
    discardBtn?.addEventListener('click', async event => {
      event.preventDefault();
      const body = new URLSearchParams({ _csrf: form.querySelector('[name=_csrf]')?.value || '' });
      try {
        const resp = await fetch(discardBtn.getAttribute('formaction'), { method: 'POST', body, credentials: 'same-origin', headers: { Accept: 'application/json' } });
        if (resp.ok) { banner.remove(); form.querySelector('#title')?.focus(); }
      } catch (_) { /* the banner stays; the plain form post remains possible */ }
    });
  }

  const api = { split, restore, autosaveKey, DEBOUNCE_MS, MAX_WAIT_MS };
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (root && root.document) {
    root.gpAutosave = api;
    if (root.document.readyState === 'loading') root.document.addEventListener('DOMContentLoaded', () => init(root.document));
    else init(root.document);
  }
})(typeof window === 'undefined' ? globalThis : window);
