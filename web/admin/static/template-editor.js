// No network dependency and no failure can prevent native editing or saving.
const source = document.querySelector('[data-code-source]');
if (source) {
  import('./vendor/codemirror-editor.js').then(({enhance}) => enhance(source)).catch(() => {
    const status = document.querySelector('[data-code-status]');
    if (status) status.textContent = source.dataset.fallback;
  });
}
