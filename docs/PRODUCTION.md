# GoRun: التشغيل والإصدار والاستعادة

دليل المشغّل للإنتاج: HTTPS والوكيل وHSTS، النسخ والاستعادة (بعد مسار الاستعادة المحصور)، والترقية من وسوم الإصدار. الفهرس: [docs/README.md](README.md). الأعطال: [TROUBLESHOOTING.md](TROUBLESHOOTING.md). التثبيت الأول: [GORUN-INSTALLATION.md](GORUN-INSTALLATION.md).

لقياس زمن الاستجابة والذاكرة والمعالج تحت قرّاء متزامنين على SQLite محلية، استخدم
`make loadtest` ودليل [LOADTEST.md](LOADTEST.md) (ليس جزءًا من CI).

## الإصدارات المنشورة

عند دفع وسم `v*` يبني سير العمل `.github/workflows/release.yml` صورة `ghcr.io/zadstack/gorun` لمعماريتي linux/amd64 وlinux/arm64 (وسوم `X.Y.Z` و`X.Y` و`latest`، وتحمل `VERSION` من الوسم ووسوم OCI للمصدر والترخيص)، ويتحقق من أن `gorun version` داخلها يطابق الإصدار، ويبني الأرشيفات التنفيذية الأربعة مع `SHA256SUMS` بالأداة أدناه، ثم ينشئ إصدار GitHub بملاحظات قسم الإصدار في `CHANGELOG.md`. طلبات الدمج التي تغيّر Dockerfile أو سير العمل تبني كل ذلك دون نشر. ثبّت الإنتاج على وسم رقمي (`0.1.0` أو `0.1`) لا على `latest` وحدها.

## نطاق الإصدار المحلي

التطبيق يعمل بعقدة واحدة وSQLite. الحزم المحلية تتضمن الملف التنفيذي والقوالب المطلوبة، ولا تنشر إلى GitHub أو خادم. اختبارات الفوترة تستخدم خادمًا بديلًا؛ نجاحها لا يؤكد الدفع الحقيقي أو وصول البريد.

```sh
python3 tools/package-release.py --version 0.1.0-rc.6 --target darwin/arm64 --target linux/amd64 --target linux/arm64
cd dist
shasum -a 256 -c SHA256SUMS-0.1.0-rc.6.txt
```

استخدم رقم إصدار جديدًا عند إعادة التغليف؛ الأداة لا تستبدل ملفات إصدار موجود.

فك الحزمة وشغّل `./gorun serve` من داخل مجلدها، لأن `themes/` مطلوبة. لا تُعد تشغيل التطبيق من مجلد `dist` خارج الحزمة. أداة التغليف ترفض استبدال حزمة موجودة.

## الإعداد للإنتاج

اضبط القيم التالية في مخزن أسرار أو ملف بيئة بصلاحية `0600` خارج المستودع:

- `GORUN_ENV=prod`
- `GORUN_BASE_URL=https://your-site.example`
- `GORUN_SECRET_KEY`: مفتاح ثابت من `openssl rand -base64 32`، يُحفظ مع خطة الاستعادة.
- `GORUN_DATA`: مجلد بيانات دائم مملوك لمستخدم الخدمة.
- `GORUN_SETUP_TOKEN`: رمز من `openssl rand -hex 24` عند إنشاء أول مدير من الويب. بدونه استخدم CLI.

من مجلد المصدر، شغّل Docker Compose الموجود في `deploy/` خلف HTTPS، أو الملف التنفيذي تحت مدير خدمات. لا تشغّل نسختين أو worker منفصلًا على قاعدة البيانات نفسها: العملية التي تشغّل workers (`gorun serve` و`gorun mcp --jobs` و`gorun reindex` دون `--no-jobs`) تأخذ قفلًا حصريًا من نظام التشغيل على `<data>/gorun.lock`، وترفض عملية ثانية التشغيل وتذكر رقم العملية التي تملك القفل. يتحرر القفل عند الإيقاف أو إذا توقفت العملية فجأة، فلا يحتاج الملف المتبقي إلى حذف. في النشر عبر Docker أو Coolify أوقف الحاوية القديمة قبل تشغيل الجديدة على الـvolume نفسه؛ وإلا تخرج الجديدة فورًا برسالة القفل. المهام التي تعمل حاليًا تحدّث `locked_at` كل 30 ثانية، ولا يُعاد إلى الطابور عند التشغيل إلا ما توقف تحديثه أكثر من دقيقتين. `GORUN_ALLOW_PRIVATE_NET` مغلق إلا عندما تحتاج خادم فوترة أو مصدرًا داخل الشبكة المحلية.

```sh
VERSION=$(git describe --tags --always --dirty) docker compose -f deploy/docker-compose.yml up -d --build
docker exec <container> /app/gorun healthcheck
```

يمرّر `VERSION` رقم النسخة إلى الصورة فيظهر في `gorun version` ولوحة الإدارة بدل `dev`. في Coolify فعّل "Include Source Commit in Build" فيُستخدم `SOURCE_COMMIT` تلقائيًا.

Docker وCompose غير مطلوبين للتشغيل المحلي من الحزمة. يظل اختبار صورة Docker الفعلي مطلوبًا على مضيف تتوفر عليه الأدوات قبل نشرها.

## HTTPS والوكيل العكسي وHSTS

ضع GoRun خلف وكيل TLS (Traefik أو Caddy أو nginx أو Coolify). التطبيق يستمع عادة على `127.0.0.1:8080` أو `:8080` داخل الشبكة الخاصة بالوكيل؛ لا تفتح منفذ التطبيق للعامة مباشرة.

| الإعداد | الدور |
| --- | --- |
| `GORUN_BASE_URL` | العنوان العام `https://…` بلا شرطة أخيرة؛ مطلوب في `prod` وليس loopback |
| `GORUN_TRUSTED_PROXIES` | عناوين/CIDR للوكيل فقط؛ فارغ = لا ثقة بترويسات التمرير |
| `GORUN_HSTS_INCLUDE_SUBDOMAINS` | اختياري؛ فقط إن كانت **كل** النطاقات الفرعية على HTTPS |
| `GORUN_HSTS_PRELOAD` | اختياري؛ يتطلب الخيار السابق؛ الخروج من قائمة preload بطيء |

سلوك الخادم:

- يرسل `Strict-Transport-Security: max-age=31536000` لطلبات HTTPS الفعلية فقط (TLS مباشر أو `X-Forwarded-Proto: https` من وكيل موثوق).
- لا يثق بـ`X-Forwarded-For` / `X-Forwarded-Proto` إلا من العناوين في `GORUN_TRUSTED_PROXIES`. خلف Coolify افحص شبكة `coolify` كما في [deploy/coolify.md](../deploy/coolify.md). لا تستخدم `0.0.0.0/0`.
- علم `Secure` على الكوكيز يتبع HTTPS الطلب **أو** كون `GORUN_BASE_URL` يبدأ بـ`https://`؛ أما HSTS وعنوان العميل الحقيقي فيتطلبان وكيلًا موثوقًا.
- بعد النشر: سجّل دخولًا وافتح **وكلاء الذكاء الاصطناعي ← التدقيق** وتأكد أن IP المسجّل هو عنوانك العام لا عنوان الوكيل.
- قائمة قبول المشغّل (CIDR، Secure، HSTS، صحة من خارج الحاوية): [deploy/coolify.md §9](../deploy/coolify.md#9-https-acceptance-checklist).

تفاصيل حدود الدخول وحظر IP: [LOGIN-SECURITY.md](LOGIN-SECURITY.md).

## تغييرات الأمان والسلوك

- الاستماع الافتراضي في `dev` محلي فقط، وفي `prod` على `:8080`. الرمز المضبوط للإعداد يُطلب في البيئتين.
- دخول المستخدم محدود بخمس محاولات لكل IP وبريد، و30 محاولة لكل IP خلال 15 دقيقة. المستخدمون خلف NAT يشتركون في ميزانية IP؛ نجاح الدخول لا يصفرها. أربع عمليات تحقق Argon2 كحد أقصى في الوقت نفسه.
- إنشاء مسودة قالب عبر MCP يتطلب موافقة. المعاينة HTML ثابتة داخل sandbox: السكريبتات والنماذج والأصول الخارجية ممنوعة؛ افحص التفاعلات على بيئة اختبار منفصلة بعد مراجعة المصدر.
- مزود OpenAI يحتاج endpoint صريحًا، في اللوحة وفي `gorun ai set --base-url`. لا يوجد عنوان جهة ثالثة تلقائي. إعداد قديم بلا عنوان يعطّل الطلب بدل إرساله إلى وجهة افتراضية.
- الصيانة تحجب صفحات المتجر والحساب وcheckout مع صفحات الموقع، وتترك الإدارة وملفاتها، REST/MCP، webhooks، health، sitemap، robots والوسائط متاحة.
- مكتبات واجهة الإدارة مثبتة النسخ مع integrity؛ كل مكتبات لوحة الإدارة مضمّنة محليًا، ويمنع CSP فيها السكريبتات الخارجية. القالب الافتراضي يستخدم سكربتات محلية؛ الصفحات العامة لقوالب Studio الثلاثة تعمل بـCSS أصلي دون سكربتات أو خطوط خارجية، بينما لوحة منشئ المواقع تستخدم مكتبات الإدارة المحلية؛ ولا يسمح CSP في الصفحات العامة بأي CDN ولا بـ`'unsafe-eval'`. القالب المخصص الذي يحتاج CDN أو Alpine يُسمح له صراحة عبر `GORUN_CSP_SCRIPT_SRC` (مصادر https و`'unsafe-eval'` فقط). Tailwind في القالب الافتراضي CSS مبني مسبقًا (`themes/default/assets/tailwind.css`) فلا يعمل مترجم داخل المتصفح ولا يحتاج الخادم Node؛ القالب المخصص الذي يحتاج المترجم يضيف `{{part "tailwind-runtime" .}}` إلى `parts/head-extra.html`.
- HSTS والوكلاء الموثوقون: انظر القسم أعلاه؛ التفاصيل الإضافية في [أمان الدخول](LOGIN-SECURITY.md) و[Coolify](../deploy/coolify.md).

## الترقية من وسم إصدار

1. نسخة احتياطية + حفظ الأسرار خارج الأرشيف الافتراضي.
2. أوقف العملية القديمة (قفل `gorun.lock`).
3. ثبّت أرشيف/صورة الوسم الجديد بنفس `GORUN_DATA` و`GORUN_SECRET_KEY`.
4. راقب migrations عند `serve`، ثم افحص الصحة ودخول الأدمن.
5. الرجوع: ثنائي/صورة سابقة + مجلد مُستعاد + المفتاح الأصلي.

**دليل التشغيل الكامل:** [UPGRADE.md](UPGRADE.md). ملخص التثبيت:
[GORUN-INSTALLATION.md](GORUN-INSTALLATION.md#upgrades-from-release-tags--الترقية-من-وسوم-الإصدار).

## النسخ الاحتياطي

لنسخة تشمل الملفات وقاعدة البيانات في نقطة متسقة، أوقف عمليات الكتابة أولًا. `VACUUM INTO` يعطي قاعدة بيانات متسقة أثناء التشغيل، لكن قاعدة البيانات وأرشيف الوسائط ليسا transaction مشتركة.

```sh
gorun backup --data /var/lib/gorun
# اختياري: أبقِ أحدث N زوجًا مكتملًا واحذف الأقدم (مفيد مع cron المضيف)
gorun backup --data /var/lib/gorun --retain 7
```

النسخة زوج ملفات `.db` و`.tar.gz` يحمل الاسم الزمني نفسه. الأرشيف يشمل `media/` و`overrides/` و`extensions/` و`form-uploads/`. **لا يشمل `secret.key` ولا `gorun.toml` افتراضيًا**، لأنهما يحملان مفتاح الموقع وأسرارًا أخرى (Stripe وS3 ورمز الإعداد) بنص صريح. احتفظ بنسخة مستقلة ومحمية من `secret.key` أو `GORUN_SECRET_KEY` ومن `gorun.toml`؛ بدون المفتاح الأصلي لا تُفك الإعدادات المشفرة في القاعدة. لإدراجهما عمدًا في أرشيف تحفظه مشفرًا خارج الخادم استخدم `gorun backup --include-secrets`؛ لوحة الإدارة لا تدرجهما أبدًا. تنزيل نسخة من لوحة الإدارة يتطلب إدخال كلمة المرور مجددًا إن مضى على آخر دخول أكثر من 10 دقائق. الملفات بصلاحية `0600`؛ احتفظ بها خارج مضيف الخدمة بصورة آمنة.

**جدولة محلية:** من **تشغيل النظام ← النسخ الاحتياطية** عيّن كل N ساعة (1–168؛ 0 = متوقف) واختياريًا احتفظ بـ N زوجًا محليًا. يعمل ذلك فقط بينما `gorun serve` يعمل مع عمال المهام؛ لا يرفع إلى S3 ولا يحذف كائنات الحاوية. بدلًا من ذلك استخدم cron المضيف / `docker exec … /app/gorun backup --retain N` (انظر [Coolify](../deploy/coolify.md)).

متغيرات البيئة الخارجية ومحتوى bucket الخاص بـS3 لا تدخل في الأرشيف. احتفظ بالمفتاح الأصلي والاعتمادات الخارجية، وانسخ bucket ضمن خطة التخزين الخاصة بك. تغيير المفتاح يجعل أسرار قاعدة البيانات غير قابلة للفك.

قبل نقل الموقع للعمل يمكنك استخدام `gorun backup-preview --input …` لمعاينة عامة مؤقتة ومعزولة على localhost. راجع [النسخ التلقائي والاستعادة الآمنة](BACKUPS.md) لجدولة النسخ اليومية أو الأسبوعية والتنبيهات وسياسة الاحتفاظ بالنسخ التي اجتازت الفحص.

## الاستعادة والرجوع

الاستعادة لا تُدمج أبدًا في مجلد البيانات الحي، ولها طريقان. ابدأ في الحالتين بفحص زوج النسخة؛ الفحص السليم محليًا يعرض عادةً `local_ok` مع قائمة تحقق للانتقال الحي، أو `review_required` عند وجود تحذيرات خاصة بالموقع (مثل وسائط S3).

**أثناء التشغيل (لوحة الإدارة):** من **تشغيل النظام ← النسخ الاحتياطية ← فحص / استعادة** تفحص النسخة ثم تستعيدها بعد إعادة إدخال كلمة المرور، والموقع يظل يعمل. تُكتب النسخة في مجلد مؤقت ثم تُنقل دفعة واحدة إلى `<data>/restores/<timestamp>/`، فإن انقطعت الاستعادة بقي مجلد `.<timestamp>.partial` تعرضه صفحة الفحص؛ احذفه ثم أعد الاستعادة. هذا المجلد داخل مجلد البيانات الحي، فلا تشغّل الخدمة عليه مباشرة: انقله أولًا إلى خارج مجلد البيانات، ثم أكمل من الخطوة 3.

**دون تشغيل (CLI):** أوقف الخدمة وكل CLI/worker الذي يستخدم مجلد البيانات، واحتفظ بالمجلد الحالي للرجوع، ثم:

1. افحص الزوج:

```sh
gorun backup-inspect --input /var/lib/gorun/backups/<timestamp>.manifest.json
```

2. استعد إلى مسار مجلد جديد (يجب ألا يكون موجودًا) خارج مجلد البيانات:

```sh
gorun backup-restore --input /var/lib/gorun/backups/<timestamp>.manifest.json --dest /var/lib/gorun-restored
```

   لا تخلط DB مستعادة مع WAL/SHM من قاعدة قديمة. الملفات المستعادة بصلاحية `0600` والمجلدات `0700` مهما كانت الصلاحيات المسجلة في الأرشيف.

بعد أي من الطريقين:

3. انسخ `secret.key` و`gorun.toml` المحفوظين مستقلًا إلى مجلد الاستعادة (إلا إن كانت النسخة أُنشئت بـ`--include-secrets`)، أو اضبط `GORUN_SECRET_KEY` الخارجي الأصلي. استعد ملكية مستخدم الخدمة. لا تولّد مفتاحًا جديدًا.
4. أوقف كل عملية على المجلد القديم، وأعد أصول S3 وأسرار البيئة الخارجية إن كانت مستخدمة. شغّل الإصدار المطابق للنسخة بـ`--data` على مجلد الاستعادة وتحقق من الصحة ودخول الأدمن والمحتوى وقراءة إعدادات التكامل.
5. قبل إصدار جديد احتفظ بالحزمة القديمة وزوج النسخة والإعدادات. إن فشل التحديث، أوقف الخدمة، وأعد الحزمة القديمة ومجلد البيانات المستعاد مع المفتاح الأصلي ثم أعد فحص الرحلات.

## المراقبة الخفيفة (اختياري)

`GORUN_METRICS=true` يفعّل `GET /metrics` بصيغة Prometheus نصية (عدادات HTTP، مقاييس المهام، وقت آخر نسخة محلية ناجحة). اختياري: `GORUN_METRICS_TOKEN` لـBearer. عطّل افتراضيًا؛ اسحب من الشبكة الخاصة ولا تفتح المسار للعامة بلا رمز أو ACL. السجلات في `prod` JSON على stderr للشحن عبر مُجمّع المضيف. الدليل: [OBSERVABILITY.md](OBSERVABILITY.md).

## قبول النشر الفعلي

بعد تحديد المضيف، تحقق من HTTPS وrestart وvolume والصحة خارج الحاوية، ثم عملية Stripe اختبارية كاملة وwebhook والفوترة وسحب الوصول بعد refund، وإرسال بريد فعلي واستعادة على بيانات اختبار. هذه فحوصات مستقلة عن نجاح الاختبارات المحلية.

نشر المصدر للعامة خطوة منفصلة: fixtures المسجلة من الموقع الحقيقي، التخطيط الداخلي، البراند وتاريخ Git تحتاج قرار نشر وتنقية. الحزم الثنائية لا تضم fixtures أو وثائق التخطيط الداخلية؛ لم يُعد كتابة تاريخ المستودع.

## حدود قراءة المحتوى

الكاتب والمساهم يقرآن محتواهما والمحتوى المنشور من الأنواع العامة؛ تاريخ مراجعات الآخرين ومسودات الترجمات غير المنشورة لا يُعرض لهما. القاعدة نفسها مطبقة على جلسات المستخدم وREST/MCP. الأدمن والمحرر يحتفظان بوصول التحرير، وخدمات النظام تعمل بسياقها الداخلي. الأنواع غير العامة لا تدخل في البحث العام أو دوال القوالب أو الصفحة الرئيسية، حتى لو كانت حالة العنصر published.

تقرير الترحيل يعرض unverified_checks للفحوصات unavailable أو skipped. الحالة validated تعني إنشاء التقرير، ولا تثبت خلوه من أخطاء أو اكتمال كل فحص. يبقى الترحيل running حتى انتهاء إنشاء التقرير لتستمر شاشة التقدم في التحديث.

## منشئ المواقع

جداول `builder_documents` داخل SQLite تدخل تلقائيًا في النسخ الاحتياطية. تشمل المسودة الحالية ونسخة JSON من آخر تخطيط منشور؛ المراجعات القديمة للمحتوى تظل HTML. لا تغيّر بيانات JSON أو رموز النسخة يدويًا أثناء التشغيل. استعد قاعدة البيانات والوسائط والقوالب معًا.

إنشاء موقع بداية يضيف مسودات وقائمة غير مفعلة: خمس صفحات للشركة أو البورتفوليو وأربع للمجلة، مع اختيار قالب Studio وهويته. التفعيل الصريح ينشر المجموعة ويغيّر الرئيسية والقائمة واللغة والقالب والهوية؛ المحتوى القديم محفوظ ويمكن الرجوع إليه من الإعدادات. التشغيل على عقدة واحدة يظل شرطًا.

## تجهيز محتوى الموقع قبل النشر

أكمل معالج بيانات النشاط وراجع الصفحات، ثم افتح شاشة الجاهزية. [دليل إنشاء الموقع والجاهزية](SITE_BUILDER.md) يشرح الحالات وروابط الإصلاح واختبار التواصل. الجاهزية المحلية لا تنشر إلى خادم ولا تختبر وصول البريد أو DNS/TLS أو استعادة النسخة. احتفظ بقبول البيئة الفعلية كخطوة مستقلة عند تحديد وجهة النشر.

---

## English — production checklist

1. **Secrets:** `GORUN_ENV=prod`, public `GORUN_BASE_URL`, stable `GORUN_SECRET_KEY`, persistent `GORUN_DATA`, optional `GORUN_SETUP_TOKEN` for web bootstrap.
2. **One process per data dir:** stop-then-start on Docker/Coolify; lock file is `<data>/gorun.lock`.
3. **HTTPS / proxy acceptance** (Coolify or `ghcr.io/zadstack/gorun` behind any reverse proxy) — full recipe: [deploy/coolify.md §9](../deploy/coolify.md#9-https-acceptance-checklist):
   - Discover the proxy hop: Coolify `docker network inspect coolify` (or the proxy container IP); set `GORUN_TRUSTED_PROXIES` to that CIDR/IP only — never `0.0.0.0/0`.
   - Cookie `Secure`: `curl -sSI https://<host>/admin/login` → `Set-Cookie` includes `Secure` (also follows `GORUN_BASE_URL=https://…`).
   - HSTS: `curl -sSI https://<host>/healthz` → `Strict-Transport-Security: max-age=31536000`. Opt-in only: `GORUN_HSTS_INCLUDE_SUBDOMAINS` / `GORUN_HSTS_PRELOAD`.
   - Health **outside** the container: `curl -fsS https://<host>/healthz` and `/readyz` → `200`. `docker exec … healthcheck` alone is not acceptance.
   - Confirm **AI Agents → Audit** shows your public IP after login; no recurring untrusted-peer log warning.
4. **Backup:** `gorun backup` (or admin). Optional local schedule + retain-N on **System → Backups**, or host cron with `gorun backup --retain N`. Default archive omits `secret.key` / `gorun.toml` unless `--include-secrets`. Encrypted S3 upload stays manual.
5. **Restore:** never merges into the live dir. Online: **Operations → Backups → Inspect / restore** → `<data>/restores/<stamp>/` → move out → stop old process → `serve --data` the moved copy with original secrets. Offline: `gorun backup-restore --dest <new-empty-dir>` while stopped.
6. **Staging / promote:** second data dir + process for staging; `gorun staging seed` / `gorun staging promote` wrap backup → restore into a **new** dest, then stop live and point `--data` (never merges). Full path: [STAGING.md](STAGING.md).
7. **Upgrade:** pin `ghcr.io/zadstack/gorun:X.Y.Z` or the matching binary archive; migrate on `serve`; roll back with previous artifact + restored data + original key. Full runbook: [UPGRADE.md](UPGRADE.md).
8. **Observability (optional):** `GORUN_METRICS=true` → `GET /metrics` (Prometheus text); optional `GORUN_METRICS_TOKEN`. Prod logs are JSON on stderr for host log shipping. Details: [OBSERVABILITY.md](OBSERVABILITY.md).
9. **Problems:** [TROUBLESHOOTING.md](TROUBLESHOOTING.md). Coolify: [deploy/coolify.md](../deploy/coolify.md).
