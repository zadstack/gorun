# Storage, generated media and encrypted backups

GoRun can activate a saved S3-compatible connection from **Settings → Storage**. Saving a connection or running its read-only bucket test does not activate storage. No scheduled backup is enabled by this feature.

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

Choose **Use for encrypted backups**, then **System → Backups → Create and upload encrypted backup**. This is an explicit manual action. The existing **Create backup** action remains local-only.

GoRun first creates its existing consistent SQLite/database and file archive locally. Each file is encrypted with AES-256-GCM using a purpose-derived key from the site's recovery secret before upload. The format authenticates the original filename and file bytes. Uploaded object names are:

```text
backups/<site-id>/YYYY/MM/DD/<timestamp>.db.gorb
backups/<site-id>/YYYY/MM/DD/<timestamp>.tar.gz.gorb
```

The limit is **64 MiB per plaintext backup file**. This bounded implementation is intended for small sites; it does not implement multipart uploads. A remote failure leaves completed local backup files available. Remote objects can be incomplete when only one upload succeeds; only success for both files is recorded as a successful remote backup. Nothing is automatically deleted or scheduled. Bucket ACL settings are not treated as proof of privacy: uploaded backup bytes are always encrypted.

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

النسخ المشفّرة تُنشأ وتُرفع يدويًا من شاشة النسخ الاحتياطية، بحد 64 ميبيبايت لكل ملف قبل التشفير. لا توجد جدولة تلقائية. احتفظ بنسخة مستقلة وآمنة من سر الموقع؛ فقدانه يمنع استعادة النسخ المشفّرة. فشل الرفع لا يحذف النسخة المحلية، ولا يعني وجود ملف خارجي واحد اكتمال النسخة. استعادة الملفات بأمر `backup-decrypt` لا تعدّل الموقع الجاري ولا تستبدل ملفًا موجودًا.

No real bucket credentials are required to build or test these paths. Unit tests use synthetic data and fake S3 responses. A configured destination is not evidence of a successful real-provider upload.
