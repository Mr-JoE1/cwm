<!--
Thanks for contributing! Please read CONTRIBUTING.md first.
PR title: a Conventional Commit, e.g. `feat(gui): filter sessions by account` or `fix(installer): ...`
-->

## What & why

<!-- What does this change, and why? Link the issue: "Closes #123". -->

## How it was tested

<!-- Commands you ran, platforms you tried, screenshots for UI changes (both themes if relevant). -->

## Checklist

- [ ] The PR title is a [Conventional Commit](https://www.conventionalcommits.org/) (`type(scope): summary`)
- [ ] [The gate](https://github.com/Mr-JoE1/cwm/blob/main/CONTRIBUTING.md#the-gate) is green locally: `gofmt -l .` empty, `go vet ./...`, `go test ./...`, `go test -tags e2e ./test/`, `GOOS=windows`/`GOOS=darwin` builds + vet, `bash tests/test.sh`
- [ ] Tests added or updated for the change (no network, `t.TempDir()`, `HOME` + `USERPROFILE` isolated)
- [ ] Docs updated (README or the [wiki](https://github.com/Mr-JoE1/cwm/wiki)) if behaviour visible to users changed
- [ ] No new dependencies, or this was discussed in an issue first
- [ ] If this is an architectural, security or public-contract change, a new decision entry was added to `docs/PLAN.md` (next free D-number)
- [ ] No secrets, tokens or personal data in code, tests, screenshots or logs
