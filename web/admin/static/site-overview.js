(() => {
  const root = document.querySelector('.site-overview');
  if (!root) return;

  const frames = [...root.querySelectorAll('iframe[data-src]')];
  const loaded = new WeakSet();
  for (const frame of frames) {
    frame.addEventListener('load', () => {
      if (frame.hasAttribute('src')) {
        loaded.add(frame);
        frame.closest('.ov-page').dataset.previewReady = 'true';
        if (frames.every(candidate => loaded.has(candidate))) root.classList.remove('ov-print-incomplete');
      }
    });
  }
  const search = root.querySelector('[data-overview-search]');
  const clearFocus = root.querySelector('[data-overview-clear-focus]');
  const searchStatus = root.querySelector('[data-overview-search-status]');
  let focusKey = '';
  const filterPreviews = () => {
    let matches = 0;
    const query = search?.value.trim().toLocaleLowerCase() || '';
    for (const frame of frames) {
      const card = frame.closest('.ov-page');
      const value = [card.querySelector('footer b')?.textContent, card.querySelector('footer code')?.textContent].join(' ').toLocaleLowerCase();
      card.hidden = !!((query && !value.includes(query)) || (focusKey && focusKey !== frame.dataset.previewKey));
      if (!card.hidden) matches++;
    }
    root.querySelectorAll('[data-overview-group]').forEach(group => {
      group.hidden = ![...group.querySelectorAll('.ov-page')].some(card => !card.hidden);
    });
    if (clearFocus) clearFocus.hidden = !focusKey;
    if (searchStatus) searchStatus.textContent = matches ? '' : searchStatus.dataset.empty;
  };
  search?.addEventListener('input', () => { focusKey = ''; filterPreviews(); });
  clearFocus?.addEventListener('click', () => { focusKey = ''; filterPreviews(); });
  root.querySelector(':scope > nav')?.addEventListener('click', event => {
    if (!event.target.closest('button')) return;
    focusKey = '';
    filterPreviews();
  });
  root.addEventListener('click', event => {
    const button = event.target.closest('[data-overview-focus]');
    if (!button) return;
    const frame = frames.find(frame => frame.dataset.previewKey === button.dataset.overviewFocus);
    if (!frame) return;
    focusKey = frame.dataset.previewKey;
    filterPreviews();
    activate(frame);
    const expand = frame.closest('.ov-page').querySelector('button[aria-expanded]');
    if (expand?.getAttribute('aria-expanded') !== 'true') expand?.click();
    frame.closest('.ov-page').scrollIntoView({block: 'start'});
  });
  let observer;
  const activate = frame => {
    if (!frame || frame.hasAttribute('src')) return;
    const url = frame.dataset.src;
    if (!url) return;
    frame.loading = 'eager';
    frame.src = url;
    observer?.unobserve(frame.closest('.ov-page'));
  };
  const activateAll = () => frames.forEach(activate);

  root.addEventListener('overview-load-all', activateAll);
  root.addEventListener('overview-load', event => {
    activate(frames.find(frame => frame.dataset.previewKey === event.detail?.key));
  });
  window.addEventListener('beforeprint', () => {
    activateAll();
    root.classList.toggle('ov-print-incomplete', frames.some(frame => !loaded.has(frame)));
  });
  root.addEventListener('overview-print', async () => {
    const button = root.querySelector('[data-overview-print]');
    if (button.disabled) return;
    const error = root.querySelector('[data-overview-print-error]');
    button.disabled = true;
    button.setAttribute('aria-busy', 'true');
    error.hidden = true;
    activateAll();
    const ready = await new Promise(resolve => {
      const pending = frames.filter(frame => !loaded.has(frame));
      if (!pending.length) return resolve(true);
      const finish = ok => {
        clearTimeout(timer);
        pending.forEach(frame => frame.removeEventListener('load', check));
        resolve(ok);
      };
      const check = () => { if (pending.every(frame => loaded.has(frame))) finish(true); };
      const timer = setTimeout(() => finish(false), 15000);
      pending.forEach(frame => frame.addEventListener('load', check));
    });
    button.disabled = false;
    button.removeAttribute('aria-busy');
    if (ready) window.print();
    else error.hidden = false;
  });

  if (typeof IntersectionObserver !== 'function') {
    activateAll();
    return;
  }
  // Native iframe lazy loading did not defer these sandboxed public previews.
  observer = new IntersectionObserver(entries => {
    for (const entry of entries) {
      if (entry.isIntersecting) activate(entry.target.querySelector('iframe[data-src]'));
    }
  }, { rootMargin: '160px 0px' });
  for (const frame of frames) observer.observe(frame.closest('.ov-page'));
})();
