# Independent Edge instances and site setup

GoRun Full can manage a registry of independent GoRun Edge sites. Each created instance has a separate Cloudflare Worker, D1 database, private R2 bucket and site-scoped manager credential. This does not clone a Full site or synchronize its content. Edge remains experimental; its manager endpoint confirms identity, not Full feature parity or every runtime operation.

## Dashboard setup

The dashboard starts with the setup checklist, then everyday actions and personal tools. Diagnostics are collapsed further down. Existing saved dashboard order and pins remain personal and are preserved.

Open **Settings → Site setup and next actions** to review identity, available content and menus, optional AI/mail/Cloudflare connections, Edge instances, storage, backup evidence and local health. Actions open the existing settings screens. Reading the dashboard does not contact providers or queue checks. Saved configuration, current identity/connectivity evidence and operation-test evidence are different states; missing, expired and changed-configuration evidence is not a pass. A local backup record does not prove that restore has been tested.

AI and Cloudflare are optional. Store provider credentials in their existing secure settings; the setup checklist does not create another credential store.

## Connect an existing independent Edge

Open **Integrations → Independent Edge instances**. Enter its HTTPS origin, label and primary manager key issued by that Edge owner. The server verifies the site's manager identity before storing a separate encrypted credential for that instance. The key is never returned by the API or rendered into the page, and checks use the credential associated with the selected instance only.

External connections verify an origin and its identity. They cannot assert a Cloudflare account/Worker association by attaching an inventory name. Only a completed creation receipt records those provider resources. The previous single connection under **Settings → Edge manager** is retained without an automatic migration or overwrite.

## Create a new independent Worker

1. Save the Cloudflare API token and select an account under the existing Integrations screen.
2. Open Independent Edge instances and enter a label, Worker prefix, new site ID, title and language. The selected saved account is displayed; change it through Cloudflare settings.
3. **Preview resource plan** checks the account and complete bounded resource inventories without creating provider resources. The server assigns distinct names and pins the embedded Worker artifact. Read access does not prove write permission. The plan lists the required Cloudflare permissions and its expiry.
4. Review the account, Worker, D1 and private R2 resources. Explicitly approve that exact plan. This queues one durable operation; queued is not deployed.
5. Refresh recorded progress. Success is recorded only after provisioning and a manager identity response matching the planned site and generated manager IDs. The instance is then added to the current registry without replacing independent edits to other instances.

Creation requires Account Settings Read, Workers Scripts Write, D1 Write and Workers R2 Storage Write in the selected account. Provider policies, enabled services and quotas can still reject execution. GoRun does not widen the token's permissions.

The new site is empty: no Full database, Full themes, customer data or demo publications are imported. The generated key establishes manager identity. Owner administration is disabled until configured separately through the [Edge owner workflow](EDGE-ADMIN.md); creation does not claim that Full can remotely edit Edge content. No DNS/custom-domain route or public R2 endpoint is created.

## Concurrency and uncertain outcomes

The approval binds the account/token revision, exact resources, site identity, artifact and expiry. Before approval, a changed registry requires another review. Once running, independent registry edits do not invalidate creation; target collisions, capacity and account/token changes remain fenced. Final registry insertion and the verified plan receipt are atomic.

Every provider mutation has a persisted running checkpoint. A timeout, interruption or uncertain result stops at `needs_reconciliation`. GoRun does not blindly repeat the mutation, adopt existing resources or automatically delete anything. Keep the recorded resource IDs and inspect the account before a manual recovery decision. There is no automatic reconciliation/retry action in this version.

Cloudflare's Worker upload does not offer an atomic create-only operation. Unpredictable generated names and an immediate existence check reduce accidental replacement risk, but cannot prevent a simultaneous external actor creating that exact name between the check and upload.

## API and automation

All operations require `settings.manage`. Discover support first with `GET /api/v1/site/edge/instances/capabilities`; older versions return unsupported. A permission error is not an empty registry. API mutations accept JSON and enforce existing authentication and CSRF rules; errors use the standard error envelope.

- `GET /api/v1/site/edge/instances`: public records and an envelope-level revision.
- `POST /api/v1/site/edge/instances`: connect an existing origin with label, write-only key and the current revision.
- `POST /api/v1/site/edge/instances/{id}/check`: explicit identity check with the current revision.
- `GET /api/v1/site/edge/instances/plans` and `.../plans/{id}`: saved progress only.
- `POST /api/v1/site/edge/instances/plans`: preview `{request, registry_revision}`.
- `POST /api/v1/site/edge/instances/plans/{id}/execute`: approve `{approved: true, fingerprint}`; responds 202 when queued.

The OpenAPI document contains the request/response schemas and permissions. Fleet must gate actions on this contract, retain the managing Full site and instance identity, and treat unavailable/forbidden/unsupported states separately.

## Build and validation boundaries

`npm --prefix edge run manager:build` compiles the existing Edge source and regenerates its embedded modules and migrations. `npm --prefix edge run manager:check` detects drift. GoRun verifies the embedded artifact digest before upload; requests cannot supply scripts or a provider API origin.

Tests use real local SQLite migrations and fake HTTP/provider boundaries. They cover independent encrypted credentials, permission failures, stale plans, target collisions, unrelated edits, interrupted stages, secret redaction, API discovery and the compiled Worker's manager-key protocol. This is not evidence of a new deployment on a real Cloudflare account. A real-account acceptance must use an explicitly approved resource plan.

Cloudflare transport contracts: [Worker multipart metadata](https://developers.cloudflare.com/workers/configuration/multipart-upload-metadata/), [D1 database creation](https://developers.cloudflare.com/api/resources/d1/subresources/database/methods/create/), [R2 bucket creation](https://developers.cloudflare.com/api/resources/r2/subresources/buckets/methods/create/).

## العربية

الموقع الكامل يدير نسخ Edge مستقلة، وليس نسخًا من محتواه. لكل نسخة جديدة Worker وقاعدة D1 وحاوية R2 خاصة ومفتاح إدارة منفصل. حفظ مفتاح Cloudflare لا ينشئ موارد تلقائيًا: راجع خطة الأسماء والحساب ثم وافق صراحة. «في الانتظار» ليست «منشور»، والتحقق من الهوية ليس اختبارًا لكل وظائف النسخة. عند نتيجة غير مؤكدة تتوقف العملية للمراجعة دون إعادة إنشاء أو حذف تلقائي. إعداد إدارة المالك يتم منفصلًا، والربط القديم محفوظ.
