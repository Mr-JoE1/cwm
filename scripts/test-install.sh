#!/usr/bin/env bash
# check() evals its single-quoted condition later, so variables are meant
# to expand then (SC2016) and CODE is read inside those strings (SC2034).
# shellcheck disable=SC2016,SC2034
# Installer harness (SPEC-V0.6 G): builds a snapshot release for this host
# with goreleaser's archive names, serves it from a local
# `python3 -m http.server` standing in for both the GitHub API and the
# release download host, and runs scripts/install.sh against it with a
# throwaway HOME and CWM_SERVICE_DRYRUN=1 (the service step only prints
# its plan). No network, nothing outside the sandbox is touched.
set -euo pipefail

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
SANDBOX=$(mktemp -d)
SERVER_PID=""
FAKE_PID=""
cleanup() {
    if [[ -n "$SERVER_PID" ]]; then kill "$SERVER_PID" 2>/dev/null || true; fi
    if [[ -n "$FAKE_PID" ]]; then kill "$FAKE_PID" 2>/dev/null || true; fi
    rm -rf "$SANDBOX"
}
trap cleanup EXIT

PASS=0
FAIL=0
ok() { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
LAST_OUT=""
bad() {
    FAIL=$((FAIL + 1))
    printf 'FAIL %s\n' "$1"
    if [[ -n "${GITHUB_ACTIONS:-}" ]]; then
        # Annotations are readable without log access: one line carrying
        # the tail of the failing step's output.
        local o
        o=$(printf '%s\n' "$LAST_OUT" | tail -n 8 | tr -d '\r' | tr '\n' '|')
        o=${o//'%'/'%25'}
        printf '::error title=installer harness::FAIL %s :: %s\n' "$1" "$o"
    fi
}
check() {
    LAST_OUT=${OUT:-}
    if eval "$2"; then ok "$1"; else bad "$1"; fi
}

# banner_of TEXT — the 4 banner lines as printed (from the first art line
# on), CRs stripped; empty when the first line is missing.
ART_FIRST=$(head -n1 "$REPO/scripts/banner.txt")
banner_of() {
    local n
    n=$(printf '%s\n' "$1" | tr -d '\r' | grep -n -F -x -- "$ART_FIRST" | head -n1 | cut -d: -f1)
    [[ -n "$n" ]] || return 0
    printf '%s\n' "$1" | tr -d '\r' | sed -n "${n},$((n + 3))p"
}
# banner_ok TEXT — the banner is scripts/banner.txt, byte for byte (D59).
banner_ok() { [[ "$(banner_of "$1")" == "$(cat "$REPO/scripts/banner.txt")" ]]; }

# plain_lines TEXT — TEXT with CRs and colour escapes removed.
plain_lines() { printf '%s\n' "$1" | tr -d '\r' | sed 's/\x1b\[[0-9;]*m//g'; }
# child_text TEXT — child output with the gutter removed and wrapped lines
# joined back (a soft wrap drops the space it broke at, so callers match
# "a ?b" across a possible break).
child_text() { plain_lines "$1" | sed -n 's/^      [|│] //p' | tr -d '\n'; }
# gutter_max TEXT — the widest line under a step (6-space indent: notes,
# child output, indented results) in characters, escapes stripped.
gutter_max() {
    plain_lines "$1" | LC_ALL=C tr -d '\200-\277' |
        LC_ALL=C awk '/^      / && length($0) > m { m = length($0) } END { print m + 0 }'
}
# wrap_ok TEXT GUTTER FROM WANT [note] — the line starting GUTTER FROM and
# the ones after it add up (spaces ignored) to exactly WANT, over at least
# two lines, every one starting with GUTTER; with "note", continuation
# lines must not carry a child-output bar.
wrap_ok() {
    local gutter=$2 want=${4// /} got="" n=0 line started=0
    while IFS= read -r line; do
        if ((!started)); then
            [[ $line == "$gutter$3"* ]] || continue
            started=1
        fi
        [[ $line == "$gutter"* ]] || return 1
        if [[ ${5:-} == note ]]; then
            [[ $line == "      |"* || $line == "      │"* || $line == "       "* ]] && return 1
        fi
        got+=${line#"$gutter"}
        got=${got// /}
        n=$((n + 1))
        if [[ $got == "$want" ]]; then ((n >= 2)); return; fi
        [[ $want == "$got"* ]] || return 1
    done < <(plain_lines "$1")
    return 1
}

case "$(uname -s)" in Linux) OS=linux ;; Darwin) OS=darwin ;; *) echo "unsupported host"; exit 1 ;; esac
case "$(uname -m)" in x86_64 | amd64) ARCH=amd64 ;; aarch64 | arm64) ARCH=arm64 ;; *) echo "unsupported arch"; exit 1 ;; esac

VER=0.0.0-harness
TAG="v$VER"
SRV="$SANDBOX/srv"
DL="$SRV/download/$TAG"
mkdir -p "$DL" "$SRV/repos/Mr-JoE1/cwm/releases" "$SANDBOX/build"

echo "== building snapshot $TAG for $OS/$ARCH"
(cd "$REPO" && CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X github.com/Mr-JoE1/cwm/internal/cli.Version=$VER -X github.com/Mr-JoE1/cwm/internal/cli.Commit=harness" \
    -o "$SANDBOX/build/cwm" ./cmd/cwm)
cp "$REPO/LICENSE" "$SANDBOX/build/"
ARCHIVE="cwm_${VER}_${OS}_${ARCH}.tar.gz"
tar -czf "$DL/$ARCHIVE" -C "$SANDBOX/build" cwm LICENSE
(cd "$DL" && { sha256sum "$ARCHIVE" 2>/dev/null || shasum -a 256 "$ARCHIVE"; } >checksums.txt)
printf '{\n  "url": "fake",\n  "tag_name": "%s",\n  "name": "%s"\n}\n' "$TAG" "$TAG" >"$SRV/repos/Mr-JoE1/cwm/releases/latest"

PORT=$(python3 -I -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')
python3 -I -m http.server "$PORT" --bind 127.0.0.1 --directory "$SRV" >"$SANDBOX/http.log" 2>&1 &
SERVER_PID=$!
for _ in $(seq 50); do
    if curl -fsS -o /dev/null "http://127.0.0.1:$PORT/download/$TAG/checksums.txt" 2>/dev/null; then break; fi
    sleep 0.1
done

# A second server fakes the rate-limited API (403) and the github.com
# releases/latest redirect (scripts/fake-release-web.py).
FPORT=$(python3 -I -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')
python3 -I "$REPO/scripts/fake-release-web.py" "$FPORT" "$TAG" >"$SANDBOX/fake.log" 2>&1 &
FAKE_PID=$!
for _ in $(seq 50); do
    if curl -fsS -o /dev/null "http://127.0.0.1:$FPORT/web/Mr-JoE1/cwm/releases/tag/$TAG" 2>/dev/null; then break; fi
    sleep 0.1
done
RATE_LIMITED=(CWM_INSTALL_API="http://127.0.0.1:$FPORT/api403" CWM_INSTALL_WEB="http://127.0.0.1:$FPORT/web")
BOTH_FAIL=(CWM_INSTALL_API="http://127.0.0.1:$FPORT/api403" CWM_INSTALL_WEB="http://127.0.0.1:$FPORT/webnone")

# run_install HOME_DIR ARGS... — install.sh in a clean environment, plus
# whatever VAR=value pairs INSTALL_ENV holds.
INSTALL_ENV=()
run_install() {
    local home=$1
    shift
    mkdir -p "$home"
    env -i HOME="$home" PATH="/usr/local/bin:/usr/bin:/bin" \
        XDG_CONFIG_HOME="$home/.config" XDG_RUNTIME_DIR="$home/run" \
        CWM_SERVICE_DRYRUN=1 \
        CWM_INSTALL_API="http://127.0.0.1:$PORT" \
        CWM_INSTALL_BASE_URL="http://127.0.0.1:$PORT/download" \
        "${INSTALL_ENV[@]}" \
        sh "$REPO/scripts/install.sh" "$@"
}

echo "== 1. fresh install (latest via the fake API)"
H1="$SANDBOX/home1"
set +e
OUT=$(run_install "$H1" --no-open 2>&1)
CODE=$?
set -e
printf '%s\n' "$OUT" | sed 's/^/    | /'
check "install exits 0" '[[ $CODE -eq 0 ]]'
check "resolved latest tag from the API" 'grep -q "release $TAG (latest)" <<<"$OUT"'
check "checksum verified" 'grep -q "checksum OK" <<<"$OUT"'
check "binary installed in ~/.local/bin" '[[ -x "$H1/.local/bin/cwm" ]]'
check "installed binary is the snapshot" '"$H1/.local/bin/cwm" --version | grep -q "$VER"'
check "PATH line added to ~/.profile" 'grep -q "# added by cwm installer" "$H1/.profile"'
check "cwm init ran (config.toml)" '[[ -f "$H1/.cwm/config.toml" ]]'
check "service step ran in dry-run mode" 'grep -q "cwm service install (dry run" <<<"$OUT"'
# Redirected output wraps at 80 columns, so join child lines back first.
check "dry run printed the autostart plan" 'grep -q -E "would write ?$H1/\.config/" <<<"$(child_text "$OUT")"'
check "dry run wrote no autostart files" '[[ ! -e "$H1/.config/systemd" && ! -e "$H1/.config/autostart" ]]'
check "no agent was started (no server.toml)" '[[ ! -e "$H1/.cwm/server.toml" ]]'
# v0.6.1 look (D51): banner, numbered steps, short hash, summary box; the
# non-TTY run is plain ASCII with no escape sequences.
check "banner names the product and version" 'grep -q "Claude Workspace Manager .*cwm $TAG" <<<"$OUT"'
check "banner art is scripts/banner.txt verbatim (sh)" 'banner_ok "$OUT"'
SH_BANNER=$(banner_of "$OUT")
check "numbered steps [1/8]..[8/8]" 'grep -q "^\[1/8\] Detecting platform" <<<"$OUT" && grep -q "^\[8/8\] Opening the dashboard" <<<"$OUT"'
SUM=$(awk -v f="$ARCHIVE" '$2 == f {print $1}' "$DL/checksums.txt")
check "checksum shown as 12 chars, never all 64" 'grep -q "checksum OK ${SUM:0:12}$" <<<"$OUT" && ! grep -q "$SUM" <<<"$OUT"'
check "child output is indented under its step" 'grep -q "^      | ok cwm home initialized at" <<<"$OUT"'
check "summary box with next steps and uninstall" 'grep -q "| cwm $TAG is installed" <<<"$OUT" && grep -q "cwm account add main" <<<"$OUT" && grep -q "install.sh | sh -s -- --uninstall" <<<"$OUT"'
check "plain mode: no escape sequences" '! grep -q $'"'"'\033'"'"' <<<"$OUT"'
check "plain mode: pure ASCII (cwm glyphs too, D50)" '! LC_ALL=C grep -q "[^[:print:][:space:]]" <<<"$OUT"'

echo "== 1b. terminal look (CWM_INSTALL_FANCY=1, UTF-8 locale; then NO_COLOR)"
set +e
INSTALL_ENV=(LANG=C.UTF-8 TERM=xterm CWM_INSTALL_FANCY=1)
OUT=$(run_install "$SANDBOX/home1b" --no-open --no-service 2>&1)
CODE=$?
set -e
check "fancy install exits 0" '[[ $CODE -eq 0 ]]'
check "fancy: colours" 'grep -q $'"'"'\033\[32m'"'"' <<<"$OUT"'
check "fancy: unicode glyphs and box" 'grep -q "✓" <<<"$OUT" && grep -q "╭" <<<"$OUT" && grep -q "│ ✓ cwm home initialized" <<<"$OUT"'
set +e
INSTALL_ENV=(LANG=C.UTF-8 TERM=xterm CWM_INSTALL_FANCY=1 NO_COLOR=1)
OUT=$(run_install "$SANDBOX/home1c" --no-open --no-service 2>&1)
CODE=$?
set -e
check "NO_COLOR: glyphs stay, colours go" '[[ $CODE -eq 0 ]] && grep -q "✓" <<<"$OUT" && ! grep -q $'"'"'\033'"'"' <<<"$OUT"'
check "NO_COLOR terminal look: banner art verbatim (sh)" 'banner_ok "$OUT"'
INSTALL_ENV=()

# Notes and child output wrap at the terminal width with the gutter kept
# on every continuation line; a long HOME makes both the PATH note and
# the init line longer than 60 columns.
echo "== 1d. narrow terminal (COLUMNS=60): notes and child output wrap"
NH="$SANDBOX/narrow-home-with-a-deliberately-long-directory-name"
NOTE="open a new terminal (or run: export PATH=\"$NH/.local/bin:\$PATH\") to use cwm directly"
set +e
INSTALL_ENV=(COLUMNS=60)
OUT=$(run_install "$NH" --no-open 2>&1)
CODE=$?
set -e
printf '%s\n' "$OUT" | sed 's/^/    | /'
check "narrow: install exits 0" '[[ $CODE -eq 0 ]]'
check "narrow: no note/child line wider than 60 columns" '[[ $(gutter_max "$OUT") -le 60 ]]'
check "narrow: PATH note wraps, continuations indented 6 spaces" 'wrap_ok "$OUT" "      " "open a new terminal" "$NOTE" note'
check "narrow: child line wraps, continuations keep \"      | \"" 'wrap_ok "$OUT" "      | " "ok cwm home initialized at" "ok cwm home initialized at $NH/.cwm"'
check "narrow: no trailing spaces on wrapped lines" '! plain_lines "$OUT" | grep -q "^      .* $"'
check "narrow: banner art still verbatim" 'banner_ok "$OUT"'
NH="$SANDBOX/narrow-fancy-home-with-a-deliberately-long-directory-name"
NOTE="open a new terminal (or run: export PATH=\"$NH/.local/bin:\$PATH\") to use cwm directly"
set +e
INSTALL_ENV=(LANG=C.UTF-8 TERM=xterm CWM_INSTALL_FANCY=1 COLUMNS=60)
OUT=$(run_install "$NH" --no-open --no-service 2>&1)
CODE=$?
set -e
check "narrow fancy: install exits 0" '[[ $CODE -eq 0 ]]'
check "narrow fancy: no note/child line wider than 60 columns" '[[ $(gutter_max "$OUT") -le 60 ]]'
check "narrow fancy: PATH note wraps, continuations indented 6 spaces" 'wrap_ok "$OUT" "      " "open a new terminal" "$NOTE" note'
check "narrow fancy: child line wraps, continuations keep \"      │ \"" 'wrap_ok "$OUT" "      │ " "✓ cwm home initialized at" "✓ cwm home initialized at $NH/.cwm"'
check "narrow fancy: dim colour starts and resets on every wrapped line" '! grep -a $'"'"'^      \033\[2m'"'"' <<<"$OUT" | grep -a -v -q $'"'"'\033\[0m$'"'"''
check "narrow fancy: banner art still verbatim" '[[ "$(banner_of "$(plain_lines "$OUT")")" == "$(cat "$REPO/scripts/banner.txt")" ]]'
INSTALL_ENV=()

echo "== 1e. API rate-limited (403): the releases/latest redirect names the tag"
HR="$SANDBOX/home-ratelimited"
set +e
INSTALL_ENV=("${RATE_LIMITED[@]}")
OUT=$(run_install "$HR" --no-open --no-service 2>&1)
CODE=$?
set -e
printf '%s\n' "$OUT" | sed 's/^/    | /'
check "rate-limited: install exits 0" '[[ $CODE -eq 0 ]]'
check "rate-limited: latest tag from the redirect" 'grep -q "release $TAG (latest)" <<<"$OUT"'
check "rate-limited: that release is installed" '"$HR/.local/bin/cwm" --version | grep -q "$VER"'
set +e
INSTALL_ENV=("${BOTH_FAIL[@]}")
OUT=$(run_install "$SANDBOX/home-bothfail" --no-open --no-service 2>&1)
CODE=$?
set -e
printf '%s\n' "$OUT" | sed 's/^/    | /'
check "API and redirect both fail: exits non-zero" '[[ $CODE -ne 0 ]]'
check "both fail: error names --version and CWM_VERSION" 'grep -q "could not determine the latest release (pass --version vX.Y.Z)" <<<"$OUT" && grep -q "or set CWM_VERSION=vX.Y.Z first" <<<"$OUT"'
check "both fail: nothing installed" '[[ ! -e "$SANDBOX/home-bothfail/.local/bin/cwm" ]]'
INSTALL_ENV=()

echo "== 2. re-run is idempotent (explicit --version)"
touch "$H1/.bashrc"
set +e
OUT=$(run_install "$H1" --version "$VER" --no-open 2>&1)
CODE=$?
set -e
check "re-install exits 0" '[[ $CODE -eq 0 ]]'
check "PATH line not duplicated in ~/.profile" '[[ $(grep -c "# added by cwm installer" "$H1/.profile") -eq 1 ]]'
check "existing ~/.bashrc gets the PATH line" 'grep -q "# added by cwm installer" "$H1/.bashrc"'
check "init skipped when already initialised" '! grep -q "cwm home initialized" <<<"$OUT"'

echo "== 3. tampered checksums.txt aborts"
cp "$DL/checksums.txt" "$SANDBOX/checksums.good"
sed 's/^[0-9a-f]\{4\}/0000/' "$SANDBOX/checksums.good" >"$DL/checksums.txt"
H3="$SANDBOX/home3"
set +e
OUT=$(run_install "$H3" --no-open 2>&1)
CODE=$?
set -e
printf '%s\n' "$OUT" | sed 's/^/    | /'
check "mismatch exits non-zero" '[[ $CODE -ne 0 ]]'
check "mismatch is reported" 'grep -q "checksum mismatch" <<<"$OUT"'
check "nothing installed on mismatch" '[[ ! -e "$H3/.local/bin/cwm" && ! -e "$H3/.cwm" ]]'

echo "== 4. archive missing from checksums.txt aborts"
echo "0000000000000000000000000000000000000000000000000000000000000000  other.tar.gz" >"$DL/checksums.txt"
H4="$SANDBOX/home4"
set +e
OUT=$(run_install "$H4" --no-open 2>&1)
CODE=$?
set -e
check "missing entry exits non-zero" '[[ $CODE -ne 0 ]] && grep -q "not listed in checksums.txt" <<<"$OUT"'
check "nothing installed on missing entry" '[[ ! -e "$H4/.local/bin/cwm" ]]'
cp "$SANDBOX/checksums.good" "$DL/checksums.txt"

echo "== 5. unknown version fails cleanly"
set +e
OUT=$(run_install "$SANDBOX/home5" --version v9.9.9 --no-open 2>&1)
CODE=$?
set -e
check "missing release exits non-zero" '[[ $CODE -ne 0 ]] && grep -q "download failed" <<<"$OUT"'

echo "== 6. --uninstall"
set +e
OUT=$(run_install "$H1" --uninstall 2>&1)
CODE=$?
set -e
printf '%s\n' "$OUT" | sed 's/^/    | /'
check "uninstall exits 0" '[[ $CODE -eq 0 ]]'
check "binary removed" '[[ ! -e "$H1/.local/bin/cwm" ]]'
check "PATH lines removed" '! grep -q "# added by cwm installer" "$H1/.profile" "$H1/.bashrc"'
check "service uninstall ran (dry run)" 'grep -q "would remove" <<<"$OUT"'
check "workspace kept" '[[ -f "$H1/.cwm/config.toml" ]] && grep -q "was kept" <<<"$OUT"'

# 7. install.ps1 logic under PowerShell 7 on this host (optional: needs
# pwsh, or PWSH=/path/to/pwsh). The "windows" zip carries this host's
# binary named cwm.exe, so the script's real code paths run end to end;
# user-Path edits are no-ops off Windows.
PWSH=${PWSH:-$(command -v pwsh || true)}
if [[ -n "$PWSH" ]]; then
    echo "== 7. install.ps1 under $("$PWSH" -NoProfile -c '$PSVersionTable.PSVersion.ToString()' 2>/dev/null)"
    WZIP="cwm_${VER}_windows_amd64.zip"
    mkdir -p "$SANDBOX/wbuild"
    cp "$SANDBOX/build/cwm" "$SANDBOX/wbuild/cwm.exe"
    cp "$SANDBOX/build/cwm" "$SANDBOX/wbuild/cwmd.exe"
    python3 -I -c 'import sys, zipfile
with zipfile.ZipFile(sys.argv[1], "w", zipfile.ZIP_DEFLATED) as z:
    for f in sys.argv[2:]:
        z.write(f, f.rsplit("/", 1)[-1])' "$DL/$WZIP" "$SANDBOX/wbuild/cwm.exe" "$SANDBOX/wbuild/cwmd.exe"
    (cd "$DL" && { sha256sum "$WZIP" 2>/dev/null || shasum -a 256 "$WZIP"; } >>checksums.txt)
    run_ps1() {
        local home=$1
        shift
        mkdir -p "$home"
        env -i HOME="$home" PATH="/usr/local/bin:/usr/bin:/bin" \
            DOTNET_CLI_TELEMETRY_OPTOUT=1 POWERSHELL_TELEMETRY_OPTOUT=1 \
            PROCESSOR_ARCHITECTURE=AMD64 LOCALAPPDATA="$home/AppData/Local" \
            XDG_CONFIG_HOME="$home/.config" XDG_RUNTIME_DIR="$home/run" CWM_SERVICE_DRYRUN=1 \
            CWM_INSTALL_API="http://127.0.0.1:$PORT" CWM_INSTALL_BASE_URL="http://127.0.0.1:$PORT/download" \
            "$@"
    }
    P1="$SANDBOX/ps-home1"
    set +e
    OUT=$(run_ps1 "$P1" env CWM_NO_OPEN=1 "$PWSH" -NoProfile -NonInteractive -File "$REPO/scripts/install.ps1" 2>&1)
    CODE=$?
    set -e
    printf '%s\n' "$OUT" | sed 's/^/    | /'
    check "ps1: install exits 0" '[[ $CODE -eq 0 ]]'
    check "ps1: checksum verified" 'grep -q "checksum OK" <<<"$OUT"'
    check "ps1: cwm.exe + cwmd.exe installed" '[[ -x "$P1/AppData/Local/cwm/bin/cwm.exe" && -f "$P1/AppData/Local/cwm/bin/cwmd.exe" ]]'
    check "ps1: cwm init ran" '[[ -f "$P1/.cwm/config.toml" ]]'
    check "ps1: service step ran (dry run)" 'grep -q "cwm service install (dry run" <<<"$OUT"'
    WSUM=$(awk -v f="$WZIP" '$2 == f {print $1}' "$DL/checksums.txt")
    check "ps1: banner + numbered steps + summary box" 'grep -q "Claude Workspace Manager .*cwm $TAG" <<<"$OUT" && grep -q "^\[3/8\] Verifying SHA-256" <<<"$OUT" && grep -q "| cwm $TAG is installed" <<<"$OUT" && grep -q "CWM_UNINSTALL=.1.; irm .*install.ps1 | iex" <<<"$OUT"'
    check "ps1: checksum shown as 12 chars, never all 64" 'grep -q "checksum OK ${WSUM:0:12}$" <<<"$OUT" && ! grep -q "$WSUM" <<<"$OUT"'
    check "ps1: banner art is scripts/banner.txt verbatim" 'banner_ok "$OUT"'
    check "sh and ps1 print the same banner art" '[[ -n "$SH_BANNER" && "$(banner_of "$OUT")" == "$SH_BANNER" ]]'
    check "ps1: child output indented, plain ASCII when redirected" 'grep -q "^      | ok cwm home initialized at" <<<"$OUT" && ! LC_ALL=C grep -q "[^[:print:][:space:]]" <<<"$OUT"'
    P4="$SANDBOX/ps-home4"
    set +e
    OUT=$(run_ps1 "$P4" env CWM_NO_OPEN=1 CWM_NO_SERVICE=1 CWM_INSTALL_FANCY=1 "$PWSH" -NoProfile -NonInteractive -File "$REPO/scripts/install.ps1" 2>&1)
    CODE=$?
    set -e
    check "ps1: terminal look uses unicode glyphs (non-Windows host), cwm matches" '[[ $CODE -eq 0 ]] && grep -q "✓ checksum OK" <<<"$OUT" && grep -q "╭" <<<"$OUT" && grep -q "│ ✓ cwm home initialized" <<<"$OUT"'
    check "ps1: terminal look keeps the banner art verbatim" '[[ "$(banner_of "$(sed "s/\x1b\[[0-9;]*m//g" <<<"$OUT")")" == "$(cat "$REPO/scripts/banner.txt")" ]]'
    # Narrow terminal: child output wraps under its gutter (install.ps1
    # prints no notes off Windows).
    P5="$SANDBOX/ps-narrow-home-with-a-deliberately-long-directory-name"
    set +e
    OUT=$(run_ps1 "$P5" env CWM_NO_OPEN=1 CWM_NO_SERVICE=1 COLUMNS=60 "$PWSH" -NoProfile -NonInteractive -File "$REPO/scripts/install.ps1" 2>&1)
    CODE=$?
    set -e
    check "ps1 narrow (COLUMNS=60): exits 0, no child line wider than 60 columns" '[[ $CODE -eq 0 ]] && [[ $(gutter_max "$OUT") -le 60 ]]'
    check "ps1 narrow: child line wraps, continuations keep \"      | \"" 'wrap_ok "$OUT" "      | " "ok cwm home initialized at" "ok cwm home initialized at $P5/.cwm"'
    check "ps1 narrow: banner art still verbatim" 'banner_ok "$OUT"'
    P6="$SANDBOX/ps-narrow-fancy-home-with-a-deliberately-long-directory-name"
    set +e
    OUT=$(run_ps1 "$P6" env CWM_NO_OPEN=1 CWM_NO_SERVICE=1 CWM_INSTALL_FANCY=1 COLUMNS=60 "$PWSH" -NoProfile -NonInteractive -File "$REPO/scripts/install.ps1" 2>&1)
    CODE=$?
    set -e
    check "ps1 narrow fancy: exits 0, no child line wider than 60 columns" '[[ $CODE -eq 0 ]] && [[ $(gutter_max "$OUT") -le 60 ]]'
    check "ps1 narrow fancy: continuations keep \"      │ \"" 'wrap_ok "$OUT" "      │ " "✓ cwm home initialized at" "✓ cwm home initialized at $P6/.cwm"'
    # irm | iex path: the script text through Invoke-Expression.
    P2="$SANDBOX/ps-home2"
    set +e
    OUT=$(run_ps1 "$P2" env CWM_NO_OPEN=1 CWM_NO_SERVICE=1 CWM_VERSION="$VER" "$PWSH" -NoProfile -NonInteractive -Command \
        "Get-Content -Raw -LiteralPath '$REPO/scripts/install.ps1' | Invoke-Expression" 2>&1)
    CODE=$?
    set -e
    check "ps1 via Invoke-Expression: exits 0, no service step" '[[ $CODE -eq 0 ]] && [[ -x "$P2/AppData/Local/cwm/bin/cwm.exe" ]] && ! grep -q "cwm service install" <<<"$OUT"'
    check "ps1 via Invoke-Expression: banner art verbatim" 'banner_ok "$OUT"'
    # API rate-limited (403): the releases/latest redirect names the tag.
    P7="$SANDBOX/ps-home-ratelimited"
    set +e
    OUT=$(run_ps1 "$P7" env CWM_NO_OPEN=1 CWM_NO_SERVICE=1 "${RATE_LIMITED[@]}" "$PWSH" -NoProfile -NonInteractive -File "$REPO/scripts/install.ps1" 2>&1)
    CODE=$?
    set -e
    printf '%s\n' "$OUT" | sed 's/^/    | /'
    check "ps1 rate-limited: exits 0, tag from the redirect, installed" '[[ $CODE -eq 0 ]] && grep -q "release $TAG (latest)" <<<"$OUT" && [[ -x "$P7/AppData/Local/cwm/bin/cwm.exe" ]] && "$P7/AppData/Local/cwm/bin/cwm.exe" --version | grep -q "$VER"'
    P8="$SANDBOX/ps-home-bothfail"
    set +e
    OUT=$(run_ps1 "$P8" env CWM_NO_OPEN=1 CWM_NO_SERVICE=1 "${BOTH_FAIL[@]}" "$PWSH" -NoProfile -NonInteractive -File "$REPO/scripts/install.ps1" 2>&1)
    CODE=$?
    set -e
    printf '%s\n' "$OUT" | sed 's/^/    | /'
    check "ps1 both fail: exits non-zero with both hints, nothing installed" '[[ $CODE -ne 0 ]] && grep -q "could not determine the latest release (.*403.*); pass -Version vX.Y.Z" <<<"$OUT" && grep -q "or set \$env:CWM_VERSION=.vX.Y.Z. first" <<<"$OUT" && [[ ! -e "$P8/AppData/Local/cwm/bin/cwm.exe" ]]'
    # Tampered checksum aborts.
    cp "$DL/checksums.txt" "$SANDBOX/checksums.ps1good"
    sed "s/^[0-9a-f]\{4\}\(.*$WZIP\)/0000\1/" "$SANDBOX/checksums.ps1good" >"$DL/checksums.txt"
    P3="$SANDBOX/ps-home3"
    set +e
    OUT=$(run_ps1 "$P3" env CWM_NO_OPEN=1 "$PWSH" -NoProfile -NonInteractive -File "$REPO/scripts/install.ps1" 2>&1)
    CODE=$?
    set -e
    check "ps1: tampered checksum aborts, nothing installed" '[[ $CODE -ne 0 ]] && grep -q "checksum mismatch" <<<"$OUT" && [[ ! -e "$P3/AppData/Local/cwm/bin/cwm.exe" ]]'
    cp "$SANDBOX/checksums.ps1good" "$DL/checksums.txt"
    set +e
    OUT=$(run_ps1 "$P1" "$PWSH" -NoProfile -NonInteractive -File "$REPO/scripts/install.ps1" -Uninstall 2>&1)
    CODE=$?
    set -e
    check "ps1: -Uninstall removes binaries, keeps workspace" '[[ $CODE -eq 0 ]] && [[ ! -e "$P1/AppData/Local/cwm/bin/cwm.exe" ]] && [[ -f "$P1/.cwm/config.toml" ]]'
else
    echo "== 7. install.ps1: skipped (no pwsh; set PWSH=/path/to/pwsh)"
fi

echo
echo "installer harness: $PASS passed, $FAIL failed"
[[ $FAIL -eq 0 ]]
