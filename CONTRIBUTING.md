# Contributing to cwm

Thanks for helping! Bug reports, docs fixes, ideas and code are all welcome.
This page is the short version. The full guide lives in the wiki's
**[Developer Guide](https://github.com/Mr-JoE1/cwm/wiki/Developer-Guide)**
([Contributing](https://github.com/Mr-JoE1/cwm/wiki/Contributing) ·
[Building & Testing](https://github.com/Mr-JoE1/cwm/wiki/Building-and-Testing) ·
[Architecture](https://github.com/Mr-JoE1/cwm/wiki/Architecture) ·
[Decision Log](https://github.com/Mr-JoE1/cwm/wiki/Decision-Log)).

By taking part you agree to the [Code of Conduct](CODE_OF_CONDUCT.md). Report
security problems privately ([SECURITY.md](SECURITY.md)), never in public issues.

## Before you start

- **Bugs:** use the [bug report form](https://github.com/Mr-JoE1/cwm/issues/new?template=bug_report.yml)
  and attach `cwm version` and a redacted `cwm doctor --json`
  ([how to redact](https://github.com/Mr-JoE1/cwm/wiki/Reporting-Issues)).
- **Features:** check the [Roadmap](https://github.com/Mr-JoE1/cwm/wiki/Roadmap), then use the
  [feature request form](https://github.com/Mr-JoE1/cwm/issues/new?template=feature_request.yml).
- **Bigger changes** (a new command, a dependency, the sync layout, the `/api`
  contract): open an issue to discuss it before writing code.
- Want an easy way in? Look for
  [`good first issue`](https://github.com/Mr-JoE1/cwm/labels/good%20first%20issue).

## Quick dev setup

You need **Go 1.25+**, **git** and **bash**. `shellcheck`, `python3` and `pwsh`
are only needed for the installer checks.

```sh
git clone https://github.com/Mr-JoE1/cwm.git && cd cwm
go build -o cwm ./cmd/cwm

# try it without touching your real setup
tmp=$(mktemp -d)
HOME=$tmp USERPROFILE=$tmp ./cwm init
HOME=$tmp USERPROFILE=$tmp ./cwm gui
```

## The gate

CI runs this on `ubuntu-latest` and `windows-latest`. Run it before you push:

```sh
gofmt -l .                                   # must print nothing
go vet ./...
go test ./...
go test -tags e2e ./test/                    # real binary, fake claude, local git only
GOOS=windows GOARCH=amd64 go build ./... && GOOS=linux GOARCH=arm64 go build ./...
GOOS=darwin GOARCH=arm64 go build ./... && GOOS=darwin GOARCH=amd64 go build ./...
GOOS=windows go vet ./... && GOOS=darwin go vet ./...
bash tests/test.sh                           # legacy v0.1 bash kit, must stay green

# only when you touch scripts/install.*
shellcheck -s sh scripts/install.sh && shellcheck scripts/test-install.sh
bash scripts/test-install.sh
```

## Rules in brief

- **No new dependencies** without an issue and a decision first (the module
  uses only `github.com/BurntSushi/toml`).
- Library code doesn't panic. Wrap errors with `fmt.Errorf("…: %w", err)`.
- Tests: no network, `t.TempDir()` only, set **both** `HOME` and `USERPROFILE`,
  fake every OS call (no real `systemctl`, `schtasks`, `launchctl`, terminal or
  `claude`). Windows-only skips use an explicit `t.Skip("reason")` (D22).
- Security-sensitive code keeps the existing model: argv arrays (never shell
  strings), strict allowlists, loopback by default, no secrets in logs.
- Frontend (`internal/gui/web`): no external references, no inline
  scripts/styles, no `innerHTML`/`eval`, 150 KB budget (enforced by a test).

## Pull request checklist

- [ ] Branch from `main` (`feat/…`, `fix/…`, `docs/…`); one logical change per commit
- [ ] Title and commits follow [Conventional Commits](https://www.conventionalcommits.org/): `type(scope): summary`
- [ ] The gate above is green locally; CI is green on ubuntu and windows
- [ ] Tests added or updated
- [ ] User-visible change? Docs updated (README or the [wiki](https://github.com/Mr-JoE1/cwm/wiki))
- [ ] Architecture, security or public-contract change? New entry in [docs/PLAN.md](docs/PLAN.md) (next free D-number)
- [ ] No secrets, tokens or personal data in code, tests, screenshots or logs

Thanks again for contributing!
