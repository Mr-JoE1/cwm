#!/bin/sh
# cwm installer for Linux and macOS (SPEC-V0.6 G, D37).
#
#   curl -fsSL https://mr-joe1.github.io/cwm/install.sh | sh
#   curl -fsSL https://mr-joe1.github.io/cwm/install.sh | sh -s -- --version v0.6.0 --no-service
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
#        CWM_INSTALL_API / CWM_INSTALL_BASE_URL (test seams: GitHub API and
#        release download base).
#
# The whole script is one function called on the last line, so a truncated
# download never runs half an installer.

set -eu

REPO="Mr-JoE1/cwm"
MARKER="# added by cwm installer"

say() { printf 'cwm-install: %s\n' "$*"; }
die() { printf 'cwm-install: error: %s\n' "$*" >&2; exit 1; }

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
        *) die "unsupported OS $(uname -s) — on Windows use install.ps1" ;;
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
    die "could not determine the latest release (pass --version vX.Y.Z)"
}

profile_files() {
    printf '%s\n' "$HOME/.profile"
    for f in "$HOME/.bashrc" "$HOME/.zshrc" "$HOME/.zprofile" "$HOME/.bash_profile"; do
        [ -f "$f" ] && printf '%s\n' "$f"
    done
    return 0
}

add_to_path() {
    case ":$PATH:" in *":$BINDIR:"*) return 0 ;; esac
    case "$BINDIR" in
        "$HOME"/*) shown="\$HOME/${BINDIR#"$HOME"/}" ;;
        *) shown="$BINDIR" ;;
    esac
    line="export PATH=\"$shown:\$PATH\" $MARKER"
    profile_files | while IFS= read -r f; do
        if [ -f "$f" ] && grep -F "$MARKER" "$f" >/dev/null 2>&1; then
            continue
        fi
        printf '\n%s\n' "$line" >>"$f"
        say "added $BINDIR to PATH in $f"
    done
    PATH="$BINDIR:$PATH"
    export PATH
    say "open a new terminal (or run: export PATH=\"$BINDIR:\$PATH\") to use cwm directly"
}

remove_from_path() {
    profile_files | while IFS= read -r f; do
        [ -f "$f" ] || continue
        if grep -F "$MARKER" "$f" >/dev/null 2>&1; then
            grep -v -F "$MARKER" "$f" >"$f.cwm-tmp" || true
            cat "$f.cwm-tmp" >"$f" # keep the original file's mode/ownership
            rm -f "$f.cwm-tmp"
            say "removed the PATH line from $f"
        fi
    done
    return 0
}

do_uninstall() {
    dest="$BINDIR/cwm"
    if [ -x "$dest" ]; then
        "$dest" service uninstall || say "service uninstall reported an error (continuing)"
        "$dest" service stop >/dev/null 2>&1 || true
        rm -f "$dest"
        say "removed $dest"
    else
        say "no cwm at $dest"
    fi
    remove_from_path
    say "your workspace (${CWM_HOME:-$HOME/.cwm}) was kept — delete it yourself if you want it gone"
}

main() {
    VERSION="${CWM_VERSION:-}"
    NO_SERVICE=0
    NO_OPEN=0
    UNINSTALL=0
    while [ $# -gt 0 ]; do
        case "$1" in
            --version) [ $# -ge 2 ] || die "--version needs a value"; VERSION=$2; shift ;;
            --version=*) VERSION=${1#--version=} ;;
            --no-service) NO_SERVICE=1 ;;
            --no-open) NO_OPEN=1 ;;
            --uninstall) UNINSTALL=1 ;;
            -h | --help) usage; exit 0 ;;
            *) die "unknown flag $1 (see --help)" ;;
        esac
        shift
    done
    [ -n "${HOME:-}" ] || die "HOME is not set"
    BINDIR="${CWM_INSTALL_DIR:-$HOME/.local/bin}"

    if [ "$UNINSTALL" = 1 ]; then
        do_uninstall
        return 0
    fi
    if [ "$(id -u)" = 0 ]; then
        say "warning: running as root installs cwm for root only; run as your normal user instead"
    fi

    detect_platform
    TMP=$(mktemp -d 2>/dev/null || mktemp -d -t cwm-install)
    trap 'rm -rf "$TMP"' EXIT INT TERM

    if [ -z "$VERSION" ]; then
        VERSION=$(latest_tag)
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

    say "installing cwm $TAG ($OS/$ARCH) into $BINDIR"
    fetch "$BASE/$ARCHIVE" "$TMP/$ARCHIVE" || die "download failed: $BASE/$ARCHIVE"
    fetch "$BASE/checksums.txt" "$TMP/checksums.txt" || die "download failed: $BASE/checksums.txt"

    want=$(awk -v f="$ARCHIVE" '$2 == f || $2 == "*" f { print $1; exit }' "$TMP/checksums.txt")
    [ -n "$want" ] || die "$ARCHIVE is not listed in checksums.txt — refusing to install"
    got=$(sha256 "$TMP/$ARCHIVE") || die "no SHA-256 tool (sha256sum, shasum or openssl) — refusing to install unverified"
    if [ "$got" != "$want" ]; then
        die "checksum mismatch for $ARCHIVE (expected $want, got $got) — nothing was installed"
    fi
    say "checksum OK ($got)"

    mkdir -p "$TMP/x"
    tar -xzf "$TMP/$ARCHIVE" -C "$TMP/x" || die "could not unpack $ARCHIVE"
    [ -f "$TMP/x/cwm" ] || die "archive has no cwm binary"

    mkdir -p "$BINDIR"
    dest="$BINDIR/cwm"
    if [ -x "$dest" ]; then
        # Upgrade: stop the old agent so the new binary serves the dashboard.
        "$dest" service stop >/dev/null 2>&1 || true
    fi
    cp "$TMP/x/cwm" "$BINDIR/.cwm.new.$$"
    chmod 0755 "$BINDIR/.cwm.new.$$"
    mv -f "$BINDIR/.cwm.new.$$" "$dest"
    say "installed $("$dest" --version)"

    add_to_path

    if [ ! -f "${CWM_HOME:-$HOME/.cwm}/config.toml" ]; then
        "$dest" init || say "cwm init failed — run it yourself later"
    fi
    if [ "$NO_SERVICE" = 0 ]; then
        "$dest" service install || say "autostart could not be installed — run 'cwm service install' later"
    fi
    if [ "$NO_OPEN" = 0 ]; then
        "$dest" dashboard || say "could not open the dashboard — run 'cwm dashboard'"
    fi
    say "done. Next: cwm account add main   (or use the setup wizard in the dashboard)"
}

main "$@"
