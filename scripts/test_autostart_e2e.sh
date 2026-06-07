#!/usr/bin/env bash
# End-to-end test for the AutoStart-via-manualStart refactor.
#
# Spins up an isolated cfdmgrd on an alt port + data dir, exercises every
# behavioral change in the recent fix, and asserts on daemon logs + API
# responses. Does not touch the running dev environment.

set -uo pipefail

PORT=${TEST_PORT:-18080}
TOKEN=${TEST_TOKEN:-e2etest}
DATA=${TEST_DATA:-tmp/test-autostart}
BIN=${TEST_BIN:-./cfdmgrd-dev.exe}
BASE="http://127.0.0.1:${PORT}"
PASS=0
FAIL=0

cR='\033[31m'; cG='\033[32m'; cY='\033[33m'; cB='\033[36m'; cN='\033[0m'

pass() { PASS=$((PASS+1)); printf "  ${cG}✓${cN} %s\n" "$1"; }
fail() { FAIL=$((FAIL+1)); printf "  ${cR}✗${cN} %s\n" "$1"; }
note() { printf "  ${cY}…${cN} %s\n" "$1"; }
section() { printf "\n${cB}=== %s ===${cN}\n" "$1"; }

api() { curl -s -H "Authorization: Bearer ${TOKEN}" "$@"; }

DAEMON_PID=

start_daemon() {
  local marker=$1
  : > "${DATA}/daemon.log"
  CFDM_HTTP_ADDR=":${PORT}" \
  CFDM_API_TOKEN="${TOKEN}" \
  CFDM_DATA_DIR="${DATA}" \
  CFDM_LOG_LEVEL=debug \
  "${BIN}" serve >>"${DATA}/daemon.log" 2>&1 &
  DAEMON_PID=$!
  for _ in $(seq 1 50); do
    sleep 0.1
    if curl -fsS -H "Authorization: Bearer ${TOKEN}" "${BASE}/api/v1/health" >/dev/null 2>&1; then
      note "daemon up (${marker}) pid=${DAEMON_PID}"
      return 0
    fi
  done
  echo "daemon failed to start (${marker})"
  cat "${DATA}/daemon.log"
  exit 1
}

stop_daemon() {
  if [ -n "${DAEMON_PID}" ] && kill -0 "${DAEMON_PID}" 2>/dev/null; then
    kill "${DAEMON_PID}" 2>/dev/null || true
    wait "${DAEMON_PID}" 2>/dev/null || true
  fi
  DAEMON_PID=
}

trap 'stop_daemon' EXIT

# -------- preflight --------
[ -x "${BIN}" ] || { echo "missing binary: ${BIN}"; exit 1; }

# Make sure no stale process holds the port.
if curl -fsS "${BASE}/api/v1/health" -H "Authorization: Bearer ${TOKEN}" >/dev/null 2>&1; then
  echo "port ${PORT} already in use; aborting"
  exit 1
fi

rm -rf "${DATA}"
mkdir -p "${DATA}/profiles"

# Three fixtures:
#   auto-on   : no [cfdm] block at all → manualStart absent → autostart
#   auto-off  : [cfdm] manualStart=true → skip on boot
#   default   : [cfdm] only with name  → manualStart absent → autostart
# Server addr points at 127.0.0.1:1 so frp will fail to connect; that's
# fine — we assert on daemon's *intent to start*, not on the frp client
# actually staying up.
cat >"${DATA}/profiles/auto-on.toml" <<'EOF'
serverAddr = "127.0.0.1"
serverPort = 1

[cfdm]
name = "auto-on case"
EOF

cat >"${DATA}/profiles/auto-off.toml" <<'EOF'
serverAddr = "127.0.0.1"
serverPort = 1

[cfdm]
name = "auto-off case"
manualStart = true
EOF

cat >"${DATA}/profiles/default.toml" <<'EOF'
serverAddr = "127.0.0.1"
serverPort = 1

[cfdm]
name = "default case"
EOF

# Seed a legacy meta.json with a populated auto_start list to confirm
# the new daemon no longer relies on it. Also include a sort that does
# NOT mention default — to verify unknowns fall back to id-order tail.
cat >"${DATA}/meta.json" <<'EOF'
{
  "version": 1,
  "auto_start": ["should-be-ignored"],
  "sort": ["auto-off", "auto-on"]
}
EOF

# ============================================================
section "1) Cold boot: manualStart drives AutoStart"
# ============================================================
start_daemon "boot1"

# Give the AutoStart loop a moment to fire `instance started` lines.
sleep 0.5

LOG="${DATA}/daemon.log"

if grep -q 'msg="instance started" config_id=auto-on' "${LOG}"; then
  pass "auto-on (manualStart unset) was AutoStarted"
else
  fail "auto-on (manualStart unset) was NOT AutoStarted"
fi

if grep -q 'msg="instance started" config_id=default' "${LOG}"; then
  pass "default (manualStart unset) was AutoStarted"
else
  fail "default (manualStart unset) was NOT AutoStarted"
fi

if grep -q 'msg="instance started" config_id=auto-off' "${LOG}"; then
  fail "auto-off (manualStart=true) was AutoStarted (regression!)"
else
  pass "auto-off (manualStart=true) was skipped by AutoStart"
fi

# ============================================================
section "2) Legacy meta.auto_start is fully ignored"
# ============================================================
if grep -q 'msg="instance started" config_id=should-be-ignored' "${LOG}"; then
  fail "AutoStart still reads meta.auto_start (regression!)"
else
  pass "stale meta.auto_start entry 'should-be-ignored' had no effect"
fi

# ============================================================
section "3) Start/Stop no longer mutates meta.auto_start"
# ============================================================
# Snapshot meta before Start; the legacy list should stay unchanged.
META_BEFORE=$(cat "${DATA}/meta.json")

# Stop auto-on, then immediately start it back. Under old code this would
# remove then re-add it to meta.auto_start.
api -X POST "${BASE}/api/v1/configs/auto-on/stop" >/dev/null
api -X POST "${BASE}/api/v1/configs/auto-on/start" >/dev/null

# Read what auto_start looks like now.
AUTO_START_AFTER=$(python -c "import json; d=json.load(open('${DATA}/meta.json')); print(','.join(d.get('auto_start') or []))")

if [ "${AUTO_START_AFTER}" = "should-be-ignored" ]; then
  pass "meta.auto_start untouched by Start/Stop (still '${AUTO_START_AFTER}')"
else
  fail "meta.auto_start was mutated: '${AUTO_START_AFTER}' (expected 'should-be-ignored')"
fi

# ============================================================
section "4) PUT /configs/{id}: autoDelete bool → 400, object → 200"
# ============================================================
CODE=$(curl -s -o /dev/null -w "%{http_code}" \
  -X PUT "${BASE}/api/v1/configs/auto-on" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ${TOKEN}" \
  -d '{"config":{"serverAddr":"127.0.0.1","serverPort":1,"cfdm":{"name":"x","manualStart":false,"autoDelete":false}}}')
if [ "${CODE}" = "400" ]; then
  pass "PUT autoDelete:false  → 400 (strict decoder still rejects, as expected)"
else
  fail "PUT autoDelete:false  → ${CODE} (expected 400)"
fi

CODE=$(curl -s -o /dev/null -w "%{http_code}" \
  -X PUT "${BASE}/api/v1/configs/auto-on" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ${TOKEN}" \
  -d '{"config":{"serverAddr":"127.0.0.1","serverPort":1,"cfdm":{"name":"x","manualStart":false,"autoDelete":{"afterDate":"0001-01-01T00:00:00Z"}}}}')
if [ "${CODE}" = "200" ]; then
  pass "PUT autoDelete:{...} → 200"
else
  fail "PUT autoDelete:{...} → ${CODE} (expected 200)"
fi

CODE=$(curl -s -o /dev/null -w "%{http_code}" \
  -X PUT "${BASE}/api/v1/configs/auto-on" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ${TOKEN}" \
  -d '{"config":{"serverAddr":"127.0.0.1","serverPort":1,"cfdm":{"name":"x","manualStart":false}}}')
if [ "${CODE}" = "200" ]; then
  pass "PUT autoDelete omitted → 200 (front-end new payload shape)"
else
  fail "PUT autoDelete omitted → ${CODE} (expected 200)"
fi

# ============================================================
section "5) PUT manualStart=true persists into TOML"
# ============================================================
api -X PUT "${BASE}/api/v1/configs/auto-on" \
  -H "Content-Type: application/json" \
  -d '{"config":{"serverAddr":"127.0.0.1","serverPort":1,"cfdm":{"name":"flipped","manualStart":true}}}' >/dev/null

if grep -q '^manualStart = true' "${DATA}/profiles/auto-on.toml"; then
  pass "TOML file shows 'manualStart = true' after PUT"
else
  fail "TOML did not persist manualStart=true:"
  grep -A 5 '\[cfdm\]' "${DATA}/profiles/auto-on.toml" | sed 's/^/      /'
fi

# Reset auto-off back to manualStart=false via API for the next phase.
api -X PUT "${BASE}/api/v1/configs/auto-off" \
  -H "Content-Type: application/json" \
  -d '{"config":{"serverAddr":"127.0.0.1","serverPort":1,"cfdm":{"name":"flipped-off","manualStart":false}}}' >/dev/null

stop_daemon
sleep 0.3

# ============================================================
section "6) Restart honours the flipped flags"
# ============================================================
start_daemon "boot2"
sleep 0.5
LOG="${DATA}/daemon.log"

# After the flip: auto-on=true (don't start), auto-off=false (start), default=unset (start)
if grep -q 'msg="instance started" config_id=auto-on' "${LOG}"; then
  fail "auto-on (now manualStart=true) was AutoStarted (flag flip not respected)"
else
  pass "auto-on (now manualStart=true) skipped on boot"
fi
if grep -q 'msg="instance started" config_id=auto-off' "${LOG}"; then
  pass "auto-off (now manualStart=false) was AutoStarted on boot"
else
  fail "auto-off (now manualStart=false) was NOT AutoStarted on boot"
fi
if grep -q 'msg="instance started" config_id=default' "${LOG}"; then
  pass "default still AutoStarted (no regression)"
else
  fail "default NOT AutoStarted (regression!)"
fi

# ============================================================
section "7) AutoStart ordering follows meta.sort (auto-off before auto-on/default)"
# ============================================================
# meta.sort = ["auto-off", "auto-on"]; default is unknown → tail.
# auto-on is skipped here (manualStart=true), so we expect:
#   auto-off  first (sort idx 0)
#   default   second (unknown, id-order tail)
FIRST=$(grep 'msg="instance started"' "${LOG}" | head -1 | sed -n 's/.*config_id=\([a-zA-Z0-9_-]*\).*/\1/p')
SECOND=$(grep 'msg="instance started"' "${LOG}" | sed -n '2p' | sed -n 's/.*config_id=\([a-zA-Z0-9_-]*\).*/\1/p')

if [ "${FIRST}" = "auto-off" ]; then
  pass "first AutoStart was 'auto-off' (sort idx 0)"
else
  fail "first AutoStart was '${FIRST}' (expected auto-off)"
fi
if [ "${SECOND}" = "default" ]; then
  pass "second AutoStart was 'default' (unknown id, tail)"
else
  fail "second AutoStart was '${SECOND}' (expected default)"
fi

# ============================================================
section "8) markAutoStart / setAutoStart symbols are truly gone"
# ============================================================
# Ripgrep across internal/ to make sure no production code still calls them.
HITS=$(grep -RIn --include="*.go" --exclude="*.tmp.*" 'markAutoStart\|setAutoStart' internal/ 2>/dev/null || true)
if [ -z "${HITS}" ]; then
  pass "no internal/ Go file references markAutoStart/setAutoStart"
else
  fail "dead-code references remain:"
  printf '      %s\n' "${HITS}"
fi

stop_daemon

# ============================================================
section "Summary"
# ============================================================
printf "  ${cG}PASS=%d${cN}  ${cR}FAIL=%d${cN}\n" "${PASS}" "${FAIL}"
if [ "${FAIL}" -gt 0 ]; then
  echo
  echo "Last daemon log (tail):"
  tail -40 "${DATA}/daemon.log" | sed 's/^/    /'
  exit 1
fi
echo
echo "  Data dir: ${DATA}  (kept for inspection; delete manually if needed)"
exit 0
