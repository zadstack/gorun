// Keep the navigation position across full page loads, independently of the page.
(() => {
  const nav = document.querySelector('[data-admin-navigation]');
  if (!nav) return;
  const key = 'gorun.admin.navigation.scroll';
  let position = 0;
  try {
    const value = Number(sessionStorage.getItem(key));
    if (Number.isFinite(value) && value >= 0) position = value;
  } catch { /* Storage may be disabled; navigation must still work. */ }

  const restore = () => {
    if (!nav.clientHeight) return false;
    nav.scrollTop = position;
    return true;
  };
  // On mobile the navigation stays hidden until its drawer is opened.
  if (!restore()) {
    const observer = new ResizeObserver(() => {
      if (restore()) observer.disconnect();
    });
    observer.observe(nav);
  }
  const save = () => {
    if (!nav.clientHeight) return;
    position = nav.scrollTop;
    try { sessionStorage.setItem(key, String(position)); } catch { /* Optional. */ }
  };
  nav.addEventListener('scroll', save, { passive: true });
  nav.addEventListener('click', save, { capture: true });
  window.addEventListener('pagehide', save);
})();
