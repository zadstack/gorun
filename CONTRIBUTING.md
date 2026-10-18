# Contributing to GoRun

**العربية** · [English](#english)

<div dir="rtl" lang="ar">

## المساهمة

- **الإعداد:** Go 1.27، ثم من جذر المستودع: `make build` و`bin/gorun serve` (البيانات في `./data`).
  لا يحتاج التطبيق Node ولا npm: القوالب Go مع HTMX وAlpine ومكتبات محلية. CSS الخاص بـTailwind في القالب الافتراضي
  مبني مسبقًا ومحفوظ في المستودع؛ إن غيّرت أصناف Tailwind فيه أو في Go فشغّل `make css` (أداة Tailwind المستقلة بنسخة
  مثبتة ومتحقق من بصمتها) واحفظ الناتج، ويتحقق CI من أنه محدّث.
- **أوامر make:** `make build` (ملف ثابت في `bin/gorun`)، `make test` (`go test ./...`)، `make run`،
  `make lint` (`go vet` ثم staticcheck إن وُجد).
- **الاختبارات:** شغّل `go test -race ./...` قبل كل طلب دمج (هذا ما يشغّله CI). الاختبارات تستخدم SQLite حقيقية
  في مجلد مؤقت، لا قواعد وهمية. اكتب الاختبار أولًا عند إصلاح خطأ أو إضافة سلوك.
- **ترتيب الوحدات:** لا تستورد وحدةٌ إلا وحدات تسبقها في `kernel.BootOrder`، ويفرض ذلك
  `internal/kernel/importorder_test.go`. قائمة `Modules()` و`BootOrder` و`go.mod` يعدّلها المنسّق فقط.
  التفاصيل في [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md#adding-a-module).
- **صيغة الرسائل:** `type(scope): وصف` مثل `fix(commerce): …`، والأنواع: `feat` `fix` `docs` `test` `perf`
  `refactor` `style` `chore` `ci`.
- **اختبارات المتصفح (e2e):** طريقة تشغيلها في [docs/E2E.md](docs/E2E.md).
- **المراجعة:** طلب دمج صغير ومركّز، مع الاختبارات وتحديث الوثائق، وقائمة التحقق في قالب طلب الدمج.
- **الترخيص والتوقيع (DCO):** نقبل المساهمات الخارجية بترخيص المشروع نفسه (`AGPL-3.0-only`)، دون اتفاقية CLA. يجب أن
  يحمل كل commit في طلب الدمج سطر `Signed-off-by: الاسم <البريد>` وفق
  [شهادة منشأ المطوّر DCO 1.1](https://developercertificate.org/)، وبه تُقرّ بأن لك حق تقديم هذا العمل بترخيص
  المشروع. أضفه بـ`git commit -s`، ويجب أن يطابق البريد مؤلف الـcommit؛ وللـcommits الموجودة شغّل
  `git rebase --signoff main` ثم ادفع الفرع من جديد. يتحقق سير العمل `dco` من ذلك تلقائيًا، ويتخطى commits
  حسابات البوت مثل Dependabot. النص الكامل للشهادة في القسم الإنجليزي أدناه. أبلغ عن الثغرات بشكل خاص كما في
  [SECURITY.md](SECURITY.md).

</div>

---

<a id="english"></a>

## Dev setup

Go 1.27. From the repository root:

```sh
make build              # bin/gorun, CGO_ENABLED=0
bin/gorun serve       # http://localhost:8080, data in ./data, first admin at /admin/setup
```

The app needs no Node or npm: admin and themes are Go templates with HTMX, Alpine and Lucide served locally.
The default theme's Tailwind CSS is precompiled and committed; after changing utility classes in the default or
docs theme or in Go markup, run `make css` (pinned standalone CLI, checksum verified) and commit
`themes/default/assets/tailwind.css`. CI checks that it is up to date. Commands that boot GoRun need the themes directory (`./themes`, or `GORUN_THEMES`).

## Make targets

| Target | Runs |
|---|---|
| `make build` | `CGO_ENABLED=0 go build -trimpath` → `bin/gorun`, version from `git describe` |
| `make test` | `go test ./...` |
| `make run` | `go run ./cmd/gorun serve` |
| `make lint` | `go vet ./...`, then `staticcheck ./...` when installed |
| `make css` / `make css-check` | rebuild / verify `themes/default/assets/tailwind.css` (`tools/tailwind/build.sh`) |
| `make e2e` | builds a temporary instance and runs the Playwright browser suite |

## Tests

- Run **`go test -race ./...`** before every pull request. CI (`.github/workflows/ci.yml`) runs a CGO-disabled
  build, `go vet`, `go test -race ./...` and `govulncheck`.
- Tests use **real SQLite** in a temporary directory (`t.TempDir()`, `apitest.Boot`, `kernel.Boot`), not mocks of
  the database. Fakes exist only for external systems (`commerce/fakebilling`, Stripe, mail, recorded WordPress
  fixtures under `internal/wpmigrate/testdata`).
- Fixing a bug or adding behaviour: write the failing test first.
- Set `kernel.LogOutput = io.Discard` in a package's `TestMain` to keep test output readable.

## Module ownership and boot order

- A module may import only modules **earlier** in `kernel.BootOrder` (`internal/kernel/order.go`).
  `internal/kernel/importorder_test.go` enforces it for non-test files.
- Reach later or sibling modules through `kernel.Get[T](d, name)` services and hooks, never by import.
- `cmd/gorun/modules.go` (`Modules()`), `BootOrder` and `go.mod` are edited only by the maintainer
  coordinating a change; say so in your pull request if you need them changed.
- Inside `db.InTx` use only the `…Tx` variants (`audit.RecordTx`, `jobs.EnqueueTx`, `settings.SetTx`); emit hooks
  after commit.
- The checklist for a new module is in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md#adding-a-module).

## Commit format

`type(scope): description`, imperative and specific, e.g. `fix(commerce): refuse a mismatching session`.
Types: `feat`, `fix`, `docs`, `test`, `perf`, `refactor`, `style`, `chore`, `ci`. The scope is the module or area
(`content`, `mcp`, `wpmigrate`, `admin`, `kernel`…).

## End-to-end tests

Browser journeys (Playwright, run against the built binary) are described in [docs/E2E.md](docs/E2E.md),
including how to start the fixture servers and run the suite.

## Review expectations

- Small, focused pull requests: one concern per PR.
- Tests for every behaviour change, green under `-race`.
- Docs updated in the same PR: `docs/API.md` for new operations, `docs/MCP.md` for new tools,
  `docs/ARCHITECTURE.md` for new modules, hooks, jobs or permissions, `CHANGELOG.md` under *Unreleased*.
- Every mutation audited; API handlers return the standard envelope; no secrets in logs or responses.
- Admin UI follows the existing templates (Arabic and English strings in the module catalog, RTL checked).
- A reviewer reads the diff against the spec or issue, runs the tests, and checks the items in the pull request
  template.

## License of contributions and DCO sign-off

GoRun is licensed under the GNU Affero General Public License, version 3 only (`AGPL-3.0-only`), and contributions are accepted under the
same license. There is no CLA. Instead, every commit in a pull request must be signed off under the
[Developer Certificate of Origin 1.1](https://developercertificate.org/): by adding the line

```text
Signed-off-by: Your Name <you@example.com>
```

you certify the statements below for that commit. `git commit -s` adds the line from your `user.name` and
`user.email`; the email must match the commit author. To sign off commits already on your branch, run
`git rebase --signoff main` and push again.

The `dco` workflow (`.github/workflows/dco.yml`, script `.github/scripts/dco-check.sh`) checks every non-merge
commit of a pull request. Commits authored by GitHub bot accounts such as Dependabot are skipped.

```text
Developer Certificate of Origin
Version 1.1

Copyright (C) 2004, 2006 The Linux Foundation and its contributors.

Everyone is permitted to copy and distribute verbatim copies of this
license document, but changing it is not allowed.


Developer's Certificate of Origin 1.1

By making a contribution to this project, I certify that:

(a) The contribution was created in whole or in part by me and I
    have the right to submit it under the open source license
    indicated in the file; or

(b) The contribution is based upon previous work that, to the best
    of my knowledge, is covered under an appropriate open source
    license and I have the right under that license to submit that
    work with modifications, whether created in whole or in part
    by me, under the same open source license (unless I am
    permitted to submit under a different license), as indicated
    in the file; or

(c) The contribution was provided directly to me by some other
    person who certified (a), (b) or (c) and I have not modified
    it.

(d) I understand and agree that this project and the contribution
    are public and that a record of the contribution (including all
    personal information I submit with it, including my sign-off) is
    maintained indefinitely and may be redistributed consistent with
    this project or the open source license(s) involved.
```

Report security issues privately as described in [SECURITY.md](SECURITY.md), not in a pull request.
