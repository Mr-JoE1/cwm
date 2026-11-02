#!/usr/bin/env bash
# cwm — Claude Workspace Manager installer (Linux). Idempotent: safe to re-run anytime.
set -euo pipefail

SRC=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
CLAUDE_ME="$HOME/.claude-me"
MIN_VERSION="2.1.234"

say() { printf '%s\n' "$*"; }

# 1. claude presence + version (warn only, never abort)
if command -v claude >/dev/null 2>&1; then
    ver=$(claude --version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -n1 || true)
    if [[ -n "${ver:-}" ]]; then
        if [[ "$ver" != "$MIN_VERSION" && "$(printf '%s\n%s\n' "$ver" "$MIN_VERSION" | sort -V | tail -n1)" == "$MIN_VERSION" ]]; then
            say "warning: claude $ver < $MIN_VERSION — CLAUDE_CODE_PROJECT_DIR_NAME pinning needs >= $MIN_VERSION"
        else
            say "claude $ver ok"
        fi
    fi
else
    say "warning: 'claude' not on PATH (install Claude Code, then re-run bootstrap)"
fi

# 2. directory skeleton
mkdir -p "$CLAUDE_ME/hooks" "$CLAUDE_ME/logs" "$HOME/repos" "$HOME/bin"

# 3. settings.json is machine-specific: generate once, never clobber
if [[ -f "$CLAUDE_ME/settings.json" ]]; then
    say "settings.json exists — keeping it (hint: diff with $SRC/templates/settings.linux.json.tmpl)"
else
    # escape sed-special chars in HOME (& and \) before substitution
    esc=${HOME//\\/\\\\}
    esc=${esc//&/\\&}
    sed "s|{{HOME}}|$esc|g" "$SRC/templates/settings.linux.json.tmpl" > "$CLAUDE_ME/settings.json"
    say "generated $CLAUDE_ME/settings.json"
fi

# 4. hooks are ours: overwrite with the repo versions
cp -f "$SRC/hooks/session-start.sh" "$SRC/hooks/session-end.sh" "$CLAUDE_ME/hooks/"
chmod +x "$CLAUDE_ME/hooks/session-start.sh" "$CLAUDE_ME/hooks/session-end.sh"

# 5. stignore: overwrite (policy is centralized in the kit)
cp -f "$SRC/templates/stignore.tmpl" "$CLAUDE_ME/.stignore"

# 6. launcher (cwm canonical, cl legacy alias, cwm-doctor for `cwm doctor`)
cp -f "$SRC/bin/cwm" "$HOME/bin/cwm"
chmod +x "$HOME/bin/cwm"
cp -f "$SRC/bin/cl" "$HOME/bin/cl"
chmod +x "$HOME/bin/cl"
cp -f "$SRC/bin/doctor.sh" "$HOME/bin/cwm-doctor"
chmod +x "$HOME/bin/cwm-doctor"

# 7. persist env (guarded, no duplicates)
ensure_line() { # file line
    local file="$1" line="$2"
    touch "$file"
    grep -qsxF "$line" "$file" || printf '%s\n' "$line" >> "$file"
}
CFG_LINE='export CLAUDE_CONFIG_DIR="$HOME/.claude-me"'
PATH_LINE='export PATH="$HOME/bin:$PATH"'
ensure_line "$HOME/.bashrc" "$CFG_LINE"
ensure_line "$HOME/.profile" "$CFG_LINE"
ensure_line "$HOME/.bashrc" "$PATH_LINE"
ensure_line "$HOME/.profile" "$PATH_LINE"

# 8. next steps
say ""
say "cwm bootstrap done."
say "  workspace : $CLAUDE_ME"
say "  launcher  : $HOME/bin/cwm  (cl stays as a legacy alias)"
say "  doctor    : $HOME/bin/cwm-doctor  (run 'cwm doctor' for health checks)"
say "  open a new shell, or: export PATH=\"\$HOME/bin:\$PATH\""
say "next steps:"
say "  1. pair Syncthing on the ~/.claude-me folder with your other device"
say "     (Linux: add folder ~/'claude-me'; Windows: %USERPROFILE%\\.claude-me)"
say "  2. run the cwm bootstrap on the other device, then accept the pairing"
say "  3. 'cwm' lists projects; 'cwm <name>' clones on demand and resumes"
