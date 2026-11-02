#!/bin/sh
# fakeclaude — Claude Code stub for cwm tests and e2e runs (SPEC-V0.2.md,
# testdata/fakeclaude). POSIX sh only; no bashisms, no jq/python/node.
#
#   --version       print ${FAKECLAUDE_VERSION:-2.1.400}
#   auth login      write $CLAUDE_CONFIG_DIR/.credentials.json and .claude.json
#                   (oauthAccount.email from ${FAKECLAUDE_EMAIL:-fake@example.com},
#                   plan "claude_max"); echo done; exit 0
#   anything else   append one JSON line {args,cwd,claude_config_dir,
#                   project_dir_name} to $FAKECLAUDE_CAPTURE (unset -> exit 5)
set -u

json_escape() { # $1 -> stdout, escaped for a JSON string literal
    printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g'
}

if [ "${1:-}" = "--version" ]; then
    printf '%s\n' "${FAKECLAUDE_VERSION:-2.1.400}"
    exit 0
fi

if [ "${1:-}" = "auth" ] && [ "${2:-}" = "login" ]; then
    cfg="${CLAUDE_CONFIG_DIR:-}"
    if [ -z "$cfg" ]; then
        echo "fakeclaude: CLAUDE_CONFIG_DIR not set for auth login" >&2
        exit 5
    fi
    mkdir -p "$cfg"
    printf '{"token":"fake"}\n' > "$cfg/.credentials.json"
    printf '{"oauthAccount":{"email":"%s"},"plan":"claude_max"}\n' \
        "${FAKECLAUDE_EMAIL:-fake@example.com}" > "$cfg/.claude.json"
    echo done
    exit 0
fi

cap="${FAKECLAUDE_CAPTURE:-}"
if [ -z "$cap" ]; then
    echo "fakeclaude: FAKECLAUDE_CAPTURE not set" >&2
    exit 5
fi

args="["
sep=""
for a in "$@"; do
    args="$args$sep\"$(json_escape "$a")\""
    sep=","
done
args="$args]"
printf '{"args":%s,"cwd":"%s","claude_config_dir":"%s","project_dir_name":"%s"}\n' \
    "$args" \
    "$(json_escape "$(pwd)")" \
    "$(json_escape "${CLAUDE_CONFIG_DIR:-}")" \
    "$(json_escape "${CLAUDE_CODE_PROJECT_DIR_NAME:-}")" >> "$cap"
exit 0
