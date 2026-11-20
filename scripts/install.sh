#!/bin/sh
# cwm installer for Linux and macOS (SPEC-V0.6 G, D37; look: SPEC-V0.6.1, D51).
#
#   curl -fsSL https://mr-joe1.github.io/cwm/install.sh | sh
#   curl -fsSL https://mr-joe1.github.io/cwm/install.sh | sh -s -- --version v0.6.1 --no-service
#
# Downloads the release archive for this OS/arch, verifies its SHA-256
# against the release's checksums.txt (aborts on any mismatch), installs
# cwm per user (~/.local/bin, no sudo), puts it on PATH, runs `cwm init`
# when needed, installs the login autostart and opens the dashboard.
#
# Flags: --version X   install release X (default: latest)
#        --no-service  skip `cwm service install`
#        --no-open     do not open the dashboard at the end
#        --uninstall   remove cwm, its autostart and PATH lines (keeps ~/.cwm)
# Env:   CWM_VERSION, CWM_INSTALL_DIR (default ~/.local/bin),
#        NO_COLOR (no colours), CWM_INSTALL_FANCY=1 (colours + glyphs even
#        when stdout is not a terminal — test seam),
#        CWM_INSTALL_API / CWM_INSTALL_BASE_URL (test seams: GitHub API and
#        release download base).
#
# The whole script is one function called on the last line, so a truncated
# download never runs half an installer.

set -eu

REPO="Mr-JoE1/cwm"
MARKER="# added by cwm installer"
PAGES="https://mr-joe1.github.io/cwm"

# --- presentation -----------------------------------------------------------
# Fancy (colours + Unicode glyphs) only on a terminal; plain ASCII text when
# piped or logged. NO_COLOR drops the colours but keeps the layout.

ui_init() {
    FANCY=0
    if [ -t 1 ] || [ "${CWM_INSTALL_FANCY:-}" = 1 ]; then
        FANCY=1
    fi
    [ "${TERM:-}" = dumb ] && [ "${CWM_INSTALL_FANCY:-}" != 1 ] && FANCY=0
    C_RESET='' C_BOLD='' C_DIM='' C_GREEN='' C_YELLOW='' C_RED='' C_CYAN=''
    if [ "$FANCY" = 1 ] && [ -z "${NO_COLOR:-}" ]; then
        esc=$(printf '\033')
        C_RESET="${esc}[0m" C_BOLD="${esc}[1m" C_DIM="${esc}[2m"
        C_GREEN="${esc}[32m" C_YELLOW="${esc}[33m" C_RED="${esc}[31m" C_CYAN="${esc}[36m"
    fi
    # Unicode glyphs need a UTF-8 locale as well as a terminal.
    loc="${LC_ALL:-${LC_CTYPE:-${LANG:-}}}"
    case "$loc" in
        *[Uu][Tt][Ff]-8* | *[Uu][Tt][Ff]8*) utf=1 ;;
        *) utf=0 ;;
    esac
    if [ "$FANCY" = 1 ] && [ "$utf" = 1 ]; then
        G_OK='✓' G_WARN='⚠' G_FAIL='✗' G_BAR='│' G_DOTS='…'
        B_TL='╭' B_TR='╮' B_BL='╰' B_BR='╯' B_H='─' B_V='│'
    else
        G_OK='ok' G_WARN='!!' G_FAIL='xx' G_BAR='|' G_DOTS='...'
        B_TL='+' B_TR='+' B_BL='+' B_BR='+' B_H='-' B_V='|'
        # cwm's own glyphs follow the installer's choice (D50).
        CWM_GLYPHS=ascii
        export CWM_GLYPHS
    fi
    STEP=0
    STEPS=8
    OPEN_LINE=0 # a step header is waiting for its result on the same line
    term_width
}

# term_width — set WIDTH, the columns notes and child output wrap at:
# $COLUMNS when numeric, else the terminal's size when stdout is one, else
# 80; clamped to 40..1000.
term_width() {
    WIDTH=""
    case "${COLUMNS:-}" in
        '' | *[!0-9]*) ;;
        *) WIDTH=$COLUMNS ;;
    esac
    if [ -z "$WIDTH" ] && [ -t 1 ]; then
        WIDTH=$( (stty size </dev/tty) 2>/dev/null | awk '{ print $2 }') || WIDTH=""
        case "$WIDTH" in
            '' | *[!0-9]* | 0) WIDTH=$(tput cols 2>/dev/null) || WIDTH="" ;;
        esac
    fi
    WIDTH=$(printf '%s\n' "$WIDTH" | awk '{ n = ($0 ~ /^[0-9]+$/ && $0 + 0 > 0) ? $0 + 0 : 80; if (n < 40) n = 40; if (n > 1000) n = 1000; print n }')
}

# banner SUBTITLE — the art is scripts/banner.txt verbatim; the harness
# compares both installers' output with that file line by line (D59).
banner() {
    printf '\n'
    printf '%s                ___ __      __ _ __ ___%s\n' "$C_BOLD" "$C_RESET"
    printf '%s   ______%s     %s / __|\\ \\ /\\ / /| '"'"'_ ` _ \\%s\n' "$C_CYAN" "$C_RESET" "$C_BOLD" "$C_RESET"
    printf '%s  /  %s>_%s%s  \\%s    %s| (__  \\ V  V / | | | | | |%s\n' "$C_CYAN" "$C_BOLD" "$C_RESET" "$C_CYAN" "$C_RESET" "$C_BOLD" "$C_RESET"
    printf '%s  \\______/%s    %s \\___|  \\_/\\_/  |_| |_| |_|%s\n' "$C_CYAN" "$C_RESET" "$C_BOLD" "$C_RESET"
    printf '  %sClaude Workspace Manager%s  %s%s%s\n\n' "$C_BOLD" "$C_RESET" "$C_DIM" "$1" "$C_RESET"
}

close_line() {
    if [ "$OPEN_LINE" = 1 ]; then
        printf '\n'
        OPEN_LINE=0
    fi
}

# step TITLE — "[n/N] Title …" and leave the line open for ok/warn/skip.
step() {
    close_line
    STEP=$((STEP + 1))
    printf '%s[%d/%d]%s %s %s%s%s ' "$C_CYAN" "$STEP" "$STEPS" "$C_RESET" "$1" "$C_DIM" "$G_DOTS" "$C_RESET"
    OPEN_LINE=1
}

# result GLYPH COLOUR TEXT [DETAIL] — finish the open step line (or print an
# indented result line after child output).
result() {
    if [ "$OPEN_LINE" = 1 ]; then
        printf '%s%s%s %s' "$2" "$1" "$C_RESET" "$3"
    else
        printf '      %s%s%s %s' "$2" "$1" "$C_RESET" "$3"
    fi
    if [ -n "${4:-}" ]; then printf ' %s%s%s' "$C_DIM" "$4" "$C_RESET"; fi
    printf '\n'
    OPEN_LINE=0
}
ok() { result "$G_OK" "$C_GREEN" "$1" "${2:-}"; }
warn() { result "$G_WARN" "$C_YELLOW" "$1" "${2:-}"; }
skip() { result '-' "$C_DIM" "$1" "${2:-}"; }

# wrapped LEAD LEADCOLS TEXT — print TEXT dimmed after a 6-space indent
# and LEAD (LEADCOLS columns wide), word-wrapped at $WIDTH: every
# continuation line repeats the indent, LEAD and TEXT's own leading spaces
# (hanging indent), and the colour starts and resets on each line. A word
# longer than the room is split. Widths are
# counted in characters (UTF-8 continuation bytes are not counted, so
# wide glyphs may wrap a little late; nothing is split mid-character).
wrapped() {
    room=$((WIDTH - 6 - $2))
    if [ "${#3}" -le "$room" ]; then
        printf '      %s%s%s%s\n' "$C_DIM" "$1" "$3" "$C_RESET"
        return 0
    fi
    printf '%s\n' "$3" | CW_A="      $C_DIM$1" CW_Z="$C_RESET" CW_W="$room" LC_ALL=C awk '
        BEGIN {
            a = ENVIRON["CW_A"]; z = ENVIRON["CW_Z"]; w = ENVIRON["CW_W"] + 0
            if (w < 10) w = 10
            for (i = 128; i < 192; i++) cont[sprintf("%c", i)] = 1
        }
        # vlen S — characters in S (bytes that do not continue a UTF-8 sequence)
        function vlen(s,    i, n) {
            n = 0
            for (i = 1; i <= length(s); i++) if (!(substr(s, i, 1) in cont)) n++
            return n
        }
        # cutat S N — byte length of the first N characters of S
        function cutat(s, n,    i, k) {
            k = 0
            for (i = 1; i <= length(s); i++) {
                if (!(substr(s, i, 1) in cont)) { if (k == n) return i - 1; k++ }
            }
            return length(s)
        }
        {
            s = $0
            ind = ""
            if (match(s, /^ +/) && RLENGTH <= w / 2) ind = substr(s, 1, RLENGTH)
            while (vlen(s) > w) {
                c = cutat(s, w)
                p = 0
                if (substr(s, c + 1, 1) == " ") p = c + 1
                else for (i = c; i > 1; i--) if (substr(s, i, 1) == " ") { p = i; break }
                head = (p > 1) ? substr(s, 1, p - 1) : ""
                if (head ~ /[^ ]/) {
                    rest = substr(s, p + 1)
                } else {
                    head = substr(s, 1, c); rest = substr(s, c + 1)
                }
                sub(/ +$/, "", head); sub(/^ +/, "", rest)
                printf "%s%s%s\n", a, head, z
                s = (rest == "") ? "" : ind rest
            }
            sub(/ +$/, "", s)
            if (s != "") printf "%s%s%s\n", a, s, z
        }'
}

# note TEXT — a dim detail line under the current step.
note() {
    close_line
    wrapped '' 0 "$1"
}

die() {
    close_line
    printf '%s%s error:%s %s\n' "$C_RED" "$G_FAIL" "$C_RESET" "$*" >&2
    exit 1
}

# run_child LOG CMD... — run a cwm subcommand with its output indented and
# dimmed under the current step (live, line by line). Its output is also
# kept in LOG (for the dashboard URL); returns the command's exit code.
run_child() {
    log=$1
    shift
    close_line
    : >"$log"
    {
        "$@" 2>&1 && echo 0 >"$log.rc" || echo $? >"$log.rc"
    } | while IFS= read -r line || [ -n "$line" ]; do
        printf '%s\n' "$line" >>"$log"
        wrapped "$G_BAR " 2 "$line"
    done
    rc=$(cat "$log.rc" 2>/dev/null || echo 1)
    return "$rc"
}

# summary LINE... — the closing box; width follows the longest line.
summary() {
    printf '\n'
    printf '%s\n' "$@" | awk -v tl="$B_TL" -v tr="$B_TR" -v bl="$B_BL" -v br="$B_BR" \
        -v h="$B_H" -v v="$B_V" -v c="$C_CYAN" -v r="$C_RESET" -v b="$C_BOLD" '
        { line[NR] = $0; if (length($0) > w) w = length($0) }
        END {
            bar = ""; for (i = 0; i < w + 2; i++) bar = bar h
            printf "  %s%s%s%s%s\n", c, tl, bar, tr, r
            for (i = 1; i <= NR; i++) {
                pad = ""; for (j = length(line[i]); j < w; j++) pad = pad " "
                text = line[i]
                if (i == 1) text = b text r
                printf "  %s%s%s %s%s %s%s%s\n", c, v, r, text, pad, c, v, r
            }
            printf "  %s%s%s%s%s\n", c, bl, bar, br, r
        }'
    printf '\n'
}

# human BYTES — "4.2 MB" style size.
human() {
    awk -v n="$1" 'BEGIN {
        if (n >= 1048576) printf "%.1f MB", n / 1048576
        else if (n >= 1024) printf "%.0f KB", n / 1024
        else printf "%d B", n }'
}

# shown PATH — $HOME-relative form for display (a literal ~, on purpose).
# shellcheck disable=SC2088
shown() {
    case "$1" in
        "$HOME"/*) printf '~/%s' "${1#"$HOME"/}" ;;
        *) printf '%s' "$1" ;;
    esac
}

# shown_list LINES — newline-separated paths as one display line.
shown_list() {
    printf '%s\n' "$1" | while IFS= read -r f; do
        [ -n "$f" ] && printf '%s\n' "$(shown "$f")"
    done | paste -sd ' ' -
}

# --- mechanics --------------------------------------------------------------

usage() {
    cat <<'USAGE'
usage: install.sh [--version vX.Y.Z] [--no-service] [--no-open] [--uninstall]
  --version X   install release X (default: latest)
  --no-service  skip `cwm service install` (no autostart at login)
  --no-open     do not open the dashboard at the end
  --uninstall   remove cwm, its autostart and PATH lines (keeps ~/.cwm)
USAGE
}

# fetch URL DEST — curl or wget, failing on HTTP errors.
fetch() {
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL --retry 2 -o "$2" "$1"
    elif command -v wget >/dev/null 2>&1; then
        wget -q -O "$2" "$1"
    else
        die "need curl or wget"
    fi
}

# sha256 FILE — prints the hex digest with whatever tool exists.
sha256() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | cut -d' ' -f1
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | cut -d' ' -f1
    elif command -v openssl >/dev/null 2>&1; then
        openssl dgst -sha256 -r "$1" | cut -d' ' -f1
    else
        return 1
    fi
}

detect_platform() {
    case "$(uname -s)" in
        Linux) OS=linux ;;
        Darwin) OS=darwin ;;
        *) die "unsupported OS $(uname -s) - on Windows use install.ps1" ;;
    esac
    case "$(uname -m)" in
        x86_64 | amd64) ARCH=amd64 ;;
        aarch64 | arm64) ARCH=arm64 ;;
        *) die "unsupported CPU $(uname -m) (cwm ships amd64 and arm64)" ;;
    esac
    # Rosetta: a shell translated on Apple silicon still wants arm64.
    if [ "$OS" = darwin ] && [ "$ARCH" = amd64 ] &&
        [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)" = 1 ]; then
        ARCH=arm64
    fi
}

latest_tag() {
    api="${CWM_INSTALL_API:-https://api.github.com}"
    json="$TMP/latest.json"
    if fetch "$api/repos/$REPO/releases/latest" "$json" 2>/dev/null; then
        tag=$(sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$json" | head -n 1)
        if [ -n "$tag" ]; then
            printf '%s\n' "$tag"
            return 0
        fi
    fi
    # API rate-limited or unreachable: the releases/latest redirect names the tag.
    if [ -z "${CWM_INSTALL_API:-}" ] && command -v curl >/dev/null 2>&1; then
        url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest" 2>/dev/null || true)
        tag=${url##*/}
        case "$tag" in v[0-9]*) printf '%s\n' "$tag"; return 0 ;; esac
    fi
    return 1
}

profile_files() {
    printf '%s\n' "$HOME/.profile"
    for f in "$HOME/.bashrc" "$HOME/.zshrc" "$HOME/.zprofile" "$HOME/.bash_profile"; do
        [ -f "$f" ] && printf '%s\n' "$f"
    done
    return 0
}

# add_to_path — prints one "added to <file>" note per profile it touched.
add_to_path() {
    case ":$PATH:" in *":$BINDIR:"*) return 0 ;; esac
    case "$BINDIR" in
        "$HOME"/*) rel="\$HOME/${BINDIR#"$HOME"/}" ;;
        *) rel="$BINDIR" ;;
    esac
    line="export PATH=\"$rel:\$PATH\" $MARKER"
    profile_files | while IFS= read -r f; do
        if [ -f "$f" ] && grep -F "$MARKER" "$f" >/dev/null 2>&1; then
            continue
        fi
        printf '\n%s\n' "$line" >>"$f"
        printf '%s\n' "$f"
    done
    PATH="$BINDIR:$PATH"
    export PATH
}

# remove_from_path — prints each profile it cleaned.
remove_from_path() {
    profile_files | while IFS= read -r f; do
        [ -f "$f" ] || continue
        if grep -F "$MARKER" "$f" >/dev/null 2>&1; then
            grep -v -F "$MARKER" "$f" >"$f.cwm-tmp" || true
            cat "$f.cwm-tmp" >"$f" # keep the original file's mode/ownership
            rm -f "$f.cwm-tmp"
            printf '%s\n' "$f"
        fi
    done
    return 0
}

do_uninstall() {
    STEPS=3
    banner "uninstaller"
    TMP=$(mktemp -d 2>/dev/null || mktemp -d -t cwm-install)
    trap 'rm -rf "$TMP"' EXIT INT TERM
    dest="$BINDIR/cwm"

    step "Removing the login autostart"
    if [ -x "$dest" ]; then
        if run_child "$TMP/svc" "$dest" service uninstall; then
            ok "autostart removed"
        else
            warn "service uninstall reported an error (continuing)"
        fi
        "$dest" service stop >/dev/null 2>&1 || true
    else
        skip "no cwm at $(shown "$dest")"
    fi

    step "Removing the binary"
    if [ -e "$dest" ]; then
        rm -f "$dest"
        ok "removed $(shown "$dest")"
    else
        skip "nothing to remove"
    fi

    step "Cleaning PATH lines"
    cleaned=$(remove_from_path)
    if [ -n "$cleaned" ]; then
        ok "removed the PATH line" "from $(shown_list "$cleaned")"
    else
        skip "no PATH lines found"
    fi

    summary "cwm was uninstalled" \
        "" \
        "Your workspace ($(shown "${CWM_HOME:-$HOME/.cwm}")) was kept -" \
        "delete it yourself if you want it gone."
}

main() {
    VERSION="${CWM_VERSION:-}"
    NO_SERVICE=0
    NO_OPEN=0
    UNINSTALL=0
    while [ $# -gt 0 ]; do
        case "$1" in
            --version) [ $# -ge 2 ] || { ui_init; die "--version needs a value"; }; VERSION=$2; shift ;;
            --version=*) VERSION=${1#--version=} ;;
            --no-service) NO_SERVICE=1 ;;
            --no-open) NO_OPEN=1 ;;
            --uninstall) UNINSTALL=1 ;;
            -h | --help) usage; exit 0 ;;
            *) ui_init; die "unknown flag $1 (see --help)" ;;
        esac
        shift
    done
    ui_init
    [ -n "${HOME:-}" ] || die "HOME is not set"
    BINDIR="${CWM_INSTALL_DIR:-$HOME/.local/bin}"

    if [ "$UNINSTALL" = 1 ]; then
        do_uninstall
        return 0
    fi

    detect_platform
    TMP=$(mktemp -d 2>/dev/null || mktemp -d -t cwm-install)
    trap 'rm -rf "$TMP"' EXIT INT TERM

    which_tag="pinned"
    if [ -z "$VERSION" ]; then
        VERSION=$(latest_tag) || die "could not determine the latest release (pass --version vX.Y.Z)"
        which_tag="latest"
    fi
    case "$VERSION" in
        v*) TAG=$VERSION ;;
        *) TAG="v$VERSION" ;;
    esac
    case "$TAG" in
        *[!A-Za-z0-9._+-]*) die "invalid version $VERSION" ;;
    esac
    NUM=${TAG#v}
    ARCHIVE="cwm_${NUM}_${OS}_${ARCH}.tar.gz"
    BASE="${CWM_INSTALL_BASE_URL:-https://github.com/$REPO/releases/download}/$TAG"

    banner "installer $G_DOTS cwm $TAG"
    if [ "$(id -u)" = 0 ]; then
        printf '%s%s%s running as root installs cwm for root only; run as your normal user instead\n\n' "$C_YELLOW" "$G_WARN" "$C_RESET"
    fi

    step "Detecting platform"
    ok "$OS/$ARCH" "release $TAG ($which_tag)"

    step "Downloading $ARCHIVE"
    fetch "$BASE/$ARCHIVE" "$TMP/$ARCHIVE" || die "download failed: $BASE/$ARCHIVE"
    fetch "$BASE/checksums.txt" "$TMP/checksums.txt" || die "download failed: $BASE/checksums.txt"
    ok "$(human "$(wc -c <"$TMP/$ARCHIVE" | tr -d ' ')")"

    step "Verifying SHA-256"
    want=$(awk -v f="$ARCHIVE" '$2 == f || $2 == "*" f { print $1; exit }' "$TMP/checksums.txt")
    [ -n "$want" ] || die "$ARCHIVE is not listed in checksums.txt - refusing to install"
    got=$(sha256 "$TMP/$ARCHIVE") || die "no SHA-256 tool (sha256sum, shasum or openssl) - refusing to install unverified"
    if [ "$got" != "$want" ]; then
        die "checksum mismatch for $ARCHIVE (expected $want, got $got) - nothing was installed"
    fi
    ok "checksum OK" "$(printf '%s' "$got" | cut -c1-12)"

    step "Installing"
    mkdir -p "$TMP/x"
    tar -xzf "$TMP/$ARCHIVE" -C "$TMP/x" || die "could not unpack $ARCHIVE"
    [ -f "$TMP/x/cwm" ] || die "archive has no cwm binary"
    mkdir -p "$BINDIR"
    dest="$BINDIR/cwm"
    upgraded=""
    if [ -x "$dest" ]; then
        # Upgrade: stop the old agent so the new binary serves the dashboard.
        "$dest" service stop >/dev/null 2>&1 || true
        upgraded=" (upgraded)"
    fi
    cp "$TMP/x/cwm" "$BINDIR/.cwm.new.$$"
    chmod 0755 "$BINDIR/.cwm.new.$$"
    mv -f "$BINDIR/.cwm.new.$$" "$dest"
    ok "$(shown "$dest")$upgraded" "$("$dest" --version)"

    step "Updating PATH"
    case ":$PATH:" in
        *":$BINDIR:"*) skip "$(shown "$BINDIR") is already on PATH" ;;
        *)
            touched=$(add_to_path)
            PATH="$BINDIR:$PATH"
            export PATH
            if [ -n "$touched" ]; then
                ok "added $(shown "$BINDIR") to PATH" "in $(shown_list "$touched")"
            else
                ok "PATH line already present" "new terminals pick it up"
            fi
            note "open a new terminal (or run: export PATH=\"$BINDIR:\$PATH\") to use cwm directly"
            ;;
    esac

    step "Initializing workspace"
    if [ -f "${CWM_HOME:-$HOME/.cwm}/config.toml" ]; then
        skip "already initialized" "${CWM_HOME:-~/.cwm}"
    elif run_child "$TMP/init" "$dest" init; then
        ok "workspace ready"
    else
        warn "cwm init failed - run it yourself later"
    fi

    step "Installing login autostart"
    if [ "$NO_SERVICE" = 1 ]; then
        skip "skipped (--no-service)"
    elif run_child "$TMP/svc" "$dest" service install; then
        ok "the agent starts at every login"
    else
        warn "autostart could not be installed - run 'cwm service install' later"
    fi

    step "Opening the dashboard"
    if [ "$NO_OPEN" = 1 ]; then
        skip "skipped (--no-open)"
    elif run_child "$TMP/dash" "$dest" dashboard; then
        ok "dashboard opened in your browser"
    else
        warn "could not open the dashboard - run 'cwm dashboard'"
    fi
    close_line

    # The URL printed by `cwm dashboard` / `cwm service install` (never
    # the token: those lines carry none).
    url=$(cat "$TMP/dash" "$TMP/svc" 2>/dev/null | grep -o 'http://[^ /?]*/' | head -n 1 || true)
    [ -n "$url" ] || url="run: cwm dashboard"

    summary "cwm $TAG is installed" \
        "" \
        "  binary      $(shown "$dest")" \
        "  version     $NUM ($OS/$ARCH)" \
        "  dashboard   $url" \
        "" \
        "Next steps" \
        "  1. open the dashboard    cwm dashboard" \
        "  2. add a Claude login    cwm account add main" \
        "     (or use the setup wizard in the dashboard)" \
        "" \
        "Uninstall" \
        "curl -fsSL $PAGES/install.sh | sh -s -- --uninstall"
}

main "$@"
