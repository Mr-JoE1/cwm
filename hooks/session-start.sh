#!/usr/bin/env bash
# SessionStart hook: remember "this machine worked on repo X at origin Y" in
# repos.tsv so `cl <name>` can clone it on demand on another device.
# Contract: this hook must never fail the session — always exit 0.
set -u
trap 'exit 0' EXIT

cfg="${CLAUDE_CONFIG_DIR:-$HOME/.claude-me}"
logdir="$cfg/logs"
log="$logdir/session-start.log"

logline() {
    mkdir -p "$logdir" 2>/dev/null || true
    printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" >> "$log" 2>/dev/null || true
}

url=$(git remote get-url --push origin 2>/dev/null) || { logline "skip: cwd is not a git repo with an origin remote ($PWD)"; exit 0; }
name=$(basename "$PWD")

tmp="$cfg/repos.tsv.$$"
if ! { cat "$cfg/repos.tsv" 2>/dev/null || true; printf '%s\t%s\n' "$name" "$url"; } > "$tmp" 2>/dev/null; then
    logline "error: cannot write $tmp"
    exit 0
fi
if ! sort -u "$tmp" > "$cfg/repos.tsv" 2>/dev/null; then
    rm -f "$tmp" 2>/dev/null
    logline "error: cannot write $cfg/repos.tsv"
    exit 0
fi
rm -f "$tmp" 2>/dev/null
exit 0
