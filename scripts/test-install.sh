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
cleanup() {
    if [[ -n "$SERVER_PID" ]]; then kill "$SERVER_PID" 2>/dev/null || true; fi
    rm -rf "$SANDBOX"
}
trap cleanup EXIT

PASS=0
FAIL=0
ok() { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }
check() { if eval "$2"; then ok "$1"; else bad "$1"; fi; }

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

# run_install HOME_DIR ARGS... — install.sh in a clean environment.
run_install() {
    local home=$1
    shift
    mkdir -p "$home"
    env -i HOME="$home" PATH="/usr/local/bin:/usr/bin:/bin" \
        XDG_CONFIG_HOME="$home/.config" XDG_RUNTIME_DIR="$home/run" \
        CWM_SERVICE_DRYRUN=1 \
        CWM_INSTALL_API="http://127.0.0.1:$PORT" \
        CWM_INSTALL_BASE_URL="http://127.0.0.1:$PORT/download" \
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
check "resolved latest tag from the API" 'grep -q "installing cwm $TAG" <<<"$OUT"'
check "checksum verified" 'grep -q "checksum OK" <<<"$OUT"'
check "binary installed in ~/.local/bin" '[[ -x "$H1/.local/bin/cwm" ]]'
check "installed binary is the snapshot" '"$H1/.local/bin/cwm" --version | grep -q "$VER"'
check "PATH line added to ~/.profile" 'grep -q "# added by cwm installer" "$H1/.profile"'
check "cwm init ran (config.toml)" '[[ -f "$H1/.cwm/config.toml" ]]'
check "service step ran in dry-run mode" 'grep -q "cwm service install (dry run" <<<"$OUT"'
check "dry run printed the autostart plan" 'grep -q "would write $H1/.config/" <<<"$OUT"'
check "dry run wrote no autostart files" '[[ ! -e "$H1/.config/systemd" && ! -e "$H1/.config/autostart" ]]'
check "no agent was started (no server.toml)" '[[ ! -e "$H1/.cwm/server.toml" ]]'

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
        env -i HOME="$home" PATH="/usr/local/bin:/usr/bin:/bin" DOTNET_SYSTEM_GLOBALIZATION_INVARIANT=1 \
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
    # irm | iex path: the script text through Invoke-Expression.
    P2="$SANDBOX/ps-home2"
    set +e
    OUT=$(run_ps1 "$P2" env CWM_NO_OPEN=1 CWM_NO_SERVICE=1 CWM_VERSION="$VER" "$PWSH" -NoProfile -NonInteractive -Command \
        "Get-Content -Raw -LiteralPath '$REPO/scripts/install.ps1' | Invoke-Expression" 2>&1)
    CODE=$?
    set -e
    check "ps1 via Invoke-Expression: exits 0, no service step" '[[ $CODE -eq 0 ]] && [[ -x "$P2/AppData/Local/cwm/bin/cwm.exe" ]] && ! grep -q "cwm service install" <<<"$OUT"'
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
