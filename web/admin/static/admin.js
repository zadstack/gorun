// GoRun admin helpers (Alpine stores/components, HTMX hooks, icons).
document.addEventListener('alpine:init', () => {
  // Confirmation modal: gp.confirm(message, onYes) or data-confirm / hx-confirm.
  // The dialog keeps Tab between its buttons and returns focus to the
  // control that opened it on every close path (as agents-dock.js does).
  Alpine.store('confirm', {
    open: false,
    message: '',
    onYes: null,
    returnFocus: null,
    ask(message, onYes) {
      this.returnFocus = document.activeElement instanceof HTMLElement && document.activeElement !== document.body ? document.activeElement : null;
      this.message = message; this.onYes = onYes; this.open = true;
    },
    yes() { const fn = this.onYes; this.close(); if (fn) fn(); },
    close() {
      if (!this.open) return;
      this.open = false; this.onYes = null;
      const target = this.returnFocus; this.returnFocus = null;
      if (target?.isConnected) target.focus({ preventScroll: true });
    },
    trap(event, root) {
      const buttons = [...root.querySelectorAll('button')].filter((b) => !b.disabled);
      const first = buttons[0], last = buttons[buttons.length - 1];
      if (!first) return;
      if (!root.contains(document.activeElement)) { event.preventDefault(); first.focus(); }
      else if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
    },
  });
  // Toasts: gp.toast(message, kind) with kind success | error | info.
  Alpine.store('toasts', {
    items: [],
    push(message, kind = 'info') {
      const id = Date.now() + Math.random();
      this.items.push({ id, message, kind });
      setTimeout(() => this.remove(id), 4000);
    },
    remove(id) { this.items = this.items.filter((t) => t.id !== id); },
  });
  // Shell: the sidebar opens from the inline-start edge (right in RTL).
  Alpine.data('gpShell', () => ({
    sidebarOpen: false,
    get rtl() { return document.documentElement.dir === 'rtl'; },
    toggleSidebar() {
      if (this.sidebarOpen) { this.closeSidebar(); return; }
      this.sidebarOpen = true;
      this.$nextTick(() => this.$refs.sidebar.querySelector('a, button')?.focus({ preventScroll: true }));
    },
    closeSidebar() {
      this.sidebarOpen = false;
      this.$nextTick(() => document.querySelector('[data-sidebar-toggle]')?.focus());
    },
    sidebarKey(event) {
      if (!this.sidebarOpen) return;
      if (event.key === 'Escape') { event.preventDefault(); this.closeSidebar(); }
      if (event.key !== 'Tab') return;
      const controls = [...this.$refs.sidebar.querySelectorAll('a[href], button:not([disabled])')].filter(el => el.getClientRects().length);
      const first = controls[0], last = controls[controls.length - 1];
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); }
      if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
    },
  }));
  // AI writing menu of a rich text field (partials/fields/richtext.html).
  // The result is previewed; Replace / Insert below edit the Quill
  // instance, whose text-change handler keeps the textarea in sync. The
  // editor stays read-only while a request or a preview is pending so the
  // remembered range cannot drift.
  Alpine.data('gpAI', () => ({
    open: false, busy: false, result: '', error: '', range: null, target: null,
    quill() {
      const field = this.$root.closest('[data-field-type="richtext"]');
      const el = field && field.querySelector('[x-ref="editor"]');
      return el ? el.gpQuill : null;
    },
    toggle() {
      const q = this.quill();
      const r = q && q.getSelection();
      this.range = r ? { index: r.index, length: r.length } : null;
      this.open = !this.open;
    },
    async run(action) {
      const q = this.quill();
      this.open = false; this.error = ''; this.result = '';
      if (!q) return;
      const r = this.range && this.range.length > 0 ? this.range : null;
      const html = r ? q.getSemanticHTML(r.index, r.length) : q.getSemanticHTML();
      if (!(r ? q.getText(r.index, r.length) : q.getText()).trim()) { this.error = this.$root.dataset.empty; return; }
      this.target = r; this.busy = true; q.enable(false);
      try {
        this.result = await gp.aiWrite(action, html, this.$root.dataset.error);
      } catch (e) {
        this.error = e.message; q.enable(true);
      } finally { this.busy = false; }
    },
    apply(mode) {
      const q = this.quill(), r = this.target, html = this.result;
      this.cancel();
      if (!q || !html) return;
      if (mode === 'replace' && r) {
        q.deleteText(r.index, r.length, 'user');
        q.clipboard.dangerouslyPasteHTML(r.index, html, 'user');
      } else if (mode === 'replace') {
        q.clipboard.dangerouslyPasteHTML(html, 'user');
      } else {
        // On a new empty line after the line that ends the selection (or
        // after the last line), so no paragraph is split or merged.
        let at = q.getLength();
        if (r) {
          const [line] = q.getLine(Math.max(r.index + r.length - 1, 0));
          if (line) at = q.getIndex(line) + line.length();
        }
        if (at >= q.getLength()) {
          const end = q.getLength() - 1, [last] = q.getLine(end);
          q.insertText(end, '\n', last ? last.formats() : {}, 'user'); // the last line keeps its block format
          at = end + 1;
          q.removeFormat(at, 1, 'user');
        } else {
          q.insertText(at, '\n', 'user');
        }
        q.clipboard.dangerouslyPasteHTML(at, html, 'user');
      }
    },
    cancel() {
      this.result = ''; this.target = null;
      const q = this.quill();
      if (q) q.enable(true);
    },
  }));
  // "Suggest title" / "Write excerpt": data-action, data-target (input id);
  // the source is the body, the suggestion is previewed before it is used.
  Alpine.data('gpAIField', () => ({
    busy: false, result: '', error: '',
    async run() {
      const d = this.$root.dataset, body = document.getElementById('body');
      const src = body ? body.value.trim() : '';
      this.error = ''; this.result = '';
      if (!src) { this.error = d.empty; return; }
      this.busy = true;
      try { this.result = await gp.aiWrite(d.action, src, d.error); } catch (e) { this.error = e.message; }
      this.busy = false;
    },
    use() {
      const el = document.getElementById(this.$root.dataset.target);
      if (el) { el.value = this.result; el.dispatchEvent(new Event('input', { bubbles: true })); }
      this.result = '';
    },
  }));
});

window.gp = {
  confirm(message, onYes) { Alpine.store('confirm').ask(message, onYes); },
  toast(message, kind) { Alpine.store('toasts').push(message, kind); },
  // copy resolves to whether text reached the clipboard. The Clipboard API
  // needs HTTPS or localhost, so http://server-ip takes the fallback.
  async copy(text) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch (e) { /* fall through */ }
    const focused = document.activeElement;
    const area = document.createElement('textarea');
    area.value = text;
    area.readOnly = true;
    area.style.position = 'fixed';
    area.style.opacity = '0';
    document.body.append(area);
    area.select();
    try {
      return document.execCommand('copy');
    } catch (e) {
      return false;
    } finally {
      area.remove();
      focused?.focus();
    }
  },
  // aiWrite posts to the AI writing endpoint and resolves to its text; it
  // rejects with the server's message, or fallback when there is none.
  async aiWrite(action, text, fallback) {
    const meta = document.querySelector('meta[name="csrf-token"]');
    const title = document.getElementById('title');
    const body = new URLSearchParams({ action, text, title: title ? title.value : '' });
    let res, data;
    try {
      res = await fetch('/admin/x/ai/write', { method: 'POST', body, credentials: 'same-origin',
        headers: { 'X-CSRF-Token': meta ? meta.content : '', Accept: 'application/json' } });
      data = (res.headers.get('Content-Type') || '').includes('application/json') ? await res.json() : {};
    } catch (e) { throw new Error(fallback); }
    if (!res.ok || !data.text) throw new Error(data.error || fallback);
    return data.text;
  },
};

// <form data-confirm="..."> asks before submitting.
document.addEventListener('submit', (e) => {
  const form = e.target;
  if (!form.dataset || !form.dataset.confirm || form.dataset.confirmed) return;
  e.preventDefault();
  // Keep the clicked button so its name/value (e.g. a row menu's choice) is sent.
  const submitter = e.submitter && e.submitter.form === form ? e.submitter : null;
  gp.confirm(form.dataset.confirm, () => {
    form.dataset.confirmed = '1';
    form.requestSubmit(submitter);
    delete form.dataset.confirmed; // the next submit asks again
  });
}, true);

// HTMX: send the CSRF token on every request (backs up hx-headers on <body>).
document.addEventListener('htmx:configRequest', (e) => {
  const meta = document.querySelector('meta[name="csrf-token"]');
  if (meta) e.detail.headers['X-CSRF-Token'] = meta.content;
});
// HTMX: hx-confirm uses the modal instead of window.confirm.
document.addEventListener('htmx:confirm', (e) => {
  if (!e.detail.question) return;
  e.preventDefault();
  gp.confirm(e.detail.question, () => e.detail.issueRequest(true));
});

const renderIcons = () => { if (window.lucide) window.lucide.createIcons(); };
document.addEventListener('DOMContentLoaded', renderIcons);
document.addEventListener('htmx:afterSwap', renderIcons);
