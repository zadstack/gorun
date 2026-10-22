# Automatic local backups and safe recovery

## Set a schedule

Open **Operations → Backups** and choose **Daily (every 24 hours)** or **Weekly
(every 7 days)**. Choose how many verified local copies to retain (1–100), or
`0` to retain all. Scheduling remains disabled until you save a nonzero interval.
Previously configured custom intervals of 1–168 hours remain supported.

The policy is saved atomically. It runs only while the server's job workers are
running, including `gorun start` and normal `gorun serve`. The first due backup
runs on the next scheduler check; this cron is checked hourly (the first worker
scheduler tick after process startup is roughly 30 seconds). Subsequent intervals
are measured from the last verified scheduled success, not local midnight. The
last successful time survives restarts. A failed run remains due and retries on
the next hourly check. Stopping the server also stops its in-app schedule.

For a host scheduler instead:

```sh
gorun backup --data /var/lib/gorun --retain 7
```

The local **Back up now**, scheduler and CLI paths verify the resulting local pair before
reporting success. CLI `--retain` and scheduled retention count only pairs that
pass a fresh local inspection. Manual **Back up now** does not prune or reset the
scheduled interval. The two settings use their existing keys:
`backup.schedule_hours` and `backup.retain`.

## What gets verified and retained

A local backup has a consistent SQLite snapshot, an archive of local media,
overrides, extensions and form uploads, and a manifest binding both files by
SHA-256 and size. Verification checks both digests, SQLite integrity and foreign
keys, archive safety and gzip completeness. Restore repeats these checks and
only publishes a destination after extraction and final digest verification.

A failed verification does not advance the successful time or delete earlier
copies. Retention freshly checks existing pairs, keeps the newest N verified
pairs, and deletes only older verified pairs. Incomplete, legacy and corrupt
entries are left for inspection and do **not** displace healthy copies. This can
leave more than N entries on disk; review them deliberately. Verification may
read large archives, so allow time and I/O capacity for the scheduled run.

The Backups page shows backup-entry count, total regular-file bytes under the
local backup directory, filesystem free space where supported, and the last
recorded locally verified success. That timestamp is a history record, not a
claim that files have not changed or been removed since then.

## Failure alerts

A scheduled attempt records running/success/failure state, timestamp, stage and
backup stamp under `backup.schedule.state`. The Backups page displays a persistent
alert for creation, verification or recording failures. Retention failures are
shown separately: the new snapshot was verified, but cleanup needs attention.
The next scheduled backup will try cleanup again. The System Health `backups`
check also reports failures, cleanup warnings, a missing first scheduled success,
or an overdue verified run. Raw errors and credentials are not displayed.

These are **in-app alerts**, not email or push notifications. Worker logs and
audit entries provide operational evidence. A database-write failure may also
prevent the alert state from being recorded; investigate worker logs and disk
permissions. `backup.local.last_success` records the last verified admin/manual
or scheduled copy. Creation is audited as `system.backup` or
`system.backup_scheduled`; failures use `system.backup_failed`,
`system.backup_schedule_failed` and `system.backup_retention_failed`.

## Preview before cutover

From a backup's **Inspect / restore** page, copy the displayed command, or run:

```sh
gorun backup-preview --input /var/lib/gorun/backups/STAMP.manifest.json
# Headless / a chosen local port:
gorun backup-preview --input /var/lib/gorun/backups/STAMP.manifest.json \
  --addr 127.0.0.1:9091 --no-browser
```

The command verifies the original pair again and restores a **disposable new
copy** into an owner-only temporary directory. It ignores archived configuration,
original secrets and environment credentials, generates an in-memory preview
secret, removes sessions/API keys/magic links and pending jobs from the copy,
strips integrations/storage destinations from its settings, and rewrites the
base URL to the local preview.

A fixed set of built-in public-content modules renders the preview. No downstream
module options, extension code, commerce services, API/MCP, cron or job workers
are started. There is no account access. It accepts only loopback connections,
validates Host/port, rejects writes and privileged paths, and discards credentials
and response cookies. Browser scripts, forms, frames, connections and external
resources are disabled. External redirects/links, refresh metadata and scripts
are removed; public-page navigation and local static assets still work.
Responses larger than 16 MiB are unavailable in this bounded preview.

Only the disposable copy may be sanitized or migrated. The source backup and
live site remain unchanged. The browser opens automatically unless
`--no-browser` is set. Stop with **Ctrl+C** for a graceful shutdown and deletion
of the disposable tree. Forced termination or a machine crash may leave an
owner-only `gorun-backup-preview-*` directory under the operating system's temp
folder; remove that leftover deliberately after checking no preview uses it.

This is a public-content preview, not proof of complete disaster recovery.
Commerce/plugin-only pages, JavaScript interactions, protected content,
encrypted settings and remotely stored media may be absent. Custom themes must
be installed separately in the matching version (`GORUN_THEMES` / extra roots);
the bundled default can be provisioned when no theme directory exists.

## Restore into a new directory

The preview does **not** replace your live data. When satisfied:

```sh
gorun backup-inspect --input /var/lib/gorun/backups/STAMP.manifest.json
gorun backup-restore --input /var/lib/gorun/backups/STAMP.manifest.json \
  --dest /var/lib/gorun-restored
```

The destination must not exist. Online admin restore similarly confines output
to `<data>/restores/STAMP/`, stages it in `.STAMP.partial`, and refuses existing
or interrupted destinations. It requires backup permission, CSRF and recent
password confirmation. The live database is never overwritten or merged.

For final cutover, move an admin-restored directory outside the live data tree,
review all recovery requirements, stop every process using the old site, and
point the service's data directory at the new location. Preserve the old data
folder for rollback. This release does not perform automatic live cutover.

**Keep independently:** the original `secret.key` / `GORUN_SECRET_KEY`, deployment
configuration, matching theme bundles, environment values and external objects
(such as S3 media). Default backups omit `secret.key` and `gorun.toml`; the explicit
CLI `--include-secrets` option stores them in plaintext and requires protected
offline handling. Database/file capture is not one atomic filesystem snapshot:
pause content/media writes for a fully consistent recovery point.

Copies on the same disk are not off-site protection. Encrypted S3/R2
uploads can be manual or explicitly enabled for the schedule. A failed scheduled
upload keeps the new verified local pair; configured local retention still
applies to older pairs. Cloud objects are never
automatically deleted.
See [storage](STORAGE.md), [production recovery](PRODUCTION.md) and
[staging/cutover](STAGING.md).

## بالعربية

من **تشغيل النظام ← النسخ الاحتياطية** اختر يوميًا أو أسبوعيًا وحدد عدد النسخ
السليمة التي تريد الاحتفاظ بها. يعرض النظام آخر نجاح والمساحة، وينبّه داخل لوحة
النسخ وحالة النظام عند الفشل. النسخة الجديدة لا تحذف نسخة سليمة قديمة إلا بعد
اجتياز الفحص؛ النسخ التالفة أو الجزئية تُترك للمراجعة. يمكنك تفعيل رفع النسخ
المجدولة المشفّرة إلى وجهة الباك أب صراحةً؛ عند فشل الرفع تبقى النسخة الجديدة
مع تطبيق عدد الاحتفاظ المحدد على النسخ الأقدم، ويظهر تحذير مستقل عن نجاح الفحص المحلي.

قبل نقل الموقع للعمل، استخدم `gorun backup-preview --input …` لمعاينة نسخة
مؤقتة على localhost دون مهام أو تكاملات أو دخول للحسابات أو تشغيل إضافات.
أوقفها عبر Ctrl+C. الاستعادة الدائمة تظل إلى مجلد جديد، ولا تستبدل البيانات
الحالية تلقائيًا. احتفظ بالمفتاح الأصلي والثيم والملفات الخارجية مستقلًا؛
المعاينة ليست إثباتًا لاكتمال كل متطلبات الاستعادة.
