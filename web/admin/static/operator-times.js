(() => {
  if (window.__operatorTimes) return;
  window.__operatorTimes = true;
  const update = () => {
    const locale = document.documentElement.lang || 'en';
    const relative = new Intl.RelativeTimeFormat(locale, { numeric: 'auto' });
    for (const element of document.querySelectorAll('[data-operator-time]')) {
      const at = Date.parse(element.getAttribute('datetime') || '');
      if (!Number.isFinite(at)) continue;
      const seconds = (at - Date.now()) / 1000;
      const [unit, size] = Math.abs(seconds) < 60 ? ['second', 1] : Math.abs(seconds) < 3600 ? ['minute', 60] : Math.abs(seconds) < 86400 ? ['hour', 3600] : ['day', 86400];
      element.textContent = relative.format(Math.round(seconds / size), unit);
    }
  };
  document.addEventListener('htmx:afterSettle', update);
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', update, { once: true });
  else update();
})();
