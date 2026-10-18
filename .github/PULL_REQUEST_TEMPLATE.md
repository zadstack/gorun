## What and why

<!-- One concern per PR. Link the issue or spec section. -->

## How it was tested

<!-- Commands run and what they showed. -->

## Checklist

- [ ] `go build ./...` and `go vet ./...` pass
- [ ] `go test -race ./...` passes (real SQLite, no database mocks)
- [ ] New behaviour has tests written first; bug fixes include a regression test
- [ ] No import of a later module (`kernel.BootOrder`); `Modules()`, `BootOrder` and `go.mod` untouched or called out
- [ ] Inside `db.InTx` only `…Tx` variants; hooks emitted after commit
- [ ] Mutations audited; API handlers use the standard envelope; no secrets in logs or responses
- [ ] Admin strings added in both `ar` and `en` catalogs; RTL checked
- [ ] Docs updated (`docs/API.md`, `docs/MCP.md`, `docs/ARCHITECTURE.md` as relevant) and `CHANGELOG.md` under *Unreleased*
- [ ] Commit messages follow `type(scope): description`
- [ ] Every commit is signed off (`git commit -s`, DCO; see CONTRIBUTING.md)
