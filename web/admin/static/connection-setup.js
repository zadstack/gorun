(() => {
  const official = provider => ({openai: 'https://api.openai.com/v1', anthropic: 'https://api.anthropic.com/v1'})[provider] || '';
  const fill = (kind, provider, current) => kind === 'ai' && !current.trim() ? official(provider) : current;
  if (typeof module !== 'undefined') module.exports = {fill};
  if (typeof document === 'undefined') return;
  document.querySelectorAll('[data-connection-setup]').forEach(form => {
    const endpoint = form.elements.endpoint, kind = form.elements.kind, provider = form.elements.provider, button = form.querySelector('[data-official-endpoint]');
    const refresh = () => { button.hidden = kind.value !== 'ai'; button.disabled = !!endpoint.value.trim() || !official(provider.value);
      const ar=document.documentElement.lang==='ar',purpose=form.querySelector('[data-connection-purpose]');
      const help={mcp:ar?'اتصال أدوات: احفظ عنوان خادم MCP، ثم اختبر الاكتشاف دون تنفيذ أدوات.':'Tool connection: save the MCP server endpoint, then test discovery without executing tools.',ai:ar?'نموذج للكتابة: اختر البروتوكول والموديل، واحفظ قبل اختبار الاتصال.':'Writing model: choose its protocol and model, then save before testing.',s3:ar?'تخزين ملفات: استخدم عنوان S3 واسم الحاوية. الحفظ لا ينقل الملفات ولا يفعّل الوجهة.':'File storage: use the S3 endpoint and bucket name. Saving does not move files or activate storage.'};
      if(purpose)purpose.textContent=help[kind.value]||'';
      endpoint.placeholder=kind.value==='ai'?official(provider.value):kind.value==='s3'?'https://ACCOUNT_ID.r2.cloudflarestorage.com':'https://tools.example.com/mcp';
    };
    button.addEventListener('click', () => { endpoint.value = fill(kind.value, provider.value, endpoint.value); endpoint.dispatchEvent(new Event('input', {bubbles: true})); endpoint.focus(); });
    [endpoint, kind, provider].forEach(el => { el.addEventListener('input', refresh); el.addEventListener('change', refresh); });
    refresh();
  });
})();
