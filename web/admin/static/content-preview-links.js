// Shareable preview links in the content editor (docs/EDITING_SAFETY.md).
// Without JavaScript the buttons post the editor form to their formaction
// (a page shows the new link once; revoke redirects back). With it, create
// and revoke run in the background and the list updates in place.
(function () {
  'use strict';
  const box = document.querySelector('[data-preview-links]');
  const form = box && box.closest('form');
  if (!box || !form) return;
  const list = box.querySelector('[data-preview-list]');
  const empty = box.querySelector('[data-preview-empty]');
  const created = box.querySelector('[data-preview-created]');
  const urlField = box.querySelector('[data-preview-url]');
  const error = box.querySelector('[data-preview-error]');
  const csrf = () => form.querySelector('[name=_csrf]')?.value || '';
  const post = (url, params) => fetch(url, {
    method: 'POST', credentials: 'same-origin', headers: { Accept: 'application/json' },
    body: new URLSearchParams({ _csrf: csrf(), ...params }),
  }).then(async resp => ({ resp, data: await resp.json().catch(() => ({})) }));
  const fail = message => { error.textContent = message || ''; error.hidden = !message; };
  const syncEmpty = () => { if (empty) empty.hidden = !!list.querySelector('li'); };

  function revokeButton(action) {
    const button = document.createElement('button');
    button.type = 'submit';
    button.className = 'btn btn-ghost btn-sm';
    button.setAttribute('formaction', action);
    button.setAttribute('formnovalidate', '');
    button.dataset.previewRevoke = '';
    button.textContent = list.dataset.revokeLabel || 'Revoke';
    return button;
  }

  box.addEventListener('click', event => {
    const create = event.target.closest('[data-preview-create]');
    const revoke = event.target.closest('[data-preview-revoke]');
    const copy = event.target.closest('[data-preview-copy]');
    if (create) {
      event.preventDefault();
      create.disabled = true;
      fail('');
      post(create.getAttribute('formaction'), { preview_ttl: box.querySelector('[name=preview_ttl]').value })
        .then(({ resp, data }) => {
          if (resp.status !== 201 || !data.ok) {
            const fields = data.error && data.error.fields;
            fail((fields && Object.values(fields)[0]) || (data.error && data.error.message) || resp.statusText);
            return;
          }
          const link = data.data;
          urlField.value = link.url;
          created.hidden = false;
          urlField.focus();
          urlField.select();
          const item = document.createElement('li');
          item.dataset.previewLink = link.id;
          const label = document.createElement('span');
          label.append((list.dataset.expiresLabel || '') + ' ', window.gpDateTime ? window.gpDateTime.element(link.expires_at) : link.expires_at);
          item.append(label, revokeButton(create.getAttribute('formaction') + '/' + encodeURIComponent(link.id) + '/revoke'));
          list.prepend(item);
          syncEmpty();
        })
        .catch(() => fail(error.dataset.network || 'Network error'))
        .finally(() => { create.disabled = false; });
    } else if (revoke) {
      event.preventDefault();
      const run = () => post(revoke.getAttribute('formaction'), {}).then(({ resp, data }) => {
        if (resp.ok && data.ok) {
          revoke.closest('li')?.remove();
          syncEmpty();
          box.querySelector('[data-preview-create]')?.focus();
        } else {
          fail((data.error && data.error.message) || resp.statusText);
        }
      }).catch(() => fail('Network error'));
      const message = list.dataset.confirm || '';
      if (window.gp && typeof window.gp.confirm === 'function') window.gp.confirm(message, run);
      else if (window.confirm(message)) run();
    } else if (copy) {
      event.preventDefault();
      const done = () => { const label = copy.querySelector('span'); if (label) label.textContent = copy.dataset.copied || label.textContent; };
      if (navigator.clipboard && window.isSecureContext) navigator.clipboard.writeText(urlField.value).then(done, () => { urlField.select(); });
      else { urlField.select(); try { document.execCommand('copy'); done(); } catch (_) { /* the field stays selected */ } }
    }
  });
})();
