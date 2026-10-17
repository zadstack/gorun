# Contributing to GoRun

**العربية** · [English](#english)

<div dir="rtl" lang="ar">

## المساهمة

- **الإعداد:** Go 1.27، ثم من جذر المستودع: `make build` و`bin/gorun serve` (البيانات في `./data`).
  لا Node ولا npm ولا خطوة بناء للواجهة: القوالب Go وTailwind وHTMX وAlpine من CDN.
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

</div>

---

<a id="english"></a>

## Dev setup

Go 1.27. From the repository root:

```sh
make build              # bin/gorun, CGO_ENABLED=0
bin/gorun serve       # http://localhost:8080, data in ./data, first admin at /admin/setup
```

There is no Node, npm or frontend build: admin and themes are Go templates with Tailwind, HTMX, Alpine and
Lucide from CDNs. Commands that boot GoRun need the themes directory (`./themes`, or `GORUN_THEMES`).

## Make targets

| Target | Runs |
|---|---|
| `make build` | `CGO_ENABLED=0 go build -trimpath` → `bin/gorun`, version from `git describe` |
| `make test` | `go test ./...` |
| `make run` | `go run ./cmd/gorun serve` |
| `make lint` | `go vet ./...`, then `staticcheck ./...` when installed |
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

## License of contributions

GoRun is licensed under the GNU Affero General Public License v3.0.

**Outside code contributions are not accepted yet.** The project is still deciding on its contribution
agreement. Until it is published here, please open issues for bugs, security reports (see `SECURITY.md`),
documentation problems and feature ideas instead of pull requests; pull requests from outside the project
will be closed without review. This section will say when code contributions open and on what terms.
