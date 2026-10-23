#!/usr/bin/env bash
# Measure Wisp against the memory and latency budgets with the mock daemon. Builds nothing
# (`make perf` builds first). Private socket, data dirs and D-Bus under a temp
# dir; the UI runs offscreen. Writes the table to build/perf.txt.
# Env: WISP_BIN (default bin/), PERF_OUT, PERF_IDLE_SECS (20).
# Exit status: 0 if every budget passes, 1 if one fails, 2 on setup error.
set -uo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
BIN=${WISP_BIN:-$ROOT/bin}
OUT=${PERF_OUT:-$ROOT/build/perf.txt}
IDLE=${PERF_IDLE_SECS:-20}
BIG=61400009999@s.whatsapp.net
HZ=$(getconf CLK_TCK)

export TMPDIR=${TMPDIR:-$ROOT/build/tmp}
mkdir -p "$TMPDIR"
TMP=$(mktemp -d "${TMPDIR}/wisp-perf.XXXXXX")
SOCK=$TMP/wispd.sock
DPID="" UPID="" BUSPID=""
ROWS=()
FAILS=0

# shellcheck disable=SC2329  # invoked via trap
cleanup() {
  kill "$UPID" "$DPID" 2>/dev/null
  pkill -f -- "$SOCK" 2>/dev/null
  wait
  [[ -n $BUSPID ]] && kill "$BUSPID" 2>/dev/null
  rm -rf "$TMP"
}
trap cleanup EXIT
trap 'exit 130' INT TERM

die() { echo "perf: $*" >&2; exit 2; }
ctl() { "$BIN/wispctl" --socket "$SOCK" "$@"; }
rss() { awk '/^VmRSS:/{printf "%.1f", $2 / 1024}' "/proc/$1/status"; }
pss() { awk '/^Pss:/{printf "%.1f", $2 / 1024}' "/proc/$1/smaps_rollup"; }
ticks() { sed 's/^.*) //' "/proc/$1/stat" | awk '{print $12 + $13}'; }
csw() { cat "/proc/$1/task/"*/status 2>/dev/null | awk '/ctxt_switches/{n += $2} END{print n + 0}'; }
lt() { awk -v a="$1" -v b="$2" 'BEGIN{exit !(a + 0 < b + 0)}'; }
perf_val() { grep -oE "$1 [0-9.]+" "$TMP/ui.err" | head -1 | cut -d' ' -f2; }

# row METRIC VALUE UNIT [BUDGET]: PASS/FAIL against "< BUDGET", info without one.
row() {
  local status=info
  if [[ -n ${4:-} ]]; then
    if [[ -n $2 ]] && lt "$2" "$4"; then status=PASS; else status=FAIL; FAILS=$((FAILS + 1)); fi
  fi
  ROWS+=("$(printf '%-36s %8s %-3s %10s  %s' "$1" "${2:-n/a}" "$3" "${4:+< $4 $3}" "$status")")
}

for b in wispd wisp wispctl; do [[ -x $BIN/$b ]] || die "missing $BIN/$b (run make build)"; done
command -v jq >/dev/null || die "jq required"

export XDG_DATA_HOME=$TMP/data XDG_CACHE_HOME=$TMP/cache XDG_CONFIG_HOME=$TMP/config XDG_STATE_HOME=$TMP/state
mkdir -p "$XDG_DATA_HOME" "$XDG_CACHE_HOME" "$XDG_CONFIG_HOME" "$XDG_STATE_HOME"
export XDG_RUNTIME_DIR=$TMP/runtime
mkdir -m 700 "$XDG_RUNTIME_DIR"
mkdir -p "$XDG_DATA_HOME/wisp"
for asset in "$ROOT"/build/whatsapp/wallpaper-*.png "$ROOT"/build/whatsapp/Roboto*.woff2; do
  [[ ! -f $asset ]] || ln -s "$asset" "$XDG_DATA_HOME/wisp/"
done
export WISP_SOCKET=$SOCK
# Private session bus without service files: nothing gets D-Bus activated
# (a portal or ksecretd started here outlives the bus).
cat >"$TMP/bus.conf" <<EOF
<busconfig><type>session</type><listen>unix:path=$TMP/bus</listen><auth>EXTERNAL</auth>
<policy context="default"><allow send_destination="*" eavesdrop="true"/><allow eavesdrop="true"/><allow own="*"/></policy></busconfig>
EOF
if BUSINFO=$(dbus-daemon --config-file="$TMP/bus.conf" --fork --nopidfile --print-address=1 --print-pid=1 2>/dev/null); then
  DBUS_SESSION_BUS_ADDRESS=$(sed -n 1p <<<"$BUSINFO"); BUSPID=$(sed -n 2p <<<"$BUSINFO")
else
  DBUS_SESSION_BUS_ADDRESS=unix:path=$TMP/no-bus
fi
export DBUS_SESSION_BUS_ADDRESS

# Daemon, idle and after paging through the 10k-message Big Chat.
"$BIN/wispd" --mock --socket "$SOCK" --data "$TMP/data" >"$TMP/wispd.log" 2>&1 &
DPID=$!
for _ in $(seq 100); do ctl ping >/dev/null 2>&1 && break; sleep 0.05; done
ctl ping >/dev/null 2>&1 || die "wispd did not start: $(tail -3 "$TMP/wispd.log")"
sleep 5
row "daemon RSS, idle" "$(rss "$DPID")" MB 60
row "daemon PSS, idle" "$(pss "$DPID")" MB

before="" n=0
while :; do
  page=$(ctl call messages.list "$(jq -nc --arg c "$BIG" --arg b "$before" '{chat:$c,before:$b,limit:200}')") || die "messages.list failed"
  n=$((n + $(jq '.messages | length' <<<"$page")))
  [[ $(jq .hasMore <<<"$page") == true ]] || break
  before=$(jq -r '.messages[-1].id' <<<"$page")
done
sleep 2
row "daemon RSS, Big Chat loaded ($n)" "$(rss "$DPID")" MB 60
row "daemon PSS, Big Chat loaded" "$(pss "$DPID")" MB

# UI offscreen with Big Chat open.
QT_QPA_PLATFORM=offscreen QT_QUICK_BACKEND=software QT_ACCESSIBILITY=0 WISP_PERF=1 \
  "$BIN/wisp" --socket "$SOCK" --chat "$BIG" --screenshot "$TMP/window.png" --screenshot-delay "$(((IDLE + 60) * 1000))" >/dev/null 2>"$TMP/ui.err" &
UPID=$!
for _ in $(seq 200); do grep -q WISP_CHAT_OPEN "$TMP/ui.err" && break; sleep 0.05; done
row "WISP_READY (cold start)" "$(perf_val WISP_READY)" ms 1000
row "WISP_CHAT_OPEN (Big Chat)" "$(perf_val WISP_CHAT_OPEN)" ms 100
sleep 5
kill -0 "$UPID" 2>/dev/null || die "UI exited: $(tail -3 "$TMP/ui.err")"
row "UI RSS, idle, Big Chat open" "$(rss "$UPID")" MB 120
row "UI PSS, idle, Big Chat open" "$(pss "$UPID")" MB

# Idle CPU and context switches over $IDLE s.
c0u=$(ticks "$UPID") c0d=$(ticks "$DPID") s0u=$(csw "$UPID") s0d=$(csw "$DPID")
sleep "$IDLE"
pct() { awk -v d="$1" -v hz="$HZ" -v s="$IDLE" 'BEGIN{printf "%.2f", d / hz / s * 100}'; }
per_s() { awk -v d="$1" -v s="$IDLE" 'BEGIN{printf "%.2f", d / s}'; }
row "UI idle CPU (${IDLE} s)" "$(pct $(($(ticks "$UPID") - c0u)))" % 0.2
row "daemon idle CPU (${IDLE} s)" "$(pct $(($(ticks "$DPID") - c0d)))" % 0.2
row "UI context switches" "$(per_s $(($(csw "$UPID") - s0u)))" /s
row "daemon context switches" "$(per_s $(($(csw "$DPID") - s0d)))" /s
grep -qE 'ReferenceError|TypeError|is not a type|Cannot assign' "$TMP/ui.err" && row "UI QML errors" "$(grep -cE 'Error' "$TMP/ui.err")" "" 1

mkdir -p "$(dirname "$OUT")"
{
  echo "wisp perf $(date '+%F %T')  $(git -C "$ROOT" rev-parse --short HEAD)  mock, UI offscreen"
  printf '%s\n' "${ROWS[@]}"
  if ((FAILS == 0)); then echo "all budgets pass"; else echo "$FAILS over budget"; fi
} | tee "$OUT"
((FAILS == 0))
