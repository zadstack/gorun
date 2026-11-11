// One date/time output for admin scripts, matching the server's `datetime`
// template function: the short UTC form "2026-11-10 18:04" as text, the
// machine value in datetime, the full UTC value in title, always left to
// right. Exposed as window.gpDateTime; also loadable from Node tests.
(function (root) {
  'use strict';
  const pad = n => String(n).padStart(2, '0');
  const parse = value => {
    if (value instanceof Date) return Number.isFinite(value.getTime()) ? value : null;
    if (value === null || value === undefined || value === '') return null;
    const date = new Date(value);
    return Number.isFinite(date.getTime()) ? date : null;
  };
  const day = d => `${d.getUTCFullYear()}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())}`;
  const short = d => `${day(d)} ${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}`;
  const full = d => `${short(d)}:${pad(d.getUTCSeconds())} UTC`;
  const iso = d => d.toISOString().replace(/\.\d{3}Z$/, 'Z');

  // format returns the short text, "—" for empty values and the raw value
  // when it cannot be parsed.
  function format(value) {
    const date = parse(value);
    if (date) return short(date);
    return value === null || value === undefined || value === '' ? '—' : String(value);
  }

  // element returns a <time> element (or a text node for unusable values).
  function element(value, doc) {
    const d = doc || root.document;
    const date = parse(value);
    if (!date) return d.createTextNode(format(value));
    const time = d.createElement('time');
    time.dateTime = iso(date);
    time.setAttribute('dir', 'ltr');
    time.title = full(date);
    time.textContent = short(date);
    return time;
  }

  const api = { format, element, full: value => { const date = parse(value); return date ? full(date) : format(value); } };
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (root) root.gpDateTime = api;
})(typeof window === 'undefined' ? globalThis : window);
