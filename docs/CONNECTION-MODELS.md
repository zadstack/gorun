# Model discovery in Connections

The native Connections editor can explicitly load the selected AI provider's model catalog before a profile is saved. The editable model ID stays intact until a person selects a returned model. Opening or editing the page sends no provider request. Discovery does not generate text, activate a provider, or save connection/test state.

`POST /admin/settings/connections/models` requires a signed-in user with `settings.manage` and native CSRF. Form fields are `id`, `version`, `kind=ai`, `provider`, `endpoint`, `token`, and optional `clear_secret`. New profiles leave ID/version empty. Saved profiles require an exact version before and after the provider request. A blank token retains the saved credential only when provider and normalized endpoint are unchanged; a changed destination requires its credential to be entered explicitly.

Success returns `{ "models": [{ "id": "model-id", "label": "Model label" }], "excluded_count": 0, "profile_id": "…", "version": "…" }`. Errors return a stable `error` code and localized `message`, without provider bodies, keys or transport error details. Responses are private/no-store. Loading a catalog does not prove that a given model supports text generation or the user's planned workload.

The server uses the existing public-network safe HTTP client: HTTPS, DNS/dial protection, no redirects, 20 seconds total, 1 MiB per response, at most 10 pages and 1,000 model records. IDs follow the same grammar as saved model IDs. Unsupported IDs are omitted from the dropdown and explicitly counted in `excluded_count`; the UI displays that count. The 1,000-record bound applies before filtering. Empty usable results, invalid labels, credential reflections, oversized or incomplete catalogs remain hard errors. This is a list of application-supported models, not a claim that every provider identifier is supported. Results are deduplicated and sorted. Anthropic supports `has_more`/`last_id` cursor pagination; arbitrary OpenAI-compatible pagination formats are not followed.

The browser keeps the current model even if it was not returned. Provider, endpoint, credential or profile/version changes clear discovered choices and abort/ignore stale responses without clearing the editable model. All catalog labels are inserted as text, not HTML.

Official protocol references checked for this implementation:
- [OpenAI model list](https://developers.openai.com/api/reference/resources/models/methods/list)
- [Anthropic model list](https://platform.claude.com/docs/en/api/models/list)

Verification uses fake providers and an isolated local native admin fixture. No production credential, provider request, generation or charge is part of those tests.

## العربية

زر «جلب الموديلات المتاحة» يقرأ فهرس المزوّد عند الضغط عليه فقط. يدعم اتصالًا جديدًا قبل الحفظ، ويحافظ على الموديل المكتوب أو المحفوظ. لا يحفظ الاتصال ولا يفعّله ولا يولّد محتوى. المفاتيح تبقى على الخادم؛ تغيير الوجهة يتطلب إدخال مفتاحها صراحةً، والرد القديم يُرفض إذا تغيّر الاتصال أثناء الجلب. القائمة لا تؤكد دعم كل موديل لتوليد النصوص أو لأي مهمة محددة.

المعرّفات التي لا تقبلها صيغة التطبيق تُستبعد مع إظهار عددها صراحةً. تظل أي إعادة للمفتاح أو تسميات غير صالحة أو قائمة تتجاوز الحدود سببًا لرفض الرد؛ حدود العدد تُحسب قبل التصفية.
