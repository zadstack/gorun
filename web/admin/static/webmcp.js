// WebMCP: registers GoRun admin tools with document.modelContext so an agent
// running in this browser can work through the signed-in user's session
// (https://webmachinelearning.github.io/webmcp/). Nothing happens in browsers
// without the API. Every tool calls an admin endpoint that applies the
// user's permissions on the server; the only write is a draft, so publishing,
// deleting and settings stay with the human in the regular screens.
(() => {
  'use strict';
  const mc = document.modelContext;
  const config = document.querySelector('[data-webmcp]');
  if (!mc || typeof mc.registerTool !== 'function' || !config) return;

  const MAX_TEXT = 4000;
  const lifetime = new AbortController();
  addEventListener('pagehide', () => lifetime.abort(), {once: true});

  async function getJSON(path, signal) {
    const response = await fetch(path, {credentials: 'same-origin', redirect: 'error', signal, headers: {Accept: 'application/json'}});
    if (!response.ok) throw new Error(`GoRun answered ${response.status} for ${path}`);
    return response.json();
  }

  // adminPath accepts only paths inside this site's admin.
  function adminPath(value) {
    const url = new URL(String(value || ''), location.origin);
    if (url.origin !== location.origin || !(url.pathname === '/admin' || url.pathname.startsWith('/admin/'))) {
      throw new Error('href must be an admin path such as /admin/content/post');
    }
    return url.pathname + url.search;
  }

  const tools = [
    {
      name: 'search_admin',
      title: 'Search the admin',
      description: 'Search content titles, users and admin pages the signed-in user may see. Returns links to open with open_admin_page.',
      inputSchema: {type: 'object', properties: {query: {type: 'string', minLength: 2, description: 'Words to search for, at least 2 characters.'}}, required: ['query']},
      annotations: {readOnlyHint: true, untrustedContentHint: true},
      execute: ({query}, {signal}) => getJSON('/admin/x/webmcp/search?' + new URLSearchParams({q: String(query || '')}), signal),
    },
    {
      name: 'list_admin_pages',
      title: 'List admin pages',
      description: 'List the admin pages and content types this user may use, and whether they may create drafts.',
      inputSchema: {type: 'object', properties: {}},
      annotations: {readOnlyHint: true},
      execute: (_, {signal}) => getJSON('/admin/x/webmcp/manifest', signal),
    },
    {
      name: 'get_current_page',
      title: 'Read the current page',
      description: 'Read the page the user has open: path, title, heading and its visible main text (trimmed). The text can contain content written by other people.',
      inputSchema: {type: 'object', properties: {}},
      annotations: {readOnlyHint: true, untrustedContentHint: true},
      execute: () => {
        const main = document.getElementById('main');
        const text = (main?.innerText || '').replace(/\n{3,}/g, '\n\n').trim();
        return {
          path: location.pathname + location.search,
          title: document.title,
          heading: main?.querySelector('h1')?.textContent.trim() || '',
          section: document.querySelector('[data-navigation-sections] [aria-current]')?.textContent.trim() || '',
          text: text.length > MAX_TEXT ? text.slice(0, MAX_TEXT) + '…' : text,
          truncated: text.length > MAX_TEXT,
        };
      },
    },
    {
      name: 'open_admin_page',
      title: 'Open an admin page',
      description: 'Navigate this tab to an admin page, for example a link returned by search_admin or list_admin_pages. Tools are registered again on the new page.',
      inputSchema: {type: 'object', properties: {href: {type: 'string', description: 'An admin path such as /admin/content/post.'}}, required: ['href']},
      execute: ({href}) => {
        const path = adminPath(href);
        // Answer first: navigating ends this document and its tools.
        setTimeout(() => location.assign(path), 0);
        return {opening: path};
      },
    },
    {
      name: 'create_content_draft',
      title: 'Create a content draft',
      description: 'Create a draft as the signed-in user. It is never published: the user reviews and publishes it in the editor. Use list_admin_pages for the content type slugs.',
      inputSchema: {type: 'object', properties: {
        type: {type: 'string', description: 'Content type slug, for example post or page.'},
        title: {type: 'string', minLength: 1, maxLength: 300},
        body: {type: 'string', description: 'HTML body; it is sanitized. At most 64 KB.'},
      }, required: ['type', 'title']},
      execute: async ({type, title, body}, {signal}) => {
        const response = await fetch('/admin/x/webmcp/drafts', {method: 'POST', credentials: 'same-origin', redirect: 'error', signal,
          headers: {'Content-Type': 'application/json', Accept: 'application/json', 'X-CSRF-Token': config.dataset.csrf},
          body: JSON.stringify({type: String(type || ''), title: String(title || ''), body: String(body || '')})});
        const data = await response.json().catch(() => ({}));
        if (!response.ok) throw new Error(data.error || `GoRun answered ${response.status}`);
        return data;
      },
    },
  ];

  for (const tool of tools) {
    mc.registerTool(tool, {signal: lifetime.signal}).catch(error => console.warn('WebMCP tool not registered:', tool.name, error));
  }
})();
