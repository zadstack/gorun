(() => {
  const init = root => {
    const form = root.querySelector?.('[data-navigation-editor]');
    if (!form || form.dataset.orderReady) return;
    form.dataset.orderReady = 'true';
    const groups = form.querySelector('[data-navigation-groups]');
    const refresh = () => {
      for (const container of [groups, ...groups.querySelectorAll('[data-navigation-items]')]) {
        const rows = [...container.children].filter(x => x.matches('[data-navigation-row]'));
        rows.forEach((row, index) => {
          row.querySelector('[data-move="up"]').disabled = index === 0;
          row.querySelector('[data-move="down"]').disabled = index === rows.length - 1;
        });
      }
    };
    form.addEventListener('click', event => {
      const button = event.target.closest('[data-move]');
      if (!button) return;
      const row = button.closest('[data-navigation-row]');
      const sibling = button.dataset.move === 'up' ? row.previousElementSibling : row.nextElementSibling;
      if (!sibling || !sibling.matches('[data-navigation-row]')) return;
      if (button.dataset.move === 'up') row.parentNode.insertBefore(row, sibling);
      else row.parentNode.insertBefore(sibling, row);
      refresh();
      const label = row.dataset.label;
      const index = [...row.parentNode.children].filter(x => x.matches('[data-navigation-row]')).indexOf(row) + 1;
      form.querySelector('[data-order-status]').textContent = `${label}: ${form.dataset.position} ${index}. ${form.dataset.unsaved}`;
      // A disabled boundary button cannot retain keyboard focus after the move.
      (button.disabled ? row.querySelector('[data-move]:not(:disabled)') : button)?.focus();
    });
    refresh();
  };
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', () => init(document));
  else init(document);
  document.addEventListener('htmx:afterSwap', e => init(e.target));
})();
