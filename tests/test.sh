#!/usr/bin/env bash
# End-to-end test (Linux). Runs entirely inside a mktemp sandbox HOME with a
# stubbed `claude` binary — no network, no real ~/.claude or ~/.claude-me.
# `cl` is tested through the installed legacy-alias wrapper (it must keep
# passing every original assertion); `cwm` and `cwm doctor` through the
# installed canonical launcher.
set -euo pipefail

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

SANDBOX=$(mktemp -d)
STUBBIN=$(mktemp -d)
trap 'rm -rf "$SANDBOX" "$STUBBIN"' EXIT

export HOME="$SANDBOX/home"
mkdir -p "$HOME"
export CLAUDE_CONFIG_DIR="$HOME/.claude-me"
CAPTURE="$SANDBOX/capture.txt"
export CAPTURE
export SSH_STUB_MARKER="$SANDBOX/ssh-marker"

# --- stub claude: records args + env; fakes --version -----------------------
# CLAUDE_STUB_VERSION lets individual tests simulate an outdated claude.
cat > "$STUBBIN/claude" <<EOF
#!/usr/bin/env bash
if [[ "\${1:-}" == "--version" ]]; then
    printf '%s (Claude Code)\n' "\${CLAUDE_STUB_VERSION:-2.1.234}"
    exit 0
fi
{
    printf 'ARGS:'
    printf ' [%s]' "\$@"
    printf '\n'
    printf 'CLAUDE_CONFIG_DIR=%s\n' "\${CLAUDE_CONFIG_DIR:-}"
    printf 'CLAUDE_CODE_PROJECT_DIR_NAME=%s\n' "\${CLAUDE_CODE_PROJECT_DIR_NAME:-}"
    printf 'CWD=%s\n' "\$PWD"
} >> "\$CAPTURE"
exit 0
EOF
chmod +x "$STUBBIN/claude"

# --- stub ssh: must NEVER be called by doctor in default mode ---------------
# WHY a self-reporting stub: doctor is local-only by default (decision D9); if
# anything drags ssh into the default path, this marker file appears and the
# test fails. The stub never touches the network.
cat > "$STUBBIN/ssh" <<'EOF'
#!/usr/bin/env bash
printf 'ssh invoked with: %s\n' "$*" >> "${SSH_STUB_MARKER:?SSH_STUB_MARKER not set}"
exit 255
EOF
chmod +x "$STUBBIN/ssh"

export PATH="$STUBBIN:$PATH"

# hostname sanitizer — mirrors hooks/session-end.sh (test must stay in sync)
HOST=$(hostname 2>/dev/null || true)
HOST=${HOST%%.*}
HOST=$(printf '%s' "$HOST" | tr -c 'A-Za-z0-9-' '-')
while [[ "$HOST" == -* ]]; do HOST=${HOST#-}; done
while [[ "$HOST" == *- ]]; do HOST=${HOST%-}; done
HOST=${HOST:-unknown}

# --- assertion helpers -------------------------------------------------------
PASS=0
FAIL=0
ok()  { PASS=$((PASS + 1)); printf '  ok   - %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf '  FAIL - %s\n' "$1"; }
assert_contains() { # desc haystack needle
    if [[ "$2" == *"$3"* ]]; then ok "$1"; else bad "$1 — expected '$3' in: ${2:0:300}"; fi
}
assert_file_contains() { # desc file needle
    if [[ -f "$2" ]] && grep -qF -- "$3" "$2"; then ok "$1"; else bad "$1 — '$3' not found in ${2:-<missing>}"; fi
}
assert_file_line() { # desc file exact-line
    if [[ -f "$2" ]] && grep -qxF -- "$3" "$2"; then ok "$1"; else bad "$1 — exact line '$3' not found in ${2:-<missing>}"; fi
}

echo "== 1. bootstrap =="
if bash "$REPO/bootstrap.sh" > "$SANDBOX/bootstrap.out" 2>&1; then ok "bootstrap exits 0"; else bad "bootstrap exits 0 — see $SANDBOX/bootstrap.out"; fi
[[ -d "$HOME/.claude-me" ]] && ok ".claude-me created" || bad ".claude-me created"
assert_file_contains "settings.json contains sandbox home path" "$HOME/.claude-me/settings.json" "$HOME"
assert_file_contains "settings.json wires session-start hook" "$HOME/.claude-me/settings.json" "session-start.sh"
assert_file_contains "settings.json wires session-end hook" "$HOME/.claude-me/settings.json" "session-end.sh"
[[ -x "$HOME/.claude-me/hooks/session-start.sh" ]] && ok "session-start hook installed +x" || bad "session-start hook installed +x"
[[ -x "$HOME/.claude-me/hooks/session-end.sh" ]] && ok "session-end hook installed +x" || bad "session-end hook installed +x"
[[ -x "$HOME/bin/cwm" ]] && ok "cwm installed at ~/bin/cwm (+x)" || bad "cwm installed at ~/bin/cwm (+x)"
[[ -x "$HOME/bin/cl" ]] && ok "cl alias installed at ~/bin/cl (+x)" || bad "cl alias installed at ~/bin/cl (+x)"
[[ -x "$HOME/bin/cwm-doctor" ]] && ok "doctor installed at ~/bin/cwm-doctor (+x)" || bad "doctor installed at ~/bin/cwm-doctor (+x)"
assert_file_contains "repo bin/cl is a thin wrapper around cwm" "$REPO/bin/cl" 'exec "$(dirname "$0")/cwm" "$@"'
assert_file_line ".bashrc persists CLAUDE_CONFIG_DIR" "$HOME/.bashrc" 'export CLAUDE_CONFIG_DIR="$HOME/.claude-me"'
assert_file_contains ".profile persists CLAUDE_CONFIG_DIR" "$HOME/.profile" 'export CLAUDE_CONFIG_DIR'
assert_file_contains ".stignore installed" "$HOME/.claude-me/.stignore" "settings.json"
if bash "$REPO/bootstrap.sh" >> "$SANDBOX/bootstrap.out" 2>&1; then ok "bootstrap is idempotent (re-run exits 0)"; else bad "bootstrap is idempotent (re-run exits 0)"; fi
assert_file_contains "settings.json not clobbered on re-run" "$SANDBOX/bootstrap.out" "keeping it"

echo "== 2. repo fixture + session-start hook =="
WORK="$SANDBOX/work"
mkdir -p "$WORK"
ALPHA="$WORK/alpha"
BARE="$WORK/origin.git"
git init -q "$ALPHA"
git -C "$ALPHA" symbolic-ref HEAD refs/heads/main
git init -q --bare "$BARE"
git -C "$BARE" symbolic-ref HEAD refs/heads/main
git -C "$ALPHA" remote add origin "$BARE"
echo "hello" > "$ALPHA/file.txt"
git -C "$ALPHA" add -A
git -C "$ALPHA" -c user.name=tester -c user.email=tester@test commit -q -m "initial"
git -C "$ALPHA" push -q origin main
echo "dirty wip" > "$ALPHA/wip.txt"

if (cd "$WORK" && bash "$HOME/.claude-me/hooks/session-start.sh"); then ok "session-start exits 0 outside a repo"; else bad "session-start exits 0 outside a repo"; fi
(cd "$ALPHA" && bash "$HOME/.claude-me/hooks/session-start.sh")
assert_file_contains "repos.tsv records alpha<TAB>origin" "$HOME/.claude-me/repos.tsv" "$(printf 'alpha\t%s' "$BARE")"
(cd "$ALPHA" && bash "$HOME/.claude-me/hooks/session-start.sh")
[[ "$(wc -l < "$HOME/.claude-me/repos.tsv")" -eq 1 ]] && ok "repos.tsv deduped on re-run" || bad "repos.tsv deduped on re-run"

echo "== 3. session-end hook (WIP auto-push) =="
if (cd "$ALPHA" && bash "$HOME/.claude-me/hooks/session-end.sh"); then ok "session-end exits 0 (dirty)"; else bad "session-end exits 0 (dirty)"; fi
if git --git-dir="$BARE" rev-parse --verify --quiet "refs/heads/sync/$HOST" >/dev/null; then
    ok "bare origin has branch sync/$HOST"
else
    bad "bare origin has branch sync/$HOST (host=$HOST; branches: $(git --git-dir="$BARE" for-each-ref --format='%(refname)' | tr '\n' ' '))"
fi
[[ "$(git --git-dir="$BARE" show "sync/$HOST:wip.txt" 2>/dev/null || true)" == "dirty wip" ]] \
    && ok "WIP file present in sync/$HOST tree" || bad "WIP file present in sync/$HOST tree"
if (cd "$ALPHA" && bash "$HOME/.claude-me/hooks/session-end.sh"); then ok "session-end exits 0 on clean tree (no-op)"; else bad "session-end exits 0 on clean tree (no-op)"; fi

echo "== 4. cl <name> (wrapper → cwm): clone on demand + resume =="
rm -rf "$HOME/repos/alpha"
rm -f "$CAPTURE"
if (cd "$SANDBOX" && bash "$HOME/bin/cl" alpha) > "$SANDBOX/cl.out" 2>&1; then ok "cl alpha exits 0"; else bad "cl alpha exits 0 — $(cat "$SANDBOX/cl.out")"; fi
[[ -d "$HOME/repos/alpha/.git" ]] && ok "repo cloned into ~/repos/alpha" || bad "repo cloned into ~/repos/alpha"
assert_file_contains "stub claude invoked with --resume" "$CAPTURE" "[--resume]"
assert_file_line "CLAUDE_CODE_PROJECT_DIR_NAME pinned to alpha" "$CAPTURE" "CLAUDE_CODE_PROJECT_DIR_NAME=alpha"
assert_file_line "CLAUDE_CONFIG_DIR exported to claude" "$CAPTURE" "CLAUDE_CONFIG_DIR=$CLAUDE_CONFIG_DIR"

echo "== 5. cl <name> last (wrapper → cwm) =="
rm -f "$CAPTURE"
if (cd "$SANDBOX" && bash "$HOME/bin/cl" alpha last) > "$SANDBOX/cl-last.out" 2>&1; then ok "cl alpha last exits 0"; else bad "cl alpha last exits 0"; fi
assert_file_contains "stub claude invoked with --continue" "$CAPTURE" "[--continue]"

echo "== 6. cl (no args): list projects (wrapper → cwm) =="
mkdir -p "$HOME/.claude-me/projects/alpha"
printf '{"type":"stub"}\n' > "$HOME/.claude-me/projects/alpha/20261102T000000-fake.jsonl"
out=$(bash "$HOME/bin/cl")
assert_contains "listing shows alpha" "$out" "alpha"
assert_contains "listing shows session count" "$out" "1"
assert_contains "usage printed" "$out" "usage:"

echo "== 7. cl unknown: helpful error (wrapper → cwm) =="
out=$(bash "$HOME/bin/cl" nope 2>&1) && bad "cl unknown exits non-zero" || ok "cl unknown exits non-zero"
assert_contains "error mentions unknown name" "$out" "nope"
assert_contains "error lists known names" "$out" "alpha"

echo "== 8. cl fails hard without workspace (wrapper → cwm) =="
mkdir -p "$SANDBOX/empty-home"
if out=$(env -u CLAUDE_CONFIG_DIR HOME="$SANDBOX/empty-home" bash "$HOME/bin/cl" 2>&1); then
    bad "cl errors when ~/.claude-me missing"
else
    ok "cl errors when ~/.claude-me missing"
    assert_contains "error tells user to bootstrap/pair" "$out" "bootstrap"
fi

echo "== 9. pass-through pinning (wrapper → cwm) =="
rm -f "$CAPTURE"
if (cd "$HOME/repos/alpha" && bash "$HOME/bin/cl" --resume abc123); then ok "pass-through inside ~/repos exits 0"; else bad "pass-through inside ~/repos exits 0"; fi
assert_file_contains "args passed through" "$CAPTURE" "[--resume]"
assert_file_contains "session id passed through" "$CAPTURE" "[abc123]"
assert_file_line "pin set from repo dir basename" "$CAPTURE" "CLAUDE_CODE_PROJECT_DIR_NAME=alpha"
rm -f "$CAPTURE"
if (cd "$SANDBOX" && bash "$HOME/bin/cl" --resume abc123); then ok "pass-through outside ~/repos exits 0"; else bad "pass-through outside ~/repos exits 0"; fi
assert_file_line "no pin outside ~/repos" "$CAPTURE" "CLAUDE_CODE_PROJECT_DIR_NAME="
assert_file_line "CLAUDE_CONFIG_DIR still set outside ~/repos" "$CAPTURE" "CLAUDE_CONFIG_DIR=$CLAUDE_CONFIG_DIR"

echo "== 10. cwm (no args): list projects =="
out=$(bash "$HOME/bin/cwm")
assert_contains "cwm listing shows alpha" "$out" "alpha"
assert_contains "cwm usage printed" "$out" "usage:"
assert_contains "cwm usage documents doctor" "$out" "doctor"
assert_contains "cwm usage notes the cl legacy alias" "$out" "legacy alias"

echo "== 11. cwm <name>: clone on demand + resume =="
rm -rf "$HOME/repos/alpha"
rm -f "$CAPTURE"
if (cd "$SANDBOX" && bash "$HOME/bin/cwm" alpha) > "$SANDBOX/cwm.out" 2>&1; then ok "cwm alpha exits 0"; else bad "cwm alpha exits 0 — $(cat "$SANDBOX/cwm.out")"; fi
[[ -d "$HOME/repos/alpha/.git" ]] && ok "cwm cloned repo into ~/repos/alpha" || bad "cwm cloned repo into ~/repos/alpha"
assert_file_contains "stub claude invoked with --resume via cwm" "$CAPTURE" "[--resume]"
assert_file_line "cwm pins CLAUDE_CODE_PROJECT_DIR_NAME to alpha" "$CAPTURE" "CLAUDE_CODE_PROJECT_DIR_NAME=alpha"

echo "== 12. cwm doctor: missing workspace → exit 1 + FAIL =="
if out=$(env -u CLAUDE_CONFIG_DIR HOME="$SANDBOX/empty-home" bash "$HOME/bin/cwm" doctor 2>&1); then
    bad "cwm doctor exits 1 when workspace is missing"
else
    ok "cwm doctor exits 1 when workspace is missing"
fi
assert_contains "doctor prints a FAIL line" "$out" "FAIL:"
assert_contains "doctor FAIL names the workspace dir" "$out" ".claude-me"
assert_contains "doctor prints summary line" "$out" "doctor:"

echo "== 13. cwm doctor: healthy sandbox → exit 0, ok summary, no FAIL =="
if out=$(bash "$HOME/bin/cwm" doctor 2>&1); then ok "cwm doctor exits 0 on healthy workspace"; else bad "cwm doctor exits 0 on healthy workspace — $out"; fi
assert_contains "doctor prints ok lines" "$out" "ok:"
assert_contains "doctor reports workspace present" "$out" "workspace dir exists"
if grep -q '^FAIL:' <<<"$out"; then
    bad "doctor has no FAIL lines on healthy workspace — $(grep '^FAIL:' <<<"$out")"
else
    ok "doctor has no FAIL lines on healthy workspace"
fi
if [[ "$out" =~ doctor:\ [0-9]+\ ok,\ [0-9]+\ warn,\ [0-9]+\ fail ]]; then
    ok "doctor summary line format: 'doctor: N ok, M warn, K fail'"
else
    bad "doctor summary line format: 'doctor: N ok, M warn, K fail' — got: $(grep '^doctor:' <<<"$out")"
fi

echo "== 14. doctor warns on legacy dash-prefixed session folders =="
mkdir -p "$HOME/.claude-me/projects/-Users-x-legacy"
printf '{"type":"stub"}\n' > "$HOME/.claude-me/projects/-Users-x-legacy/20261102T000001-fake.jsonl"
if out=$(bash "$HOME/bin/cwm" doctor 2>&1); then ok "doctor still exits 0 when only warning"; else bad "doctor still exits 0 when only warning — $out"; fi
assert_contains "doctor mentions the legacy folder" "$out" "-Users-x-legacy"
assert_contains "doctor explains the plain-claude cause" "$out" "plain claude"
rm -rf "$HOME/.claude-me/projects/-Users-x-legacy"

echo "== 15. doctor warns on old claude version =="
if out=$(CLAUDE_STUB_VERSION=2.1.100 bash "$HOME/bin/cwm" doctor 2>&1); then ok "doctor exits 0 on old version (warn only)"; else bad "doctor exits 0 on old version (warn only) — $out"; fi
assert_contains "doctor warns about claude 2.1.100" "$out" "warn: claude 2.1.100"
assert_contains "doctor warn mentions required 2.1.234" "$out" "2.1.234"

echo "== 16. doctor never touches the network in default mode =="
if [[ -f "$SSH_STUB_MARKER" ]]; then
    bad "doctor never invokes ssh in default mode — ssh stub was called: $(cat "$SSH_STUB_MARKER")"
else
    ok "doctor never invokes ssh in default mode"
fi
rm -f "$SSH_STUB_MARKER"
if out=$(bash "$HOME/bin/cwm" doctor --net 2>&1); then ok "doctor --net exits 0 (probe stubbed)"; else bad "doctor --net exits 0 (probe stubbed) — $out"; fi
assert_contains "doctor --net runs the ssh probe and warns on auth failure" "$out" "warn:"
if [[ -f "$SSH_STUB_MARKER" ]]; then
    ok "doctor --net did invoke the ssh stub"
else
    bad "doctor --net did invoke the ssh stub"
fi

echo "== 17. cwm doctor without doctor script → clear error, exit 1 =="
mv "$HOME/bin/cwm-doctor" "$HOME/bin/cwm-doctor.hidden"
if out=$(bash "$HOME/bin/cwm" doctor 2>&1); then
    bad "cwm doctor errors when the doctor script is missing"
else
    ok "cwm doctor errors when the doctor script is missing"
    assert_contains "error explains how to fix" "$out" "bootstrap"
fi
mv "$HOME/bin/cwm-doctor.hidden" "$HOME/bin/cwm-doctor"

echo ""
echo "passed: $PASS  failed: $FAIL"
if [[ "$FAIL" -gt 0 ]]; then
    echo "TESTS FAILED"
    exit 1
fi
echo "ALL TESTS PASSED"
