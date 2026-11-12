// Passkeys (WebAuthn): registration on My account → Two-factor
// authentication and the passkey button of the second sign-in step.
// No framework. The server sends the options as JSON (binary fields in
// base64url) and verifies everything; this file only converts between JSON
// and the ArrayBuffers navigator.credentials expects. docs/specs/passkeys.md
(function () {
  'use strict';

  // base64url ⇄ ArrayBuffer (RFC 4648 §5, no padding).
  function fromB64url(s) {
    var b64 = String(s).replace(/-/g, '+').replace(/_/g, '/');
    while (b64.length % 4) b64 += '=';
    var bin = atob(b64);
    var out = new Uint8Array(bin.length);
    for (var i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
    return out.buffer;
  }
  function toB64url(buf) {
    if (!buf) return null;
    var bytes = new Uint8Array(buf);
    var bin = '';
    for (var i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
    return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  }

  function descriptors(list) {
    return (list || []).map(function (d) {
      var out = { type: d.type, id: fromB64url(d.id) };
      if (d.transports) out.transports = d.transports;
      return out;
    });
  }

  function creationOptions(json) {
    var pk = Object.assign({}, json.publicKey);
    pk.challenge = fromB64url(pk.challenge);
    pk.user = Object.assign({}, pk.user, { id: fromB64url(pk.user.id) });
    if (pk.excludeCredentials) pk.excludeCredentials = descriptors(pk.excludeCredentials);
    return { publicKey: pk };
  }

  function requestOptions(json) {
    var pk = Object.assign({}, json.publicKey);
    pk.challenge = fromB64url(pk.challenge);
    if (pk.allowCredentials) pk.allowCredentials = descriptors(pk.allowCredentials);
    return { publicKey: pk };
  }

  function credentialJSON(cred) {
    var r = cred.response;
    var response = { clientDataJSON: toB64url(r.clientDataJSON) };
    if (r.attestationObject) {
      response.attestationObject = toB64url(r.attestationObject);
      if (typeof r.getTransports === 'function') response.transports = r.getTransports();
    } else {
      response.authenticatorData = toB64url(r.authenticatorData);
      response.signature = toB64url(r.signature);
      if (r.userHandle) response.userHandle = toB64url(r.userHandle);
    }
    var out = {
      id: cred.id,
      rawId: toB64url(cred.rawId),
      type: cred.type,
      response: response,
      clientExtensionResults: typeof cred.getClientExtensionResults === 'function' ? cred.getClientExtensionResults() : {},
    };
    if (cred.authenticatorAttachment) out.authenticatorAttachment = cred.authenticatorAttachment;
    return out;
  }

  // post sends JSON with the CSRF header and returns the parsed envelope. A
  // non-JSON answer (for example the password confirmation page after a
  // RequireRecentAuth redirect) reloads the page.
  function post(url, csrf, body) {
    return fetch(url, {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf, Accept: 'application/json' },
      body: body === undefined ? '{}' : JSON.stringify(body),
    }).then(function (res) {
      var ct = res.headers.get('Content-Type') || '';
      if (ct.indexOf('application/json') !== 0) {
        window.location.reload();
        return new Promise(function () {});
      }
      return res.json().then(function (env) {
        if (!env.ok) {
          var err = new Error((env.error && env.error.message) || 'error');
          err.server = true;
          err.code = env.error && env.error.code;
          throw err;
        }
        return env.data;
      });
    });
  }

  function supported() {
    return !!(window.PublicKeyCredential && navigator.credentials && navigator.credentials.create && window.isSecureContext);
  }

  function show(el, text) {
    if (!el) return;
    el.textContent = text || '';
    el.hidden = !text;
  }

  // explain turns a failure into the message to show.
  function explain(err, msgs) {
    if (err && err.server) return err.message;
    if (err && (err.name === 'NotAllowedError' || err.name === 'AbortError')) return msgs.cancelled;
    if (err && err.name === 'InvalidStateError') return msgs.exists || msgs.failed;
    return msgs.failed;
  }

  function csrfOf(root) {
    var input = root.querySelector('input[name="_csrf"]');
    return input ? input.value : '';
  }

  function initRegister(form) {
    var section = form.closest('[data-passkeys]') || document;
    var msgsEl = document.querySelector('[data-passkey-messages]');
    var msgs = msgsEl ? msgsEl.dataset : {};
    var status = section.querySelector('[data-passkey-status]');
    if (!supported()) {
      form.hidden = true;
      show(section.querySelector('[data-passkey-unsupported]'), msgs.unsupported);
      return;
    }
    var button = form.querySelector('button[type="submit"]');
    form.addEventListener('submit', function (ev) {
      ev.preventDefault();
      var csrf = csrfOf(form);
      var name = (form.querySelector('[name="name"]') || {}).value || '';
      button.disabled = true;
      status.classList.remove('account-notice-error');
      show(status, msgs.working);
      post(form.dataset.begin, csrf)
        .then(function (options) { return navigator.credentials.create(creationOptions(options)); })
        .then(function (cred) { return post(form.dataset.finish, csrf, { name: name, credential: credentialJSON(cred) }); })
        .then(function (data) {
          if (data.codes && data.codes.length) {
            var card = document.querySelector('[data-passkey-codes]');
            var list = card.querySelector('[data-passkey-code-list]');
            list.textContent = '';
            data.codes.forEach(function (c) {
              var li = document.createElement('li');
              var code = document.createElement('code');
              code.textContent = c;
              li.appendChild(code);
              list.appendChild(li);
            });
            section.hidden = true;
            card.hidden = false;
            card.querySelector('h2').focus({ preventScroll: false });
            return;
          }
          window.location.assign(window.location.pathname + '#passkeys');
          window.location.reload();
        })
        .catch(function (err) {
          button.disabled = false;
          status.classList.add('account-notice-error');
          show(status, explain(err, msgs));
        });
    });
  }

  function initLogin(box) {
    var msgs = box.dataset;
    var button = box.querySelector('[data-passkey-login-button]');
    var error = box.querySelector('[data-passkey-error]');
    var status = box.querySelector('[data-passkey-status]');
    if (!supported()) {
      button.disabled = true;
      show(status, msgs.unsupported);
      return;
    }
    button.addEventListener('click', function () {
      var csrf = csrfOf(box);
      button.disabled = true;
      show(error, '');
      show(status, msgs.working);
      post(box.dataset.begin, csrf)
        .then(function (options) { return navigator.credentials.get(requestOptions(options)); })
        .then(function (cred) { return post(box.dataset.finish, csrf, credentialJSON(cred)); })
        .then(function (data) { window.location.assign(data.redirect || '/admin'); })
        .catch(function (err) {
          // The pending sign-in is gone (expired, used, too many attempts):
          // its page explains and links back to the password step.
          if (err && err.code === 'sign_in_expired') {
            window.location.assign(window.location.pathname);
            return;
          }
          button.disabled = false;
          show(status, '');
          show(error, explain(err, msgs));
        });
    });
  }

  function init() {
    document.querySelectorAll('form[data-passkey-register]').forEach(initRegister);
    document.querySelectorAll('[data-passkey-login]').forEach(initLogin);
  }
  if (typeof document !== 'undefined') {
    if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
    else init();
  }

  // Exposed for unit tests (node) only.
  if (typeof module !== 'undefined' && module.exports) {
    module.exports = { fromB64url: fromB64url, toB64url: toB64url, creationOptions: creationOptions, requestOptions: requestOptions, credentialJSON: credentialJSON };
  }
})();
