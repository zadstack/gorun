// Preserve arbitrary content-type/extension icon names without sending every
// Lucide icon to ordinary core pages. The fallback URL comes only from our head.
(function (global) {
  const core = global.lucide;
  const original = core.createIcons;
  const script = document.currentScript;
  const fullURL = script?.dataset.lucideFull;
  const integrity = script?.dataset.lucideIntegrity;
  const nonce = script?.nonce;
  let loading = false;
  let waiting = [];

  const iconName = name => name.replace(/(?:^|[-_\s]+)(.)/g, (_, c) => c.toUpperCase());

  function missing(options) {
    const icons = options.icons || core.icons;
    const nameAttr = options.nameAttr || 'data-lucide';
    function inspect(root) {
      const names = [nameAttr, ...(nameAttr === 'data-lucide' ? ['icon-name'] : [])];
      for (const attr of names) {
        for (const el of root.querySelectorAll(`[${attr}]`)) {
          const name = iconName(el.getAttribute(attr));
          if (!icons[name]) return true;
        }
      }
      return !!options.inTemplates && [...root.querySelectorAll('template')].some(t => inspect(t.content));
    }
    return inspect(options.root || document);
  }

  function renderKnown(options, root = options.root || document) {
    const nameAttr = options.nameAttr || 'data-lucide';
    // Filter only the upstream renderer's selection; keep its SVG, attribute
    // and replacement behavior intact, leaving unknown nodes for the fallback.
    const knownRoot = {querySelectorAll(selector) {
      const attr = selector === '[icon-name]' ? 'icon-name' : nameAttr;
      return [...root.querySelectorAll(selector)].filter(el => core.icons[iconName(el.getAttribute(attr))]);
    }};
    original({...options, icons:core.icons, root:knownRoot, inTemplates:false});
    if (options.inTemplates) {
      for (const template of root.querySelectorAll('template')) renderKnown(options, template.content);
    }
  }

  core.createIcons = function createIcons(options = {}) {
    // Saved references must also delegate unknown-name errors once full is ready.
    if (core.createIcons !== createIcons) return core.createIcons(options);
    // An explicit icons collection is the caller's contract, including errors.
    if (options.icons !== undefined) return original(options);
    // A caller may retain this wrapper after the full collection replaces core.
    if (!missing(options) || !fullURL) return original({...options, icons:core.icons});
    renderKnown(options);
    waiting.push(options);
    if (loading) return;
    loading = true;
    const fallback = document.createElement('script');
    fallback.src = fullURL;
    fallback.integrity = integrity;
    fallback.crossOrigin = 'anonymous';
    if (nonce) fallback.nonce = nonce;
    fallback.onload = () => {
      Object.assign(core, global.lucide);
      global.lucide = core;
      const pending = waiting;
      waiting = [];
      for (const entry of pending) core.createIcons(entry);
      fallback.remove();
    };
    fallback.onerror = () => {
      // Keep placeholders and permit a later HTMX/Alpine render to retry.
      loading = false;
      waiting = [];
      fallback.remove();
      console.warn('[GoRun] Extra icons could not load; the next icon render can retry.');
    };
    document.head.appendChild(fallback);
  };
})(globalThis);
