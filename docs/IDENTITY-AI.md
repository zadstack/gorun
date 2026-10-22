# AI identity suggestions (optional module)

`identityai` is an optional module (`kernel.OptionalModule`) that asks a configured **text** AI connection for identity suggestions and writes them into the **Appearance → Identity draft**. It never writes the live identity and never calls a provider without a reviewed request and an explicit approval. This first stage produces **text only** (palette, fonts, taglines, a written logo brief); it does not generate an image, vector or animation and the UI says so. Logo generation and optional animation through a provider with a verified image capability, with the same cost gating, are follow-up work and are not claimed here.

## Turning it on

The module is off until the setting `addons.identityai.enabled` is `true` (JSON boolean). Set it from the settings CLI or the System → Modules catalog when that lands; the kernel evaluates the gate before any route or navigation is registered, so a disabled module leaves no page, no nav entry and no data. Turning it off keeps whatever drafts it produced; they are ordinary identity drafts.

## What it does

Page: **Appearance → Identity AI** (`/admin/appearance/identity/ai`, permission `settings.manage`).

1. **Brief.** Choose a saved AI connection (Settings → Connections, kind `ai`; each option shows provider, model and the last recorded test status, or "untested") or the site's writing settings, and describe the sector, audience, tone, what must stay and which tagline languages you want. A theme whose `theme.json` does not declare `identity` is refused up front, because colours and fonts would never be shown.
2. **Preview request.** The page shows the provider, model and host the request really goes to (the saved endpoint, or the writing settings' provider and model), the exact system and user prompt (the site name, current tagline, theme name and current accent are included; no content bodies, keys or other settings), and the **estimated cost**: about N input tokens and at most 1 500 output tokens (the call is capped to that) at the provider's public per-million prices copied on the date shown (`PricesAsOf` in `internal/identityai/pricing.go`). Prices match exact model ids or ids with a dated suffix; any other id gets no estimate and says so. Nothing is sent.
3. **Approve and generate.** Tick the approval box and press Generate. The reviewed request is owner-bound, single-use and expires after ten minutes; a reused or expired token is refused (409) without a provider call.
4. **Result.** The reply must be one JSON object with hex colours, fonts from the identity font lists, corners, spacing, Arabic and English taglines and a written logo brief. It is validated by the theme's own identity rules (colours, known fonts, text/background contrast) when merged into the **draft**; anything invalid is rejected and the draft is left untouched. One tagline goes into `site.tagline` (the chosen language, Arabic when both were requested); the other tagline and the logo brief are shown marked **not saved** so they can be copied. The page then links to Appearance → Identity, where the live preview shows the draft and Apply or Discard decide.

Credentials stay in `internal/admin`: the module receives `Complete`, `Ready` and `Connections` hooks at boot (`admin.IdentityAIHooks`) and never reads a key. Failures from the provider are logged without provider text and shown as a generic stage failure. Every call that reached the provider is audited as `identityai.generate` with provider, model, connection id, a short request fingerprint and the outcome (`ok`, `upstream`, `bad_reply`, `invalid`, `store_failed`).

## What it cannot do

- Produce a logo file, an SVG, an icon set or an animation in this stage. The "logo brief" is text for a human designer.
- Verify that a provider or model is suitable; the connection's last test status is shown as recorded, nothing is probed from this page.
- Guarantee the estimate: prices are copied from public lists on a fixed date and an account may be billed differently.

## Tests

`internal/identityai/module_test.go` boots the kernel with fake hooks (no network): disabled module → no page, no nav; enabled → page in EN and AR with the scope statement; no ready connection → explanation and no call; preview shows prompt and cost and makes no call; generate without approval refused; approved generate → one call, draft saved, live keys unchanged, audit row, token single-use; provider error, non-JSON, invalid colour and unknown font each leave an existing draft untouched and show a safe message; known and unknown models in the price table.

## العربية

وحدة اختيارية تقترح هوية (ألوان، خطوط، وصف مختصر، وموجز شعار مكتوب) عبر اتصال ذكاء اصطناعي نصي مُعدّ مسبقًا، وتحفظ النتيجة في **مسودة** الهوية فقط. لا تُنتج صورًا ولا رسومًا متجهة ولا GIF، ولا تستدعي المزوّد إلا بعد معاينة الطلب الحرفي والموافقة على تكلفته التقديرية. تُفعَّل بالإعداد `addons.identityai.enabled`.
