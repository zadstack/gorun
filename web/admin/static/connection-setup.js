(() => {
  const official = provider => ({openai: 'https://api.openai.com/v1', anthropic: 'https://api.anthropic.com/v1'})[provider] || '';
  const fill = (kind, provider, current) => kind === 'ai' && !current.trim() ? official(provider) : current;
  if (typeof module !== 'undefined') module.exports = {fill};
  if (typeof document === 'undefined') return;
  document.querySelectorAll('[data-connection-setup]').forEach(form => {
    const endpoint = form.elements.endpoint, kind = form.elements.kind, provider = form.elements.provider, button = form.querySelector('[data-official-endpoint]');
    const refresh = () => { button.hidden = kind.value !== 'ai'; button.disabled = !!endpoint.value.trim() || !official(provider.value); };
    button.addEventListener('click', () => { endpoint.value = fill(kind.value, provider.value, endpoint.value); endpoint.dispatchEvent(new Event('input', {bubbles: true})); endpoint.focus(); });
    [endpoint, kind, provider].forEach(el => { el.addEventListener('input', refresh); el.addEventListener('change', refresh); });
    refresh();
  });
})();
