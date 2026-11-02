#!/usr/bin/env bash
# SessionEnd hook: WIP auto-push. If the tree is dirty, commit everything and
# push to sync/<hostname> on origin so the other device can pick up in-flight
# work. Contract: never fails the session — always exit 0, errors go to the log.
set -u
trap 'exit 0' EXIT

cfg="${CLAUDE_CONFIG_DIR:-$HOME/.claude-me}"
logdir="$cfg/logs"
log="$logdir/wip.log"

logline() {
    mkdir -p "$logdir" 2>/dev/null || true
    printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" >> "$log" 2>/dev/null || true
}

sanitize_host() {
    local h
    h=$(hostname 2>/dev/null || true)
    h=${h%%.*}
    h=$(printf '%s' "$h" | tr -c 'A-Za-z0-9-' '-')
    while [[ "$h" == -* ]]; do h=${h#-}; done
    while [[ "$h" == *- ]]; do h=${h%-}; done
    printf '%s' "${h:-unknown}"
}

url=$(git remote get-url --push origin 2>/dev/null) || { logline "skip: cwd is not a git repo with an origin remote ($PWD)"; exit 0; }
[[ -n "$(git status --porcelain 2>/dev/null)" ]] || { logline "skip: tree is clean ($PWD)"; exit 0; }

host=$(sanitize_host)
stamp=$(date -u +%Y-%m-%dT%H:%M:%SZ)

# -c identity overrides so commits never fail on unconfigured machines
git add -A >>"$log" 2>&1 || { logline "error: git add failed ($PWD)"; exit 0; }
if ! git -c user.name="claude-sync" -c user.email="claude-sync@localhost" \
        commit -m "wip: $host $stamp" --no-verify >>"$log" 2>&1; then
    logline "error: commit failed ($PWD)"
    exit 0
fi
if ! git push origin "HEAD:refs/heads/sync/$host" >>"$log" 2>&1; then
    logline "error: push failed for sync/$host (non-force; WIP commit left local)"
    exit 0
fi
logline "ok: pushed HEAD to sync/$host ($url)"
exit 0
