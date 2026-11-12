// node --test web/admin/static/*_test.cjs
const test = require('node:test');
const assert = require('node:assert/strict');
const pk = require('./passkeys.js');

const bytes = (buf) => Array.from(new Uint8Array(buf));

test('base64url round trip without padding', () => {
  for (const len of [0, 1, 2, 3, 31, 32, 64]) {
    const src = new Uint8Array(len).map((_, i) => (i * 37 + 251) & 0xff);
    const enc = pk.toB64url(src.buffer);
    assert.doesNotMatch(enc, /[+/=]/);
    assert.deepEqual(bytes(pk.fromB64url(enc)), Array.from(src));
  }
  assert.deepEqual(bytes(pk.fromB64url('-_8')), [0xfb, 0xff]);
});

test('creation options decode challenge, user id and exclusions', () => {
  const opts = pk.creationOptions({
    publicKey: {
      challenge: 'AQID', rp: { id: 'example.com', name: 'Site' },
      user: { id: 'BAUG', name: 'a@example.com', displayName: 'A' },
      excludeCredentials: [{ type: 'public-key', id: 'BwgJ', transports: ['usb'] }],
    },
  });
  assert.deepEqual(bytes(opts.publicKey.challenge), [1, 2, 3]);
  assert.deepEqual(bytes(opts.publicKey.user.id), [4, 5, 6]);
  assert.equal(opts.publicKey.user.name, 'a@example.com');
  assert.deepEqual(bytes(opts.publicKey.excludeCredentials[0].id), [7, 8, 9]);
  assert.deepEqual(opts.publicKey.excludeCredentials[0].transports, ['usb']);
});

test('request options decode allowCredentials', () => {
  const opts = pk.requestOptions({ publicKey: { challenge: 'AQID', allowCredentials: [{ type: 'public-key', id: 'BwgJ' }] } });
  assert.deepEqual(bytes(opts.publicKey.allowCredentials[0].id), [7, 8, 9]);
  assert.equal(opts.publicKey.allowCredentials[0].transports, undefined);
});

test('assertion credential encodes to the JSON the server parses', () => {
  const buf = (...b) => new Uint8Array(b).buffer;
  const out = pk.credentialJSON({
    id: 'BwgJ', rawId: buf(7, 8, 9), type: 'public-key', authenticatorAttachment: 'platform',
    response: { clientDataJSON: buf(1), authenticatorData: buf(2), signature: buf(3), userHandle: buf(4) },
    getClientExtensionResults: () => ({}),
  });
  assert.deepEqual(out, {
    id: 'BwgJ', rawId: 'BwgJ', type: 'public-key', authenticatorAttachment: 'platform', clientExtensionResults: {},
    response: { clientDataJSON: 'AQ', authenticatorData: 'Ag', signature: 'Aw', userHandle: 'BA' },
  });
});

test('attestation credential includes transports', () => {
  const buf = (...b) => new Uint8Array(b).buffer;
  const out = pk.credentialJSON({
    id: 'AQ', rawId: buf(1), type: 'public-key',
    response: { clientDataJSON: buf(1), attestationObject: buf(2), getTransports: () => ['internal'] },
  });
  assert.deepEqual(out.response, { clientDataJSON: 'AQ', attestationObject: 'Ag', transports: ['internal'] });
  assert.deepEqual(out.clientExtensionResults, {});
});
