(() => {
  const init = root => {
    const form = root.querySelector?.('[data-navigation-editor]');
    if (!form || form.dataset.orderReady) return;
    form.dataset.orderReady = 'true';
    const groups = form.querySelector('[data-navigation-groups]');
    let drag = null;

    const rowsOf = container => [...container.children].filter(x => x.matches('[data-navigation-row]'));

    const announce = row => {
      const label = row.dataset.label;
      const index = rowsOf(row.parentNode).indexOf(row) + 1;
      form.querySelector('[data-order-status]').textContent =
        `${label}: ${form.dataset.position} ${index}. ${form.dataset.unsaved}`;
    };

    const refresh = () => {
      for (const container of [groups, ...groups.querySelectorAll('[data-navigation-items]')]) {
        const rows = rowsOf(container);
        rows.forEach((row, index) => {
          row.querySelector('[data-move="up"]').disabled = index === 0;
          row.querySelector('[data-move="down"]').disabled = index === rows.length - 1;
        });
      }
    };

    const moveRow = (row, direction) => {
      const sibling = direction === 'up' ? row.previousElementSibling : row.nextElementSibling;
      if (!sibling || !sibling.matches('[data-navigation-row]')) return false;
      if (direction === 'up') row.parentNode.insertBefore(row, sibling);
      else row.parentNode.insertBefore(sibling, row);
      refresh();
      announce(row);
      return true;
    };

    const peerTarget = (event, moving) => {
      let el = event.target;
      while (el && el !== form) {
        if (el.matches?.('[data-navigation-row]') && el.parentNode === moving.parentNode && el !== moving) {
          return el;
        }
        el = el.parentElement;
      }
      return null;
    };

    const clearDrag = () => {
      form.querySelectorAll('.is-dragging,.is-reorder-target').forEach(el => {
        el.classList.remove('is-dragging', 'is-reorder-target');
      });
      drag = null;
    };

    form.addEventListener('click', event => {
      const button = event.target.closest('[data-move]');
      if (!button || !form.contains(button)) return;
      const row = button.closest('[data-navigation-row]');
      if (!moveRow(row, button.dataset.move)) return;
      // A disabled boundary button cannot retain keyboard focus after the move.
      (button.disabled ? row.querySelector('[data-move]:not(:disabled)') : button)?.focus();
    });

    form.addEventListener('keydown', event => {
      const handle = event.target.closest('[data-navigation-drag]');
      if (!handle || !form.contains(handle)) return;
      if (!(event.altKey && (event.key === 'ArrowUp' || event.key === 'ArrowDown'))) return;
      event.preventDefault();
      const row = handle.closest('[data-navigation-row]');
      if (moveRow(row, event.key === 'ArrowUp' ? 'up' : 'down')) handle.focus();
    });

    form.addEventListener('dragstart', event => {
      const handle = event.target.closest('[data-navigation-drag]');
      if (!handle || !form.contains(handle)) return;
      const row = handle.closest('[data-navigation-row]');
      drag = row;
      row.classList.add('is-dragging');
      event.dataTransfer.effectAllowed = 'move';
      event.dataTransfer.setData('text/plain', row.dataset.label || 'move');
      event.stopPropagation();
    });

    form.addEventListener('dragover', event => {
      if (!drag) return;
      const target = peerTarget(event, drag);
      if (!target) return;
      event.preventDefault();
      event.dataTransfer.dropEffect = 'move';
      rowsOf(drag.parentNode).forEach(el => el.classList.toggle('is-reorder-target', el === target));
    }, true);

    form.addEventListener('dragleave', event => {
      const target = event.target.closest?.('[data-navigation-row]');
      if (target && !target.contains(event.relatedTarget)) target.classList.remove('is-reorder-target');
    });

    form.addEventListener('drop', event => {
      if (!drag) return;
      const target = peerTarget(event, drag);
      if (!target) return;
      event.preventDefault();
      event.stopPropagation();
      const list = rowsOf(drag.parentNode);
      const from = list.indexOf(drag);
      const to = list.indexOf(target);
      if (from < 0 || to < 0) {
        clearDrag();
        return;
      }
      drag.parentNode.insertBefore(drag, from < to ? target.nextSibling : target);
      const moved = drag;
      clearDrag();
      refresh();
      announce(moved);
      moved.querySelector('[data-navigation-drag]')?.focus();
    }, true);

    form.addEventListener('dragend', clearDrag);
    form.addEventListener('keydown', event => {
      if (event.key === 'Escape' && drag) clearDrag();
    });

    refresh();
  };
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', () => init(document));
  else init(document);
  document.addEventListener('htmx:afterSwap', e => init(e.target));
})();
