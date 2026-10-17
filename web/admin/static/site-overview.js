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
