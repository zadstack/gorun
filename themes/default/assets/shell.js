/* Cosmetic enhancement only: native details and real links work without JS. */
(function () {
  function init() {
    var header = document.querySelector('[data-shell-header]');
    if (!header) return;
    var current = location.pathname.replace(/\/$/, '') || '/';
    header.querySelectorAll('nav a[href]').forEach(function (link) {
      var destination = new URL(link.getAttribute('href'), location.href);
      if (destination.origin === location.origin && (destination.pathname.replace(/\/$/, '') || '/') === current && !destination.hash) link.setAttribute('aria-current', 'page');
    });
    header.addEventListener('keydown', function (event) {
      if (event.key !== 'Escape') return;
      var expanded = event.target.closest('details[open]');
      if (!expanded) return;
      expanded.open = false;
      var toggle = expanded.querySelector('summary');
      if (toggle) toggle.focus();
      event.stopPropagation();
    });
  }
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init, {once:true}); else init();
}());
