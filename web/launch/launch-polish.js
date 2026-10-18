/*! GoRun launch polish — title + client validation (ships in binary) */
(function () {
  var body = document.body;
  if (!body || !body.classList.contains('launch-page')) return;
  var lang = (document.documentElement.lang || 'en').toLowerCase();
  var isAr = lang.indexOf('ar') === 0;

  var brand = (document.querySelector('meta[name="launch-brand"]') || {}).content || 'GoRun';
  if (body.classList.contains('launch-login')) {
    document.title = isAr ? ('تسجيل الدخول — ' + brand) : ('Sign in — ' + brand);
  } else if (body.classList.contains('launch-verify-email') || body.classList.contains('launch-verify') || /\/start\/verify/.test(location.pathname)) {
    document.title = isAr ? ('تفعيل البريد — ' + brand) : ('Verify email — ' + brand);
  } else if (body.classList.contains('launch-start')) {
    document.title = isAr ? ('ابدأ موقعك — ' + brand) : ('Start your website — ' + brand);
  }

  var form = document.querySelector('form.start-form');
  if (!form) return;

  function msg(en, ar) { return isAr ? ar : en; }

  function ensureErrorEl(input) {
    var id = input.name + '-polish-error';
    var el = document.getElementById(id);
    if (el) return el;
    el = document.createElement('p');
    el.id = id;
    el.className = 'field-help polish-error';
    el.style.color = '#a3261f';
    el.setAttribute('role', 'alert');
    input.insertAdjacentElement('afterend', el);
    return el;
  }

  function clearErrors() {
    form.querySelectorAll('.polish-error').forEach(function (el) {
      el.textContent = '';
    });
    form.querySelectorAll('[aria-invalid="true"]').forEach(function (el) {
      el.removeAttribute('aria-invalid');
    });
  }

  function fail(input, text) {
    input.setAttribute('aria-invalid', 'true');
    ensureErrorEl(input).textContent = text;
    return false;
  }

  form.addEventListener('submit', function (ev) {
    clearErrors();
    var ok = true;
    var email = form.querySelector('[name="email"]');
    var password = form.querySelector('[name="password"]');
    var slug = form.querySelector('[name="slug"]');
    var name = form.querySelector('[name="name"]');
    var title = form.querySelector('[name="title"]');
    var template = form.querySelector('[name="template"]:checked');

    if (!template) {
      ok = false;
      var first = form.querySelector('[name="template"]');
      if (first) fail(first, msg('Choose a starting design.', 'اختر تصميم البداية.'));
    }
    if (name && !String(name.value || '').trim()) {
      ok = fail(name, msg('Enter your name.', 'أدخل اسمك.')) && ok;
      ok = false;
    }
    if (email) {
      var evl = String(email.value || '').trim();
      if (!evl || !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(evl)) {
        ok = false;
        fail(email, msg('Enter a valid email address.', 'أدخل بريدًا إلكترونيًا صالحًا.'));
      }
    }
    if (password) {
      var pv = String(password.value || '');
      if (pv.length < 10) {
        ok = false;
        fail(password, msg('Password must be at least 10 characters.', 'كلمة المرور 10 أحرف على الأقل.'));
      }
    }
    if (title && !String(title.value || '').trim()) {
      ok = false;
      fail(title, msg('Enter a website name.', 'أدخل اسم الموقع.'));
    }
    if (slug) {
      var sv = String(slug.value || '').trim().toLowerCase();
      slug.value = sv;
      if (!/^[a-z][a-z0-9-]{1,38}[a-z0-9]$/.test(sv)) {
        ok = false;
        fail(
          slug,
          msg(
            'Address: 3–40 chars, start with a letter, lowercase letters/numbers/hyphens.',
            'العنوان: 3–40 حرفًا، يبدأ بحرف، أحرف إنجليزية صغيرة/أرقام/شرطات.'
          )
        );
      }
    }
    if (!ok) ev.preventDefault();
  });
})();
