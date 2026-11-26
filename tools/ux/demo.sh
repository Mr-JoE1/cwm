#!/usr/bin/env bash
# Demo dashboard for UX captures: builds cwm from this checkout, makes a
# throwaway HOME with invented data (demo-data.js), starts `cwm agent` on a
# free port and prints the env for capture.mjs. Never touches the real
# ~/.cwm or ~/.claude: the agent runs with env -i and HOME=<temp>.
#
#   tools/ux/demo.sh [--fresh|--legacy]      start; prints CWM_UX_BASE/HOME
#   tools/ux/demo.sh stop <demo-dir>         stop the agent it started
#
# Env: CWM_UX_DEMO (the demo dir; default a new mktemp dir), CWM_UX_PORT,
# CWM_UX_VERSION (default 0.9.0), NODE (default node), TZ (Europe/Berlin).
set -euo pipefail

if [ "${1:-}" = stop ]; then
    dir=${2:?usage: demo.sh stop <demo-dir>}
    if [ -f "$dir/agent.pid" ]; then kill "$(cat "$dir/agent.pid")" 2>/dev/null || true; rm -f "$dir/agent.pid"; echo "stopped"; fi
    exit 0
fi

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
NODE=${NODE:-node}
VERSION=${CWM_UX_VERSION:-0.9.0}
DEMO=${CWM_UX_DEMO:-$(mktemp -d "${TMPDIR:-/tmp}/cwm-ux-demo.XXXXXX")}
H="$DEMO/home"
BIN="$DEMO/bin/cwm"
mkdir -p "$DEMO/bin"

BI=github.com/Mr-JoE1/cwm/internal/buildinfo
commit=$(git -C "$ROOT" rev-parse --short=7 HEAD 2>/dev/null || echo unknown)
(cd "$ROOT" && go build -o "$BIN" -ldflags "-X $BI.Version=$VERSION -X github.com/Mr-JoE1/cwm/internal/cli.Version=$VERSION -X $BI.Commit=$commit -X $BI.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)" ./cmd/cwm)

case "${1:-}" in
    --fresh) "$NODE" "$ROOT/tools/ux/demo-data.js" "$H" "$BIN" --fresh ;;
    --legacy) LEGACY=1 "$NODE" "$ROOT/tools/ux/demo-data.js" "$H" "$BIN" ;;
    *) "$NODE" "$ROOT/tools/ux/demo-data.js" "$H" "$BIN" ;;
esac

PORT=${CWM_UX_PORT:-$("$NODE" -e 'const s=require("net").createServer().listen(0,"127.0.0.1",()=>{console.log(s.address().port);s.close()})')}
env -i HOME="$H" PATH=/usr/bin:/bin TZ="${TZ:-Europe/Berlin}" "$BIN" agent --port "$PORT" --no-open >"$DEMO/agent.log" 2>&1 &
echo $! >"$DEMO/agent.pid"
for _ in $(seq 1 100); do
    [ -s "$H/.cwm/server.toml" ] && curl -fs "http://127.0.0.1:$PORT/healthz" >/dev/null 2>&1 && break
    sleep 0.1
done
curl -fs "http://127.0.0.1:$PORT/healthz" >/dev/null || { echo "agent did not start:" >&2; cat "$DEMO/agent.log" >&2; exit 1; }

cat >"$DEMO/env.sh" <<EOF
export CWM_UX_BASE=http://127.0.0.1:$PORT
export CWM_UX_HOME=$H/.cwm
EOF
echo "CWM_UX_BASE=http://127.0.0.1:$PORT"
echo "CWM_UX_HOME=$H/.cwm"
echo "# source $DEMO/env.sh; node tools/ux/capture.mjs <outdir>"
echo "# stop: tools/ux/demo.sh stop $DEMO"
