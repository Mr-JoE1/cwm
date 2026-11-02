#!/usr/bin/env bash
# cwm doctor — local health checks for the cwm workspace.
# WHY local-only by default: doctor must stay fast and testable offline; the
# SSH probe is opt-in via --net (decision D9 in docs/PLAN.md).
set -euo pipefail

CFG="$HOME/.claude-me"
MIN_VERSION="2.1.234"
NET=0
if [[ "${1:-}" == "--net" ]]; then
    NET=1
elif [[ $# -gt 0 ]]; then
    echo "usage: cwm doctor [--net]" >&2
    exit 1
fi

OK_COUNT=0
WARN_COUNT=0
FAIL_COUNT=0
ok()   { OK_COUNT=$((OK_COUNT + 1));   printf 'ok:   %s\n' "$*"; }
warn() { WARN_COUNT=$((WARN_COUNT + 1)); printf 'warn: %s\n' "$*"; }
fail() { FAIL_COUNT=$((FAIL_COUNT + 1)); printf 'FAIL: %s\n' "$*"; }

# 1. workspace dir
if [[ -d "$CFG" ]]; then
    ok "workspace dir exists ($CFG)"
else
    fail "workspace dir missing ($CFG) — run the cwm bootstrap or pair Syncthing so the folder is restored"
fi

# 2. settings.json: present, hooks wired, wired hooks exist + are executable.
#    WHY grep for keys instead of "valid JSON": no jq/python allowed; the
#    "hooks" + "SessionStart" keys are what actually matter for wiring.
settings="$CFG/settings.json"
hook_paths() { # settings file → unique hook command paths, one per line
    grep -oE '"[^"]*/hooks/[^"]*\.sh"' "$1" 2>/dev/null | tr -d '"' | sort -u || true
}
if [[ ! -f "$settings" ]]; then
    warn "settings.json missing — hooks not wired (re-run the cwm bootstrap)"
else
    if grep -q '"hooks"' "$settings" && grep -q '"SessionStart"' "$settings"; then
        ok "settings.json wires hooks (\"hooks\" + \"SessionStart\" keys present)"
    else
        warn "settings.json lacks \"hooks\"/\"SessionStart\" keys — hooks not wired"
    fi
    wired=0
    while IFS= read -r hp; do
        [[ -n "$hp" ]] || continue
        wired=1
        if [[ ! -f "$hp" ]]; then
            fail "hook wired but missing: $hp — re-run the cwm bootstrap"
        elif [[ ! -x "$hp" ]]; then
            warn "hook not executable: $hp (chmod +x)"
        fi
    done < <(hook_paths "$settings")
    if [[ "$wired" -eq 1 ]]; then
        ok "wired hook scripts exist and are executable"
    fi
fi

# 3. hook scripts installed
if [[ -d "$CFG/hooks" ]] && [[ -n "$(find "$CFG/hooks" -maxdepth 1 -type f 2>/dev/null)" ]]; then
    n=$(find "$CFG/hooks" -maxdepth 1 -type f | wc -l | tr -d ' ')
    ok "hook scripts present in $CFG/hooks ($n file(s))"
else
    warn "no hook scripts in $CFG/hooks — re-run the cwm bootstrap"
fi

# 4. sync ignore policy
if [[ -f "$CFG/.stignore" ]]; then
    ok ".stignore present (sync ignore policy installed)"
else
    warn ".stignore missing — sync not configured (re-run the cwm bootstrap)"
fi

# 5. claude presence + version (pinning needs >= 2.1.234)
if command -v claude >/dev/null 2>&1; then
    ver=$(claude --version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -n1 || true)
    if [[ -z "${ver:-}" ]]; then
        ok "claude on PATH (version not parseable)"
    elif [[ "$ver" == "$MIN_VERSION" ]] || [[ "$(printf '%s\n%s\n' "$ver" "$MIN_VERSION" | sort -V | tail -n1)" == "$ver" ]]; then
        ok "claude $ver on PATH (>= $MIN_VERSION: project-name pinning supported)"
    else
        warn "claude $ver < $MIN_VERSION — project-name pinning unsupported; upgrade to >= $MIN_VERSION"
    fi
else
    warn "claude not on PATH — install Claude Code"
fi

# 6. CLAUDE_CONFIG_DIR persisted in a shell profile
if grep -qs 'CLAUDE_CONFIG_DIR' "$HOME/.bashrc" || grep -qs 'CLAUDE_CONFIG_DIR' "$HOME/.profile"; then
    ok "CLAUDE_CONFIG_DIR persisted in ~/.bashrc or ~/.profile"
else
    warn "CLAUDE_CONFIG_DIR not exported in ~/.bashrc or ~/.profile — re-run the cwm bootstrap"
fi

# 7. repos.tsv: readable, every non-empty line is name<TAB>url
tsv="$CFG/repos.tsv"
if [[ -r "$tsv" ]]; then
    line_re=$'^[^\t]+\t[^\t]+$'
    total=0
    bad=0
    while IFS= read -r line || [[ -n "$line" ]]; do
        [[ -z "$line" ]] && continue
        total=$((total + 1))
        [[ "$line" =~ $line_re ]] || bad=$((bad + 1))
    done < "$tsv"
    if [[ "$bad" -gt 0 ]]; then
        warn "repos.tsv has $bad malformed line(s) (expected: name<TAB>url)"
    else
        ok "repos.tsv readable, $total repo(s) recorded"
    fi
else
    warn "repos.tsv not readable — no repos recorded yet (open a repo via cwm to create it)"
fi

# 8. session-folder hygiene: dash-prefixed dirs are OS path slugs from
#    unpinned `claude` launches; their sessions will not resume cross-OS.
projects="$CFG/projects"
if [[ -d "$projects" ]]; then
    legacy=""
    while IFS= read -r d; do
        [[ -n "$d" ]] || continue
        legacy="$legacy ${d##*/}"
    done < <(find "$projects" -maxdepth 1 -mindepth 1 -type d -name '-*' 2>/dev/null || true)
    if [[ -n "$legacy" ]]; then
        warn "unpinned session folders (resume via cwm to keep sessions cross-OS; these were created by plain claude):$legacy"
    else
        ok "no unpinned (dash-prefixed) session folders"
    fi
else
    ok "no session folders yet (~/.claude-me/projects absent — created on first launch)"
fi

# 9. WIP log tail
wiplog="$CFG/logs/wip.log"
if [[ -f "$wiplog" ]]; then
    ok "WIP log tail (last 3 lines of logs/wip.log):"
    tail -n 3 "$wiplog" | sed 's/^/      /'
else
    ok "no WIP log yet (no sync/<host> pushes recorded)"
fi

# 10. --net only: SSH probe. Never runs in default mode (D9).
if [[ "$NET" -eq 1 ]]; then
    sshout=$(ssh -o BatchMode=yes -o ConnectTimeout=5 -T git@github.com 2>&1 || true)
    if [[ "$sshout" == *"successfully authenticated"* ]]; then
        ok "ssh git@github.com: successfully authenticated"
    else
        warn "ssh git@github.com failed — check your SSH key ($(printf '%s' "$sshout" | head -n 1))"
    fi
fi

printf 'doctor: %d ok, %d warn, %d fail\n' "$OK_COUNT" "$WARN_COUNT" "$FAIL_COUNT"
if [[ "$FAIL_COUNT" -gt 0 ]]; then
    exit 1
fi
exit 0
