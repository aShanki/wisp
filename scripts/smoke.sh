#!/usr/bin/env bash
# End-to-end smoke test: wispd --mock, RPC checks via wispctl, an offscreen UI
# screenshot. Builds nothing (`make smoke` builds first). Private socket, data
# dirs and D-Bus under a temp dir.
# Env: WISP_BIN (default bin/), SMOKE_KEEP=1 keeps the temp dir.
set -uo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
BIN=${WISP_BIN:-$ROOT/bin}
BIG=61400009999@s.whatsapp.net

export TMPDIR=${TMPDIR:-$ROOT/build/tmp}
mkdir -p "$TMPDIR"
TMP=$(mktemp -d "${TMPDIR}/wisp-smoke.XXXXXX")
SOCK=$TMP/wispd.sock
DPID="" EPID="" BUSPID=""
FAILS=0 PASSES=0

pass() { PASSES=$((PASSES + 1)); echo "PASS $*"; }
fail() { FAILS=$((FAILS + 1)); echo "FAIL $*"; }

# shellcheck disable=SC2329  # invoked via trap
cleanup() {
  kill "$EPID" "$DPID" 2>/dev/null
  pkill -f -- "$SOCK" 2>/dev/null
  wait
  [[ -n $BUSPID ]] && kill "$BUSPID" 2>/dev/null
  if [[ ${SMOKE_KEEP:-0} == 1 ]]; then echo "kept $TMP"; else rm -rf "$TMP"; fi
}
trap cleanup EXIT
trap 'exit 130' INT TERM

ctl() { "$BIN/wispctl" --socket "$SOCK" "$@"; }
rpc() { ctl call "$@"; }

for b in wispd wisp wispctl; do [[ -x $BIN/$b ]] || { fail "missing $BIN/$b (run make build)"; exit 1; }; done
command -v jq >/dev/null || { fail "missing tool: jq"; exit 1; }

export XDG_DATA_HOME=$TMP/data XDG_CACHE_HOME=$TMP/cache XDG_CONFIG_HOME=$TMP/config XDG_STATE_HOME=$TMP/state
mkdir -p "$XDG_DATA_HOME" "$XDG_CACHE_HOME" "$XDG_CONFIG_HOME" "$XDG_STATE_HOME"
export XDG_RUNTIME_DIR=$TMP/runtime
mkdir -m 700 "$XDG_RUNTIME_DIR"
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

echo "== wisp smoke test ($TMP)"

# --- daemon -----------------------------------------------------------------
"$BIN/wispd" --mock --socket "$SOCK" --data "$TMP/data" >"$TMP/wispd.log" 2>&1 &
DPID=$!
HELLO=""
for _ in $(seq 200); do
  HELLO=$(rpc daemon.hello '{"client":"smoke"}' 2>/dev/null) && break
  kill -0 "$DPID" 2>/dev/null || break
  sleep 0.05
done
if jq -e '.protocol == 1 and .mock == true' <<<"$HELLO" >/dev/null 2>&1; then
  pass "daemon.hello: $(jq -c '{protocol,mock,auth,connection}' <<<"$HELLO")"
else
  fail "wispd --mock did not answer: ${HELLO:0:200}"; tail -20 "$TMP/wispd.log"; exit 1
fi
ctl events >"$TMP/events.jsonl" 2>/dev/null &
EPID=$!

if out=$(rpc auth.status 2>&1) && jq -e '.state == "paired"' <<<"$out" >/dev/null; then
  pass "auth.status: paired"
else
  fail "auth.status: $out"
fi

if A=$(rpc chats.list '{"limit":1000}' 2>&1) && B=$(rpc chats.list '{"limit":1000,"archived":true}' 2>&1); then
  na=$(jq '.chats | length' <<<"$A"); ar=$(jq '.chats | length' <<<"$B")
  if ((na + ar >= 40 && na + ar <= 80)) && jq -e --arg c "$BIG" 'any(.chats[]; .jid == $c)' <<<"$A" >/dev/null; then
    pass "chats.list: $na chats + $ar archived, Big Chat present"
  else
    fail "chats.list: $na + $ar chats (expected ~50 incl. $BIG)"
  fi
  if jq -e '[.chats[].pinned] | . == (sort | reverse)' <<<"$A" >/dev/null; then
    pass "chats.list: pinned first"
  else
    fail "chats.list: pinned chats not first"
  fi
else
  fail "chats.list: ${A:0:200} ${B:0:200}"
fi
if p1=$(rpc chats.list '{"limit":10}' 2>&1) && cur=$(jq -er '.nextCursor | select(. != "")' <<<"$p1") &&
  p2=$(rpc chats.list "$(jq -nc --arg c "$cur" '{limit:10,cursor:$c}')" 2>&1) &&
  jq -en --argjson a "$p1" --argjson b "$p2" '($a.chats | length) == 10 and ([$a.chats[].jid] - [$b.chats[].jid] | length) == 10' >/dev/null; then
  pass "chats.list: cursor paging, no overlap"
else
  fail "chats.list paging: ${p1:0:200} ${p2:0:200}"
fi

# Page the whole Big Chat: newest first, no duplicates.
before="" ids="$TMP/ids" stamps="$TMP/stamps"
: >"$ids"; : >"$stamps"
while page=$(rpc messages.list "$(jq -nc --arg c "$BIG" --arg b "$before" '{chat:$c,before:$b,limit:200}')" 2>&1); do
  jq -r '.messages[].id' <<<"$page" >>"$ids"
  jq -r '.messages[].timestamp' <<<"$page" >>"$stamps"
  [[ $(jq .hasMore <<<"$page") == true ]] || break
  before=$(jq -r '.messages[-1].id' <<<"$page")
done
n=$(wc -l <"$ids")
if ((n >= 9000)) && [[ $(sort -u "$ids" | wc -l) == "$n" ]] && sort -rnc "$stamps" 2>/dev/null; then
  pass "messages.list: paged Big Chat, $n messages, newest first, no duplicates"
else
  fail "messages.list paging: $n messages, last page: ${page:0:200}"
fi

# Send: pending result, then message.new and status updates as events.
SEND_TEXT="smoke test $(date +%s)"
if r=$(rpc messages.send "$(jq -nc --arg c "$BIG" --arg t "$SEND_TEXT" '{chat:$c,text:$t}')" 2>&1) &&
  mid=$(jq -er '.message.id' <<<"$r"); then
  pass "messages.send: id=$mid status=$(jq -r .message.status <<<"$r")"
  ev() { jq -c --arg id "$mid" "select(.method == \"$1\" and .params.message.id == \$id)" "$TMP/events.jsonl" 2>/dev/null; }
  for _ in $(seq 80); do
    [[ $(ev message.update) == *'"status":"read"'* ]] && break
    sleep 0.1
  done
  if [[ -n $(ev message.new) ]]; then pass "messages.send: message.new"; else fail "messages.send: no message.new"; fi
  sts=$(ev message.update | jq -r .params.message.status | tr '\n' ' ')
  if [[ $sts == *read* ]]; then pass "messages.send: status ${sts% }"; else fail "messages.send: status updates '$sts'"; fi
  if g=$(rpc messages.list "$(jq -nc --arg c "$BIG" '{chat:$c,limit:5}')" 2>&1) &&
    jq -e --arg i "$mid" --arg t "$SEND_TEXT" 'any(.messages[]; .id == $i and .text == $t)' <<<"$g" >/dev/null; then
    pass "messages.list: sent message stored"
  else
    fail "messages.list after send: ${g:0:200}"
  fi
else
  fail "messages.send: ${r:0:200}"; mid=""
fi

if s=$(rpc search '{"query":"smok","limit":20}' 2>&1) && [[ -n $mid ]] &&
  jq -e --arg id "$mid" 'any(.results[]; .id == $id)' <<<"$s" >/dev/null; then
  pass "search: prefix finds the sent message"
else
  fail "search 'smok': ${s:0:200}"
fi
if s=$(rpc search "$(jq -nc --arg c "$BIG" '{query:"the",chat:$c,limit:10}')" 2>&1) &&
  jq -e --arg c "$BIG" '(.results | length > 0) and all(.results[]; .chat == $c)' <<<"$s" >/dev/null; then
  pass "search: restricted to one chat"
else
  fail "search with chat: ${s:0:200}"
fi

# Media: the first image in the Big Chat or one of the first chats.
img=""
for chat in "$BIG" $(jq -r '.chats[].jid' <<<"${A:-}" 2>/dev/null | head -15); do
  r=$(rpc messages.list "$(jq -nc --arg c "$chat" '{chat:$c,limit:200}')" 2>/dev/null) || continue
  img=$(jq -c 'first(.messages[] | select(.type == "image" and .media != null) | {chat, id})' <<<"$r")
  [[ -n $img ]] && break
done
if [[ -n $img ]]; then
  for m in media.thumb media.fetch; do
    if f=$(rpc "$m" "$img" 2>&1) && p=$(jq -er .path <<<"$f") && [[ -s $p ]]; then
      pass "$m: $(basename "$p") ($(stat -c %s "$p") bytes)"
    else
      fail "$m: ${f:0:200}"
    fi
  done
else
  fail "media: no image message found"
fi

if a=$(rpc avatars.get "$(jq -nc --arg j "$BIG" '{jid:$j}')" 2>&1) && ap=$(jq -r .path <<<"$a") && [[ -z $ap || -s $ap ]]; then
  pass "avatars.get: ${ap:-none}"
else
  fail "avatars.get: ${a:0:200}"
fi

if e=$(rpc no.such.method 2>&1); then
  fail "unknown method did not error: $e"
elif [[ $e == *"-32601"* ]]; then
  pass "unknown method: -32601"
else
  fail "unknown method: $e"
fi

# --- UI (offscreen) ---------------------------------------------------------
SHOT=$TMP/shot.png
QT_QPA_PLATFORM=offscreen QT_QUICK_BACKEND=software QT_ACCESSIBILITY=0 WISP_PERF=1 \
  timeout 30 "$BIN/wisp" --socket "$SOCK" --screenshot "$SHOT" --chat "$BIG" >/dev/null 2>"$TMP/ui.err"
urc=$?
if ((urc == 0)); then pass "UI: exited cleanly"; else fail "UI: exit status $urc"; fi
if [[ $(head -c 8 "$SHOT" 2>/dev/null | od -An -tx1 | tr -d ' \n') == 89504e470d0a1a0a ]]; then
  pass "UI: screenshot $(file -b "$SHOT" | grep -o '[0-9]* x [0-9]*')"
else
  fail "UI: no PNG screenshot"
fi
errs=$(grep -E 'ReferenceError|TypeError|is not a type|Cannot assign|SyntaxError|is not installed|failed to load component|Cannot read property' "$TMP/ui.err" | sort -u)
if [[ -z $errs ]]; then pass "UI: no QML errors"; else fail "UI: QML errors:"; head -30 <<<"$errs"; fi
grep -oE 'WISP_(READY|CHAT_OPEN) [0-9.]+' "$TMP/ui.err" | sed 's/^/     /;s/$/ ms/'
kill -0 "$DPID" 2>/dev/null || fail "daemon died while the UI was running"

# --- shutdown -----------------------------------------------------------------
if out=$(rpc daemon.shutdown 2>&1) && jq -e '.ok == true' <<<"$out" >/dev/null; then
  for _ in $(seq 50); do kill -0 "$DPID" 2>/dev/null || break; sleep 0.1; done
  if kill -0 "$DPID" 2>/dev/null; then
    fail "daemon.shutdown: still running after 5 s"
  else
    wait "$DPID"; drc=$?; DPID=""
    if ((drc == 0)); then pass "daemon.shutdown: exited 0"; else fail "daemon.shutdown: exit status $drc"; fi
  fi
else
  fail "daemon.shutdown: $out"
fi
if grep -qiE 'panic|fatal error|data race' "$TMP/wispd.log"; then
  fail "daemon log has panics:"; grep -iE -A5 'panic|fatal error|data race' "$TMP/wispd.log" | head -20
fi

echo
if ((FAILS == 0)); then echo "SMOKE PASS ($PASSES checks)"; exit 0; fi
echo "SMOKE FAIL ($FAILS failed, $PASSES passed)"
echo "--- wispd log"; tail -15 "$TMP/wispd.log"
echo "--- wisp stderr"; tail -15 "$TMP/ui.err"
exit 1
