#!/usr/bin/env bash
# End-to-end test (Linux). Runs entirely inside a mktemp sandbox HOME with a
# stubbed `claude` binary — no network, no real ~/.claude or ~/.claude-me.
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

# --- stub claude: records args + env; fakes --version -----------------------
cat > "$STUBBIN/claude" <<EOF
#!/usr/bin/env bash
if [[ "\${1:-}" == "--version" ]]; then
    printf '2.1.234 (Claude Code)\n'
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
[[ -x "$HOME/bin/cl" ]] && ok "launcher installed at ~/bin/cl (+x)" || bad "launcher installed at ~/bin/cl (+x)"
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

echo "== 4. cl <name>: clone on demand + resume =="
rm -rf "$HOME/repos/alpha"
rm -f "$CAPTURE"
if (cd "$SANDBOX" && bash "$HOME/bin/cl" alpha) > "$SANDBOX/cl.out" 2>&1; then ok "cl alpha exits 0"; else bad "cl alpha exits 0 — $(cat "$SANDBOX/cl.out")"; fi
[[ -d "$HOME/repos/alpha/.git" ]] && ok "repo cloned into ~/repos/alpha" || bad "repo cloned into ~/repos/alpha"
assert_file_contains "stub claude invoked with --resume" "$CAPTURE" "[--resume]"
assert_file_line "CLAUDE_CODE_PROJECT_DIR_NAME pinned to alpha" "$CAPTURE" "CLAUDE_CODE_PROJECT_DIR_NAME=alpha"
assert_file_line "CLAUDE_CONFIG_DIR exported to claude" "$CAPTURE" "CLAUDE_CONFIG_DIR=$CLAUDE_CONFIG_DIR"

echo "== 5. cl <name> last =="
rm -f "$CAPTURE"
if (cd "$SANDBOX" && bash "$HOME/bin/cl" alpha last) > "$SANDBOX/cl-last.out" 2>&1; then ok "cl alpha last exits 0"; else bad "cl alpha last exits 0"; fi
assert_file_contains "stub claude invoked with --continue" "$CAPTURE" "[--continue]"

echo "== 6. cl (no args): list projects =="
mkdir -p "$HOME/.claude-me/projects/alpha"
printf '{"type":"stub"}\n' > "$HOME/.claude-me/projects/alpha/20261102T000000-fake.jsonl"
out=$(bash "$HOME/bin/cl")
assert_contains "listing shows alpha" "$out" "alpha"
assert_contains "listing shows session count" "$out" "1"
assert_contains "usage printed" "$out" "usage:"

echo "== 7. cl unknown: helpful error =="
out=$(bash "$HOME/bin/cl" nope 2>&1) && bad "cl unknown exits non-zero" || ok "cl unknown exits non-zero"
assert_contains "error mentions unknown name" "$out" "nope"
assert_contains "error lists known names" "$out" "alpha"

echo "== 8. cl fails hard without workspace =="
mkdir -p "$SANDBOX/empty-home"
if out=$(env -u CLAUDE_CONFIG_DIR HOME="$SANDBOX/empty-home" bash "$HOME/bin/cl" 2>&1); then
    bad "cl errors when ~/.claude-me missing"
else
    ok "cl errors when ~/.claude-me missing"
    assert_contains "error tells user to bootstrap/pair" "$out" "bootstrap"
fi

echo "== 9. pass-through pinning =="
rm -f "$CAPTURE"
if (cd "$HOME/repos/alpha" && bash "$HOME/bin/cl" --resume abc123); then ok "pass-through inside ~/repos exits 0"; else bad "pass-through inside ~/repos exits 0"; fi
assert_file_contains "args passed through" "$CAPTURE" "[--resume]"
assert_file_contains "session id passed through" "$CAPTURE" "[abc123]"
assert_file_line "pin set from repo dir basename" "$CAPTURE" "CLAUDE_CODE_PROJECT_DIR_NAME=alpha"
rm -f "$CAPTURE"
if (cd "$SANDBOX" && bash "$HOME/bin/cl" --resume abc123); then ok "pass-through outside ~/repos exits 0"; else bad "pass-through outside ~/repos exits 0"; fi
assert_file_line "no pin outside ~/repos" "$CAPTURE" "CLAUDE_CODE_PROJECT_DIR_NAME="
assert_file_line "CLAUDE_CONFIG_DIR still set outside ~/repos" "$CAPTURE" "CLAUDE_CONFIG_DIR=$CLAUDE_CONFIG_DIR"

echo ""
echo "passed: $PASS  failed: $FAIL"
if [[ "$FAIL" -gt 0 ]]; then
    echo "TESTS FAILED"
    exit 1
fi
echo "ALL TESTS PASSED"
