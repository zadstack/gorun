(() => {
 'use strict';
 document.querySelectorAll('input[name="starter"][data-identity]').forEach(radio => {
  radio.addEventListener('change', () => {
   if (!radio.checked) return;
   const identity = JSON.parse(radio.dataset.identity), form = radio.closest('form');
   ['accent','background','text','font','width'].forEach(key => {
    const field = form.elements.namedItem(key);
    if (field) field.value = identity[key];
   });
  });
 });
})();
