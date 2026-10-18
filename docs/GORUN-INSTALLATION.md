# تثبيت GoRun وتشغيله

دليل المشغّل لتثبيت جديد (ملف تنفيذي أو Docker/GHCR)، أول مدير، الترقية من وسوم الإصدار، والنسخ الاحتياطي. للفهرس الكامل راجع [docs/README.md](README.md). لأعطال شائعة راجع [استكشاف الأعطال](TROUBLESHOOTING.md).

جُرب البناء والتشغيل والأوامر الأساسية على macOS Apple Silicon في مجلد بيانات مستقل. أوامر Linux والإنتاج أدناه مستمدة من المصدر الحالي (`v0.2.0` وما بعده على `main`).

## المصدر والترخيص

GoRun برنامج حر مفتوح المصدر بترخيص **GNU Affero General Public License الإصدار 3 فقط** (`AGPL-3.0-only`)، كما في ملفي [LICENSE](../LICENSE) و[NOTICE](../NOTICE). يحق لأي شخص أو جهة، أفرادًا وشركات ومنظمات، استخدامه وتعديله وإعادة توزيعه بشروط هذا الترخيص، ومنها: إذا شغّلت نسخة معدّلة منه كخدمة عبر الشبكة، فعليك إتاحة الشيفرة المصدرية المقابلة لمستخدميها. المكونات الخارجية المضمّنة تحتفظ بتراخيصها كما يوضح `NOTICE`. المرجع النهائي هو ملف LICENSE المرفق بالنسخة التي تحصل عليها.

الحزمة المدفوعة والتركيب والتشغيل المُدار خدمات مستقلة لا تغيّر ترخيص النواة. لا يتضمن هذا الدليل وعدًا بتحديثات مدى الحياة أو مدة دعم أو عقد شركة غير محدد.

المستودع الرسمي هو [github.com/zadstack/gorun](https://github.com/zadstack/gorun)، والإصدارات الموسومة (الأرشيفات التنفيذية وصورة `ghcr.io/zadstack/gorun`) منشورة في [صفحة الإصدارات](https://github.com/zadstack/gorun/releases).

## ما الذي تحتاجه؟

للبناء من المصدر: Go1.27 ؛ ويقترح go.mod الأداة go1.27.2. استُخدمت go1.27.2 في التجربة. قد يجلب Go الأداة والاعتماديات أثناء أول بناء، فتحتاج اتصالًا لتنزيلها ما لم تكن مخزنة محليًا. يوضح [دليل Go الرسمي](https://go.dev/doc/toolchain) كيفية اختيار الأداة. تحتاج `make` للأمر المختصر أدناه، أو تستعمل `go build` مباشرة.

للتشغيل من حزمة جاهزة: ملف gorun المطابق لنظامك ومعماريتك، ومجلد themes المرفق، ومجلد بيانات قابل للكتابة. لا يحتاج التشغيل Go أو PHP أو Node/npm أو خادم قاعدة بيانات منفصل؛ يستخدم SQLite. هذا لا يعني أن الملف التنفيذي وحده يشمل قوالب الموقع.

## التثبيت من إصدار منشور

بدءًا من v0.1.0 تُنشر الإصدارات على [صفحة الإصدارات في GitHub](https://github.com/zadstack/gorun/releases):

- **أرشيفات تنفيذية** لـ linux وdarwin بمعماريتي amd64 وarm64 باسم `gorun-<الإصدار>-<النظام>-<المعمارية>.tar.gz`، وفيها `gorun` ومجلد `themes/` والوثائق، مع ملف `SHA256SUMS-<الإصدار>.txt`. تحقق من البصمة قبل فك الأرشيف، ثم شغّل `./gorun serve` من **داخل** مجلد الفك حتى يجد `themes/`.
- **صورة حاوية** متعددة المعماريات `ghcr.io/zadstack/gorun` بالوسوم `0.2.0` و`0.2` و`latest` (وثبّت دائمًا على رقم إصدار في الإنتاج). تعمل بوضع الإنتاج (`GORUN_ENV=prod`) وتحتاج `GORUN_BASE_URL` و`GORUN_SECRET_KEY` وvolume دائم على `/data`. القوالب مضمّنة في الصورة.

### ملف تنفيذي (مثال linux/amd64)

```sh
curl -fLO https://github.com/zadstack/gorun/releases/download/v0.2.0/gorun-0.2.0-linux-amd64.tar.gz
curl -fLO https://github.com/zadstack/gorun/releases/download/v0.2.0/SHA256SUMS-0.2.0.txt
sha256sum --ignore-missing -c SHA256SUMS-0.2.0.txt
tar xzf gorun-0.2.0-linux-amd64.tar.gz
cd gorun-0.2.0-linux-amd64
GORUN_ENV=dev GORUN_DATA=./data GORUN_BASE_URL=http://127.0.0.1:8080 ./gorun serve
```

افتح `http://127.0.0.1:8080/admin/setup`. في الإنتاج استبدل المتغيرات كما في قسم متطلبات الإنتاج أدناه، واستمع على loopback خلف وكيل TLS.

### Docker / GHCR

```sh
docker pull ghcr.io/zadstack/gorun:0.2.0
docker run --rm --entrypoint /app/gorun ghcr.io/zadstack/gorun:0.2.0 version

docker run -d --name gorun -p 8080:8080 -v gorun-data:/data \
  -e GORUN_BASE_URL=https://cms.example.com \
  -e GORUN_SECRET_KEY="$(openssl rand -base64 32)" \
  -e GORUN_SETUP_TOKEN="$(openssl rand -hex 24)" \
  -e GORUN_TRUSTED_PROXIES=10.0.1.0/24 \
  ghcr.io/zadstack/gorun:0.2.0
```

احفظ `GORUN_SECRET_KEY` خارج الحاوية قبل التشغيل الأول. خلف Traefik أو Coolify اضبط `GORUN_TRUSTED_PROXIES` على شبكة الوكيل الحقيقية، ثم نفّذ قائمة قبول HTTPS (CIDR، Secure، HSTS، صحة من خارج الحاوية) في [Coolify §9](../deploy/coolify.md#9-https-acceptance-checklist). التفاصيل أيضًا في [الإنتاج](PRODUCTION.md). Compose من المصدر: `deploy/docker-compose.yml`.

## بناء المصدر

نفّذ من جذر نسخة المصدر التي تحتوي go.mod و cmd/gorun و themes:

```sh
go version
make build
./bin/gorun version
./bin/gorun help
```

ينشئ `make build` الملف `bin/gorun` باستخدام `CGO_ENABLED=0`. البديل دون make:

```sh
mkdir -p bin
CGO_ENABLED=0 go build -trimpath -o bin/gorun ./cmd/gorun
```

قد تعرض النسخة المحلية `GoRun dev` ، خصوصًا عند البناء من أرشيف دون.git أو دون تعيين main.version. رقم النسخة وحده لا يثبت مصدر البناء؛ احتفظ بمعرّف المصدر وبصمة الملف التنفيذي عند تسليم إصدار.

## تشغيل محلي لأول مرة

هذا المثال محلي فقط، ويربط الخادم بالـ loopback:

```sh
GORUN_ENV=dev \
GORUN_DATA=./data \
GORUN_THEMES=./themes \
GORUN_ADDR=127.0.0.1:8080 \
GORUN_BASE_URL=http://127.0.0.1:8080 \
./bin/gorun serve
```

افتح `http://127.0.0.1:8080/admin/setup` وأنشئ أول مدير. في dev لا يحتاج النموذج رمز إعداد إلا إذا ضُبط رمز صراحة. بعد وجود أي مستخدم يُغلق مسار الإعداد الأول، ويكون تسجيل الدخول من `/admin/login` ، والإدارة من `/admin`.

في طرفية أخرى:

```sh
./bin/gorun healthcheck --addr 127.0.0.1:8080
```

يبقى `serve` في المقدمة؛ أوقف التجربة بـ Ctrl+C. ينشئ التشغيل الأول قاعدة البيانات ويطبق migrations تلقائيًا، ويولّد في dev ملف `data/secret.key` بصلاحية 0600 عند غياب مفتاح آخر. حافظ على المفتاح؛ البيانات المشفرة لا تصبح قابلة للاسترجاع بمجرد إنشاء مفتاح جديد.

### إنشاء المدير من الطرفية بدل نموذج الإعداد

نفّذ قبل تشغيل الموقع أو على موقعك الذي تملك صلاحية تشغيله. المثال لـ bash أو zsh ؛ يستقبل كلمة المرور دون إظهارها أو وضعها ضمن معاملات العملية:

```sh
printf 'Admin password: ' >&2
IFS= read -r -s gorun_install_password
printf '\n' >&2
printf '%s\n' "$gorun_install_password" | \
  ./bin/gorun user create --data ./data \
  --email you@example.com --name Owner --admin --password-stdin
unset gorun_install_password
```

استبدل البريد ببريدك. الحد الأدنى الحالي لكلمة المرور 10 محارف، ويقرأ `--password-stdin` السطر الأول. وجود `--admin` مهم: بدونه ينشئ هذا الأمر editor. لا تنشئ editor أولًا ثم تتوقع بقاء نموذج أول مدير متاحًا. لا تحتاج أمر `init` ؛ ليس أمرًا موجودًا في هذه النسخة.

## البيانات والإعدادات

`--data` أو `GORUN_DATA` يحدد مجلد الموقع؛ الافتراضي `./data`. داخل المجلد توجد `gorun.db` وملفات الوسائط والتخصيصات ومفتاح dev عند إنشائه. لا تشارك مجلد البيانات أو المنفذ بين مواقع أو تجارب مستقلة.

ترتيب إعدادات التشغيل: الأعلام ← متغيرات البيئة ←`<data>/gorun.toml` ← الافتراضيات. تحديد مجلد data نفسه يأتي من العلم أو البيئة أو الافتراضي فقط؛ لا يوجد خيار data_dir داخل TOML.

| الغرض | متغير البيئة | العلم أو TOML |
|---|---|---|
| البيئة | `GORUN_ENV` | `--env` / `env` |
| مجلد البيانات | `GORUN_DATA` | `--data` |
| عنوان الاستماع | `GORUN_ADDR` | `--addr` / `addr` |
| عنوان الموقع | `GORUN_BASE_URL` | `--base-url` / `base_url` |
| اسم القالب | `GORUN_THEME` | `--theme` / `theme` |
| مجلد القوالب | `GORUN_THEMES` | متغير مستقل؛ ليس اسم القالب |
| مفتاح التشفير | `GORUN_SECRET_KEY` | `secret_key` في TOML ؛ 32 بايت بعد فك Base64 |
| رمز الإعداد الأول | `GORUN_SETUP_TOKEN` | `setup_token` ؛ يفضل البيئة على معامل يظهر فيقائمة العمليات |
| الوكيل العكسي الموثوق | `GORUN_TRUSTED_PROXIES` | `trusted_proxies` ؛ قائمة عناوين/CIDR محددة |
| مصادر سكربتات إضافية للصفحات العامة | `GORUN_CSP_SCRIPT_SRC` | `csp_script_src` ؛ مصادر https و`'unsafe-eval'` فقط، لقالب مخصص يحتاج CDN |
| طلبات الشبكات الخاصة | `GORUN_ALLOW_PRIVATE_NET` | `--allow-private-net` / `allow_private_net` ؛ معطلة افتراضيًا |
| HSTS للنطاقات الفرعية | `GORUN_HSTS_INCLUDE_SUBDOMAINS` | `hsts_include_subdomains` ؛ معطلة افتراضيًا؛ فعّلها فقط إن كانت كل النطاقات الفرعية تعمل بـHTTPS |
| HSTS preload | `GORUN_HSTS_PRELOAD` | `hsts_preload` ؛ يتطلب الخيار السابق، والتراجع عنه يستغرق أشهرًا |
| موافقة MCP من مفتاح لنفس المالك | `GORUN_MCP_SAME_OWNER_APPROVAL` | `mcp_same_owner_approval` ؛ معطلة افتراضيًا، لموقع بمشغّل واحد فقط |

مثال TOML محلي، دون أسرار، في `data/gorun.toml`:

```toml
env = "dev"
addr = "127.0.0.1:8080"
base_url = "http://127.0.0.1:8080"
theme = "default"
```

يرفض القارئ مفاتيح TOML غير المعروفة. مجلد themes يعتمد على موقع التشغيل إن لم تحدده؛ ضبط GORUN_THEMES لمسار مطلق يزيل هذا الالتباس عند تشغيل خدمة.

## متطلبات تشغيل إنتاجي

- حزمة موثقة مع gorun و themes، ومستخدم نظام محدود، ومجلد بيانات دائم خاص بالموقع. اجعل ملفات الأسرار 0600، ولا تضع data أو backups داخل جذر خدمة الملفات العامة.
- `GORUN_ENV=prod` و `GORUN_BASE_URL` عام صحيح؛ استخدم HTTPS خلف وكيل TLS. المصدر يرفض عنوانًا فارغًا أو loopback في prod، لكنه لا يتحقق وحده من DNS أو الشهادة أو الوصول العام.
- `GORUN_SECRET_KEY` ثابت، Base64 لـ 32 بايت عشوائية، أو secret_key في TOML محمي. في prod لا يعتمد الإعداد على ملف dev/secret.key تلقائيًا عند غياب المفتاح الصريح. خزّن المفتاح الأصلي مستقلًا بطريقة آمنة.
- لإنشاء أول مدير عبر الويب: `GORUN_SETUP_TOKEN` بطول 16 محرفًا على الأقل، ويُرسل في حقل النموذج، لا في URL. دون رمز، يبدأ الخادم إذا استوفت بقية الإعدادات، لكن إنشاء أول مدير عبر النموذج محظور؛ البديل إنشاء المدير من CLI بصلاحية النظام.
- عنوان استماع محدود مثل `127.0.0.1:8080` إذا كان الوكيل على الجهاز نفسه. لا تفتح منفذ التطبيق للعامة بلا حدود شبكة. وثّق عنوان الموقع والمسارات والأسرار في مشغل الخدمة الخاص ببيئتك؛ الدليل لا يثبت وحدة systemd أو Docker أو DNS تلقائيًا.
- اترك trusted proxies فارغة ما لم تستخدم وكيلًا. حين تضبطها، حدّد العنوان أو CIDR الفعلي فقط؛ لا تثق بكل الشبكات الخاصة أو `0.0.0.0/0`.
- افحص `/healthz`، ثم `/api/v1/openapi.json` ورقم الإصدار، ثم تسجيل الدخول والواجهة العامة والوسائط فعليًا. نجاح health لا يثبت جاهزية البريد أو الدفع أو الترحيل.

بعد أن تكون الأسرار الضرورية مضبوطة في بيئة الخدمة الآمنة، هذا شكل تشغيلها؛ العنوان مثال تستبدله بدومينك:

```sh
GORUN_ENV=prod \
GORUN_DATA=/var/lib/gorun/site \
GORUN_THEMES=/opt/gorun/themes \
GORUN_ADDR=127.0.0.1:8080 \
GORUN_BASE_URL=https://your-site.example \
/opt/gorun/gorun serve
```

لا تطبع الأسرار في الأوامر أو السجلات. البريد و AI و Stripe و S3 اختيارية لتثبيت CMS أساسي؛ تحتاج إعدادًا واختبارًا مستقلاً عند استخدام القدرة المرتبطة بها. إعداد البريد من `/admin/settings/mail` يدعم SMTP أو Resend؛ وجود الإعدادات ليس إثبات تسليم. التسجيل المُدار مع تفعيل البريد يتطلب مزودًا جاهزًا، ولا يُعد البريد المفقود نجاحًا.

## الترقية من وسوم الإصدار {#upgrades-from-release-tags--الترقية-من-وسوم-الإصدار}

لا يوجد محدّث تلقائي للنواة. الترقية = تثبيت ثنائي/صورة الإصدار الجديد على **نفس** مجلد البيانات والمفتاح، بعد نسخة احتياطية. **الدليل الكامل (ثنائي · GHCR · Coolify · فحص دخان · رجوع):** [UPGRADE.md](UPGRADE.md).

1. اقرأ قسم الإصدار في [CHANGELOG.md](../CHANGELOG.md) ووسوم [Releases](https://github.com/zadstack/gorun/releases).
2. أنشئ نسخة: `gorun backup --data <data>` واحفظ `secret.key` / `GORUN_SECRET_KEY` و`gorun.toml` وبيئة الخدمة خارج الأرشيف الافتراضي.
3. اختبر على مجلد بيانات مستقل (استعادة النسخة ثم تشغيل الإصدار الجديد عليه) قبل لمس الإنتاج.
4. أوقف العملية القديمة حتى يتحرر `<data>/gorun.lock` (في Docker/Coolify: stop ثم start؛ لا تتداخل نسختان على الـvolume).
5. ثبّت الوسم الجديد:
   - **ثنائي:** فك أرشيف `vX.Y.Z` الجديد، أبقِ `GORUN_DATA` و`GORUN_SECRET_KEY` كما هما، وشغّل `./gorun serve` من مجلد الفك (مع `themes/`).
   - **GHCR:** غيّر الصورة إلى `ghcr.io/zadstack/gorun:X.Y.Z` (تجنّب الاعتماد على `latest` وحدها في الإنتاج)، نفس الـvolume ونفس المتغيرات.
6. `serve` يطبّق migrations قبل فتح HTTP. للتشغيل الصريح (أمر كتابة، ليس dry-run):

```sh
./bin/gorun migrate --data ./data
```

في الإنتاج شغّل أوامر CLI بنفس بيئة الخدمة (`GORUN_ENV` و`GORUN_SECRET_KEY`)؛ `--data` لا يحمّل ملف بيئة الخدمة تلقائيًا.

7. افحص `/healthz`، دخول الأدمن، المحتوى، والوسائط. إن فشل التحديث: أوقف الخدمة، شغّل **ثنائي/صورة الإصدار السابق** على مجلد مُستعاد من النسخة مع المفتاح الأصلي. إرجاع الملف التنفيذي وحده لا يعيد مخطط SQLite إلى ما كان عليه.

توافق القوالب والإضافات جزء من الاختبار على المجلد المستقل. المواقع التي تستخدم slug قالب متقاعد تُرحَّل عند الإقلاع فقط إذا غاب القالب الأصلي؛ راجع [THEMES.md](THEMES.md).

## النسخ الاحتياطي والاستعادة

لإنشاء نسخة محلية لموقعك:

```sh
./bin/gorun backup --data ./data
```

تتكون من `<timestamp>.db` و `<timestamp>.tar.gz` و `<timestamp>.manifest.json` تحت `data/backups`. تشمل الأرشفة media و overrides و extensions و form-uploads. لا تشمل gorun.toml و secret.key إلا مع `gorun backup --include-secrets`؛ احفظهما مستقلًا وانسخهما إلى مجلد الاستعادة. لا تشمل حزمة themes الخارجية أو قيم البيئة الخارجية أو أجسام S3. احتفظ بها مستقلًا مع الإصدار والمفتاح الأصلي.

افحص الزوج المحلي، بعد استبدال اسم manifest بالاسم الذي أُنشئ:

```sh
./bin/gorun backup-inspect --input "./data/backups/REPLACE_WITH_ACTUAL_TIMESTAMP.manifest.json"
```

الفحص لا يُثبت استعادة موقع كاملة أو قراءة نسخة بعيدة؛ قد ينجح ويعرض `review_required`. لقطة SQLite متسقة، لكن DB وأرشيف الملفات ليسا معاملة واحدة؛ أوقف الكتابة حين تحتاج لقطة الموقع الكاملة المتسقة.

الاستعادة لا تُدمج في المجلد الحي، ولها طريقان:

- **دون تشغيل:** أوقف الخدمة، ثم استعد إلى مسار مجلد جديد (يجب ألا يكون موجودًا) خارج مجلد البيانات:

```sh
./bin/gorun backup-restore --input "./data/backups/REPLACE_WITH_ACTUAL_TIMESTAMP.manifest.json" --dest "./data-restored"
```

- **أثناء التشغيل:** من **تشغيل النظام ← النسخ الاحتياطية ← فحص / استعادة**، بعد تأكيد كلمة المرور، تُكتب النسخة تحت `data/restores/<timestamp>/` والموقع يعمل. هذا المجلد داخل مجلد البيانات الحي، فانقله خارجه قبل تشغيل الخدمة عليه، مثلًا `mv ./data/restores/<timestamp> ./data-restored`.

بعدها انسخ `secret.key` و`gorun.toml` إلى مجلد الاستعادة (أو اضبط `GORUN_SECRET_KEY` الأصلي)، وأوقف كل عملية على المجلد القديم، ثم شغّل `gorun serve --data ./data-restored`. التفاصيل في [دليل الإنتاج](PRODUCTION.md). لموقع تجريبي دائم وترقية إلى الحي: `gorun staging seed|promote` و[STAGING.md](STAGING.md).

## حدود ترحيل WordPress في المصدر الحالي

معالج REST من `/admin/migrations` موجود، لكن 1049/نطاق 25–26 لم يدمج إصلاح المسودات المنفصل. القراءة الحالية تضيف context=edit دون طلب statuses صريحة؛ لذلك لا تعتمد على كلمة مرور التطبيق لنقل draft أو pending أو private أو future بالكامل. وقد تتحول بعض أخطاء القراءة إلى fallback، فلا يُعد `validated` أو تقدم 100% إثبات اكتمال كل ما يمكن الوصول إليه في WordPress.

إذا كانت المسودات أو العناصر الخاصة مطلوبة، أوقف ترحيل الإنتاج إلى أن تستخدم نسخة قبلت ذلك فعليًا، أو راجع WXR مستقلًا مع معاينة وربط صور وحالات واضحة. إصلاح REST المعزول `3e2e912` اختُبر على 119 عنصرًا وصلاحيات كاتب محدود، لكنه ليس جزءًا من المصدر الموثق هنا. لا يشمل ذلك ضمان كل دلالات الإضافات أو WooCommerce.

## روابط العمل التالية

بعد التثبيت:

1. بدّل لغة لوحة الإدارة من زر اللغة في الشريط العلوي (عربية ⇄ English)، واضبط لغة الموقع من **الإعدادات ← عام**. التفاصيل في [مساحة الإدارة](ADMIN_WORKSPACE.md).
2. ابنِ أول موقع من **المظهر ← ابنِ موقعك**، أو بدّل القالب من **المظهر ← القوالب** ثم راجع [منشئ المواقع](SITE_BUILDER.md) و[القوالب](THEMES.md).
3. للوكلاء: [MCP](MCP.md). API في `/api/docs` و`/api/v1/openapi.json`.
4. للإنتاج خلف وكيل: [PRODUCTION.md](PRODUCTION.md) و[Coolify](../deploy/coolify.md). أعطال شائعة: [TROUBLESHOOTING.md](TROUBLESHOOTING.md).

إن كان هدفك منصة استضافة تسجّل العملاء وتجهّز لهم مواقع، فهذه طبقة إضافية فوق النواة. تشغيل GoRun CMS وحده لا ينشئ تلك البنية.

---

## English — fresh install and upgrades

Operator path for a new host. Full Arabic detail is above; production hardening is in [PRODUCTION.md](PRODUCTION.md).

### Binary from a release tag

```sh
curl -fLO https://github.com/zadstack/gorun/releases/download/v0.2.0/gorun-0.2.0-linux-amd64.tar.gz
curl -fLO https://github.com/zadstack/gorun/releases/download/v0.2.0/SHA256SUMS-0.2.0.txt
sha256sum --ignore-missing -c SHA256SUMS-0.2.0.txt
tar xzf gorun-0.2.0-linux-amd64.tar.gz && cd gorun-0.2.0-linux-amd64
GORUN_ENV=dev GORUN_DATA=./data GORUN_BASE_URL=http://127.0.0.1:8080 ./gorun serve
```

Open `/admin/setup`. Production needs `GORUN_ENV=prod`, a persistent data dir, HTTPS, `GORUN_BASE_URL`, a stable `GORUN_SECRET_KEY` (32 bytes Base64), and usually `GORUN_SETUP_TOKEN` (≥16 chars) for web bootstrap. Run from the extract directory so `themes/` resolves, or set `GORUN_THEMES`.

### Docker / GHCR

```sh
docker run -d --name gorun -p 8080:8080 -v gorun-data:/data \
  -e GORUN_BASE_URL=https://cms.example.com \
  -e GORUN_SECRET_KEY="$(openssl rand -base64 32)" \
  -e GORUN_SETUP_TOKEN="$(openssl rand -hex 24)" \
  ghcr.io/zadstack/gorun:0.2.0
```

Pin a version tag in production (`0.2.0` or `0.2`). Behind a reverse proxy set
`GORUN_TRUSTED_PROXIES`, then run the HTTPS acceptance checklist (trusted CIDR,
cookie `Secure`, HSTS, health from outside the container):
[deploy/coolify.md §9](../deploy/coolify.md#9-https-acceptance-checklist).
Coolify deploy notes: [deploy/coolify.md](../deploy/coolify.md). Compose:
`deploy/docker-compose.yml`.

### Upgrades from release tags

Full checklist (binary, GHCR, Coolify, smoke, rollback): [UPGRADE.md](UPGRADE.md).

1. Read [CHANGELOG.md](../CHANGELOG.md) for the target tag; `gorun backup`; keep secrets outside the default archive.
2. Stop the old process (release `<data>/gorun.lock`); do not overlap two containers on one volume.
3. Install the new archive or switch the image to `ghcr.io/zadstack/gorun:X.Y.Z` with the **same** data volume and secret key. Migrations run on `serve`.
4. Verify health, admin login, content, media. Rollback = previous binary/image + restored data dir + original key — not binary-only.

### After install

Bilingual admin toggle in the top bar · first site under **Appearance → Build your site** · theme switch under **Appearance → Themes** · staging / promote-to-live: [STAGING.md](STAGING.md) · agents: [MCP.md](MCP.md) · problems: [TROUBLESHOOTING.md](TROUBLESHOOTING.md) · index: [README.md](README.md).
