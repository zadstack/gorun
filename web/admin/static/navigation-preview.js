(() => {
  const init = root => {
    const form = root.querySelector?.('[data-navigation-editor]');
    if (!form || form.dataset.previewReady) return;
    const groups = form.querySelector('[data-navigation-groups]');
    const preview = form.querySelector('[data-navigation-preview]');
    const search = form.querySelector('[data-navigation-search]');
    const projection = form.querySelector('[data-navigation-projection]');
    if (!groups || !preview || !search || !projection) return;
    form.dataset.previewReady = 'true';
    // Classification comes from the same authorized server projection as the shell.
    const index = new Map();
    for (const section of projection.content.querySelectorAll('[data-preview-section]')) {
      for (const tab of section.querySelectorAll('[data-preview-tab]')) {
        for (const item of tab.querySelectorAll('[data-preview-item]')) {
          index.set(item.dataset.previewItem, { key: section.dataset.previewSection, label: section.dataset.label, tab: tab.dataset.previewTab, tabLabel: tab.dataset.label });
        }
      }
    }
    const defaults = ['home', 'content', 'site', 'canvas', 'inbox', 'settings', 'commerce'];
    const update = () => {
      preview.replaceChildren();
      const query = search.value.trim().toLocaleLowerCase();
      const sections = new Map();
      Array.from(groups.children).forEach((group, rank) => {
        let matches = 0;
        for (const row of group.querySelectorAll('.navigation-row')) {
          const input = row.querySelector('input[name=visible]');
          const label = row.dataset.label || '';
          const category = input && index.get(input.value);
          const shown = !query || label.toLocaleLowerCase().includes(query) || (category?.label || '').toLocaleLowerCase().includes(query) || (category?.tabLabel || '').toLocaleLowerCase().includes(query);
          row.hidden = !shown; row.style.display = shown ? '' : 'none'; if (shown) matches++;
          if (!category || !input || (!input.checked && !input.disabled)) continue;
          if (!sections.has(category.key)) sections.set(category.key, { label: category.label, rank, tabs: new Map() });
          const section = sections.get(category.key); section.rank = Math.min(section.rank, rank);
          if (!section.tabs.has(category.tab)) section.tabs.set(category.tab, { label: category.tabLabel, items: [] });
          section.tabs.get(category.tab).items.push({ id: input.value, label });
        }
        group.hidden = !matches; group.style.display = matches ? '' : 'none';
      });
      const ordered = Array.from(sections).sort((a, b) => a[1].rank - b[1].rank || defaults.indexOf(a[0]) - defaults.indexOf(b[0]));
      for (const [key, category] of ordered) {
        const section = document.createElement('section'); section.dataset.previewSection = key;
        const heading = document.createElement('h3'); heading.textContent = category.label; section.append(heading);
        for (const [tabKey, tab] of category.tabs) {
          const group = document.createElement('div'); group.dataset.previewTab = tabKey;
          const title = document.createElement('h4'); title.textContent = tab.label;
          const list = document.createElement('ul');
          for (const value of tab.items) { const item = document.createElement('li'); item.textContent = value.label; item.dataset.previewId = value.id; list.append(item); }
          group.append(title, list); section.append(group);
        }
        preview.append(section);
      }
    };
    form.addEventListener('change', update);
    form.addEventListener('click', () => queueMicrotask(update));
    search.addEventListener('input', update);
    new MutationObserver(update).observe(groups, { childList: true, subtree: true });
    update();
  };
  if (!window.__navigationPreview) {
    window.__navigationPreview = true;
    document.addEventListener('htmx:afterSettle', event => init(event.target));
  }
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', () => init(document), { once: true });
  else init(document);
})();
