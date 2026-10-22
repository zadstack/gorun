# Storage, generated media and encrypted backups

GoRun can activate a saved S3-compatible connection from **Settings → Storage**. Saving a connection or running its read-only bucket test does not activate storage. Encrypted remote backup upload can be manual or explicitly enabled for scheduled snapshots under **System → Backups**; it is off by default.

## Website media

Choose **Use for new media**. New uploads use the selected bucket through GoRun's native `/media/` URLs. Public media keeps its normal publication behavior; an S3 bucket does not need a public CDN endpoint. Existing local files and server-configured S3 files continue using their existing driver.

Every managed destination has an identity derived from its purpose, endpoint, region and bucket. Changing buckets retains previous destination credentials so existing files remain readable. Disabling uploads returns new uploads to the server's default storage and retains previous destinations. Reactivating the same endpoint/region/bucket refreshes its encrypted credentials without changing existing object paths. Deleting a connection profile does not delete its activated destination or files. Revoking old provider credentials before refreshing their destination can make old files unavailable.

Generated uploads use `media.UploadMeta.GeneratedContentID` and `GeneratedKind` (`image` or `video`). GoRun validates an existing non-trash content ID and the detected MIME type, then stores the original and image variants under:

```text
sites/<persistent-site-id>/content/<content-id>/YYYY/MM/DD/<media-id>.<ext>
```

Other managed uploads use `sites/<site-id>/uploads/YYYY/MM/<media-id>.<ext>`. Original local upload paths are unchanged. Normal MIME and upload-size restrictions still apply; MP4 is in the default allowlist. Provider credentials and signed download URLs must not be saved as `SourceURL`.

### Photo metadata

Uploaded originals are public, so GoRun removes camera metadata before storing JPEG, PNG and WebP files: GPS position, device serials, owner and editor names, XMP/IPTC, comments, and JPEG trailers such as HDR gain or depth maps. Stripping is lossless: no pixels are re-encoded, colour profiles (ICC) stay, and a JPEG keeps only its EXIF Orientation so it still displays upright. Variants are generated upright, and the stored width and height are the displayed ones. GIF and other files are stored as uploaded. Set `media.strip_metadata` to `false` to keep originals byte-for-byte; files uploaded earlier are not changed.

Storage destinations allow HTTPS endpoints only. Managed runtime requests use DNS/IP guards, bounded timeouts and no redirects. Media object paths are bound to this site's namespace. Backup destinations and backup key prefixes cannot be served through `/media/`.

## Encrypted backups

Choose **Use for encrypted backups**, then **System → Backups → Create and upload encrypted backup**. This is an explicit manual action. The **Create backup** action remains local-only. The optional schedule can also upload encrypted copies when you explicitly enable its backup-destination checkbox.

GoRun first creates its existing consistent SQLite/database and file archive locally. Each file is encrypted with AES-256-GCM using a purpose-derived key from the site's recovery secret before upload. The format authenticates the original filename and file bytes. Uploaded object names are:

```text
backups/<site-id>/YYYY/MM/DD/<timestamp>.db.gorb
backups/<site-id>/YYYY/MM/DD/<timestamp>.tar.gz.gorb
```

The limit is **64 MiB per plaintext backup file**. This bounded implementation is intended for small sites; it does not implement multipart uploads. A remote failure leaves the new completed local backup pair available; scheduled retention can still remove older verified pairs. Remote objects can be incomplete when only one upload succeeds; only success for both files is recorded as a successful remote backup. **Nothing in the bucket is automatically deleted** — retain-N applies only to local pairs under `<data>/backups/`. Bucket ACL settings are not treated as proof of privacy: uploaded backup bytes are always encrypted.

### Local schedule and retention

On **System → Backups**, choose **Daily** or **Weekly** (existing custom 1–168 hour intervals remain supported; `0` = off) and optional **retain N** locally verified pairs (`0` = never auto-delete). While the server runs with job workers, cron `backup.schedule` queues a single `backup.scheduled` job. That job creates and verifies a local pair (secrets still excluded), optionally uploads it to the explicitly selected backup destination, then prunes older verified pairs. Corrupt, legacy and partial entries do not count toward retention and remain for review. Last success, space usage and failure alerts appear on the page and System Health. See [automatic backups and safe recovery](BACKUPS.md). Manual **Back up now** does not prune. Hosts without a long-running process can use host cron instead:

```sh
gorun backup --data /var/lib/gorun --retain 7
# Coolify / Docker:
docker exec <container> /app/gorun backup --retain 7
```

Keep an independent copy of recovery secrets off the host. Schedule is not off-site continuity.

Keep an independent, protected copy of `secret.key` or `GORUN_SECRET_KEY`. A recovery secret stored only inside the encrypted backup cannot recover itself. Losing that secret makes the remote backups unrecoverable. Normal GoRun backup contents include the database and local files; remote media objects need their own bucket retention/backup policy.

## Recovery

Download both encrypted objects using the bucket's own authenticated tooling. Keep their original `<timestamp>.<extension>.gorb` basenames. With the **original site's recovery secret**, decrypt to new files:

```sh
gorun backup-decrypt --input 20261012T120000Z.db.gorb --output restored.db --data /path/to/recovery-secret-directory
gorun backup-decrypt --input 20261012T120000Z.tar.gz.gorb --output restored.tar.gz --data /path/to/recovery-secret-directory
```

The directory must contain the original `secret.key`, or supply the original `GORUN_SECRET_KEY` through your normal protected environment. Do not put secrets in command-line arguments or logs. The command authenticates the entire encrypted file before creating output, uses mode `0600`, and refuses to overwrite an existing output. Restore the decrypted pair using the normal stopped-site recovery process; the decrypt command does not change any running site.

## العربية

من **الإعدادات ← التخزين** اختر اتصال S3 محفوظًا، ثم فعّله للوسائط الجديدة أو للنسخ الاحتياطية المشفّرة، كل وجهة بصورة مستقلة. اختبار الاتصال لا يفعّل التخزين ولا يرفع ملفات. تظل الملفات القديمة مرتبطة بوجهتها، وتحديث بيانات نفس الحاوية يتطلب إعادة تفعيلها صراحةً.

الصور المرفوعة عامة، لذلك يحذف GoRun بيانات الكاميرا من ملفات JPEG وPNG وWebP قبل حفظها: الموقع الجغرافي (GPS)، والرقم التسلسلي للجهاز، وأسماء المالك والمحرّر، وبيانات XMP/IPTC، والتعليقات، والصور الملحقة بعد نهاية الصورة الأساسية. لا يُعاد ترميز الصورة، ويبقى ملف الألوان (ICC)، وتحتفظ صور JPEG باتجاه العرض فقط. لإبقاء الملف كما هو اضبط `media.strip_metadata` على `false`؛ الملفات المرفوعة سابقًا لا تتغير.

تُرتّب الصور والفيديوهات المولّدة حسب الموقع والمحتوى والتاريخ. تخضع لنفس فحص نوع الملف وحد الحجم الموجودين في مكتبة الوسائط.

يمكن إنشاء النسخ المشفّرة ورفعها يدويًا، أو تفعيل رفع كل نسخة مجدولة صراحةً من شاشة النسخ الاحتياطية، بحد 64 ميبيبايت لكل ملف قبل التشفير. الاحتفاظ N يطبّق على النسخ المحلية فقط؛ لا تُحذف كائنات S3 تلقائيًا. احتفظ بنسخة مستقلة وآمنة من سر الموقع؛ فقدانه يمنع استعادة النسخ المشفّرة. فشل الرفع لا يحذف النسخة المحلية، ولا يعني وجود ملف خارجي واحد اكتمال النسخة. استعادة الملفات بأمر `backup-decrypt` لا تعدّل الموقع الجاري ولا تستبدل ملفًا موجودًا. يمكن أيضًا `gorun backup --retain N` من cron المضيف.

No real bucket credentials are required to build or test these paths. Unit tests use synthetic data and fake S3 responses. A configured destination is not evidence of a successful real-provider upload.

## Scheduled encrypted uploads

After choosing a backup-purpose destination, open **System → Backups**, choose an
interval and enable **Upload each scheduled backup to this destination**, then
save. The default remains local-only. This uses the existing S3-compatible
credentials (including R2); a Cloudflare management API token is not substituted
for bucket credentials.

`backup.schedule.remote_destination` pins the destination ID selected by the
operator. Saving validates it against `storage.active.backup` in the same
transaction as interval/retention updates. A different or disabled destination
pauses uploads until explicitly reselected; local snapshots continue. An upload
already started uses its captured destination and reports that destination ID.
Credential rotation for the same bucket retains its identity.

The hourly cron only enqueues work. One queued/running `backup.scheduled` job is
admitted at a time. Scheduled upload attempts have a 90-second total deadline,
no cloud deletes and no automatic ACL/lifecycle changes. The shared data lease
is renewed before the bounded upload and before local pruning. Jobs use one
attempt. A persisted upload warning completes the job without adding a dead-job
alert; creation/recording failures return a generic error without filesystem or
provider details. An upload warning does not cause another local snapshot on an
immediate queue retry. The next due snapshot attempts upload again. Scheduled execution
still requires a running GoRun Full server with job workers; this is not a
Cloudflare Queues/Workers Cron integration.

A failed/partial upload keeps the new verified local pair. The configured
retain-N policy still applies to older verified pairs (including while a
destination is paused); the new pair reserves one slot even after a host clock
rollback. This prevents an upload outage from creating unlimited extra copies
when retain-N is enabled. `backup.local.last_success` still
reports the locally verified snapshot; `backup.schedule.state` reports a
`warning` at stage `remote`. System Health shows the warning. The per-backup
receipt separates acknowledgement of each file from read-back verification.
`storage.backup.last` is the latest attempt, and `storage.backup.last_success`
retains the latest fully acknowledged pair even after later failures. If both
objects were acknowledged but writing the local receipt fails, the saved state
is `uploaded_unrecorded`; acknowledgement remains recorded, while the missing
receipt is an explicit warning. Losing the data-directory lease after recording
local success similarly produces a `lease` warning and skips cleanup, rather
than relabelling the verified snapshot as failed. Older
versions did not preserve the latter, so it is empty until a new upload succeeds.
A successful upload is not proof of a complete restore or a remote read-back.
