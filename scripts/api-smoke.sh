#!/usr/bin/env bash
# API 烟测：覆盖鉴权/Configs CRUD/Validate/Lifecycle/Runtime/Metrics/Alerts/
# Logs/System/Import-Export/已删端点。
#
# 用法（需要 daemon 在 BASE 上跑、TOKEN 是其 API token）：
#   BASE=http://127.0.0.1:8088 TOKEN=dev bash scripts/api-smoke.sh
# 默认值：BASE=http://127.0.0.1:8088, TOKEN=dev
B="${BASE:-http://127.0.0.1:8088}/api/v1"
H_AUTH="Authorization: Bearer ${TOKEN:-dev}"
H_BAD="Authorization: Bearer wrong-token"
H_JSON="Content-Type: application/json"
H_TOML="Content-Type: application/toml"

PASS=0
FAIL=0
FAILED_TESTS=()

check() {
  local name=$1 expected=$2; shift 2
  local got
  got=$(curl -s -o /dev/null -w '%{http_code}' "$@")
  if [ "$got" = "$expected" ]; then
    printf '  ✅ %-52s [%s]\n' "$name" "$got"
    PASS=$((PASS+1))
  else
    printf '  ❌ %-52s [got=%s want=%s]\n' "$name" "$got" "$expected"
    FAIL=$((FAIL+1))
    FAILED_TESTS+=("$name (got=$got want=$expected)")
  fi
}

check_contains() {
  local name=$1 expected=$2 substr=$3; shift 3
  local resp status body
  resp=$(curl -s -w '\n%{http_code}' "$@")
  status=$(echo "$resp" | tail -1)
  body=$(echo "$resp" | sed '$d')
  if [ "$status" = "$expected" ] && echo "$body" | grep -q "$substr"; then
    printf '  ✅ %-52s [%s, contains %q]\n' "$name" "$status" "$substr"
    PASS=$((PASS+1))
  else
    printf '  ❌ %-52s [status=%s want=%s]\n' "$name" "$status" "$expected"
    FAIL=$((FAIL+1))
    FAILED_TESTS+=("$name")
  fi
}

section() { echo; echo "==== $1 ===="; }

section "1. 鉴权"
check "health 无鉴权 200" 200 "$B/health"
check "version 无鉴权 401" 401 "$B/version"
check "version 错 token 401" 401 -H "$H_BAD" "$B/version"
check "version 正确 token 200" 200 -H "$H_AUTH" "$B/version"

section "2. Configs CRUD"
check "POST /configs 201" 201 -H "$H_AUTH" -H "$H_JSON" -X POST "$B/configs" \
  -d '{"id":"srv1","config":{"bindPort":7301,"vhostHTTPPort":8081},"cfdm":{"name":"测试1","manualStart":false}}'
check "POST /configs 重复 409" 409 -H "$H_AUTH" -H "$H_JSON" -X POST "$B/configs" \
  -d '{"id":"srv1","config":{"bindPort":7000},"cfdm":{}}'
check "POST /configs 缺 id 400" 400 -H "$H_AUTH" -H "$H_JSON" -X POST "$B/configs" -d '{"config":{"bindPort":7000}}'
check "POST /configs 非法 id 400" 400 -H "$H_AUTH" -H "$H_JSON" -X POST "$B/configs" \
  -d '{"id":"bad/id","config":{"bindPort":7000},"cfdm":{}}'
check "POST /configs 未知字段 400 (DisallowUnknownFields)" 400 -H "$H_AUTH" -H "$H_JSON" -X POST "$B/configs" \
  -d '{"id":"x","config":{"bindPort":7000},"cfdm":{},"hacker":true}'
check_contains "GET /configs 列表" 200 '"id":"srv1"' -H "$H_AUTH" "$B/configs"
check_contains "GET /configs/srv1 含 bindPort" 200 '"bindPort":7301' -H "$H_AUTH" "$B/configs/srv1"
check "GET /configs/nonexist 404" 404 -H "$H_AUTH" "$B/configs/nonexist"
check "PUT /configs/srv1 200" 200 -H "$H_AUTH" -H "$H_JSON" -X PUT "$B/configs/srv1" \
  -d '{"config":{"bindPort":7301,"vhostHTTPPort":8081,"subDomainHost":"example.com"},"cfdm":{"name":"测试1","manualStart":false}}'
check_contains "PUT 后 subDomainHost 已更新" 200 '"subDomainHost":"example.com"' -H "$H_AUTH" "$B/configs/srv1"
check "PATCH /configs/srv1 200" 200 -H "$H_AUTH" -H "$H_JSON" -X PATCH "$B/configs/srv1" \
  -d '{"vhostHTTPSPort":8443}'
check_contains "PATCH 后含 vhostHTTPSPort" 200 '"vhostHTTPSPort":8443' -H "$H_AUTH" "$B/configs/srv1"
check "POST /configs/srv1/duplicate 201" 201 -H "$H_AUTH" -H "$H_JSON" -X POST "$B/configs/srv1/duplicate" \
  -d '{"new_id":"srv1_copy"}'
check_contains "副本字段沿用" 200 '"bindPort":7301' -H "$H_AUTH" "$B/configs/srv1_copy"
check "POST /configs/reorder 204" 204 -H "$H_AUTH" -H "$H_JSON" -X POST "$B/configs/reorder" \
  -d '{"order":["srv1_copy","srv1"]}'
check_contains "GET /configs/srv1/raw 是 cloudflared 配置 YAML" 200 'bindPort = 7301' -H "$H_AUTH" "$B/configs/srv1/raw"
check "PUT /configs/srv1/raw 合法 TOML 200" 200 -H "$H_AUTH" -H "$H_TOML" -X PUT "$B/configs/srv1/raw" \
  --data-binary 'bindPort = 7301
vhostHTTPPort = 8081
'
check "PUT /configs/srv1/raw 非法 TOML 400" 400 -H "$H_AUTH" -H "$H_TOML" -X PUT "$B/configs/srv1/raw" \
  --data-binary 'this is = = ='

section "3. Validate"
check_contains "POST /validate JSON 合法 valid:true" 200 '"valid":true' -H "$H_AUTH" -H "$H_JSON" -X POST "$B/validate" -d '{"bindPort":7000}'
check_contains "POST /validate JSON 非法端口 valid:false" 200 '"valid":false' -H "$H_AUTH" -H "$H_JSON" -X POST "$B/validate" -d '{"bindPort":99999999}'
check_contains "POST /validate TOML 合法 valid:true" 200 '"valid":true' -H "$H_AUTH" -H "$H_TOML" -X POST "$B/validate" --data-binary 'bindPort = 7000'

section "4. Lifecycle"
check "POST /configs/srv1/start 200" 200 -H "$H_AUTH" -X POST "$B/configs/srv1/start"
sleep 2
check_contains "GET /configs/srv1/status started" 200 '"state":"started"' -H "$H_AUTH" "$B/configs/srv1/status"
check "已运行再 start 409" 409 -H "$H_AUTH" -X POST "$B/configs/srv1/start"
check "POST /configs/srv1/reload 200" 200 -H "$H_AUTH" -X POST "$B/configs/srv1/reload"
sleep 2
check_contains "reload 后仍 started" 200 '"state":"started"' -H "$H_AUTH" "$B/configs/srv1/status"
check "POST /configs/nonexist/start 404" 404 -H "$H_AUTH" -X POST "$B/configs/nonexist/start"

section "5. Runtime"
check_contains "GET /runtime/srv1/overview 含 bindPort" 200 'bindPort' -H "$H_AUTH" "$B/runtime/srv1/overview"
check_contains "GET /runtime/srv1/proxies 空数组" 200 '"proxies":\[' -H "$H_AUTH" "$B/runtime/srv1/proxies"
check "GET /runtime/srv1/proxies/foo 404 (proxy 不存在)" 404 -H "$H_AUTH" "$B/runtime/srv1/proxies/foo"
check "GET /runtime/srv1/clients 200" 200 -H "$H_AUTH" "$B/runtime/srv1/clients"
check "GET /runtime/srv1_copy/overview 409 (未运行)" 409 -H "$H_AUTH" "$B/runtime/srv1_copy/overview"
check "GET /runtime/srv1_copy/proxies/x 409 (未运行)" 409 -H "$H_AUTH" "$B/runtime/srv1_copy/proxies/x"
check "GET /runtime/nonexist/overview 404" 404 -H "$H_AUTH" "$B/runtime/nonexist/overview"

section "6. Metrics 历史流量"
sleep 8  # 让采样器至少跑一轮（10s 间隔）
NOW=$(date +%s)
check_contains "GET /metrics/srv1/traffic 有 points 数组" 200 '"points":\[' -H "$H_AUTH" "$B/metrics/srv1/traffic?to=$((NOW+10))&step=10"
check "GET /metrics/srv1/traffic 缺 to 400" 400 -H "$H_AUTH" "$B/metrics/srv1/traffic"

section "7. Alerts"
check_contains "POST /alerts 201 含 id" 201 '"id":' -H "$H_AUTH" -H "$H_JSON" -X POST "$B/alerts" \
  -d '{"name":"测试规则","enabled":true,"metric":"conns","op":">=","threshold":0,"for_seconds":0}'
check "POST /alerts 缺 metric 400" 400 -H "$H_AUTH" -H "$H_JSON" -X POST "$B/alerts" -d '{"name":"x"}'
check_contains "GET /alerts 列表" 200 '"items":' -H "$H_AUTH" "$B/alerts"
RULE_ID=$(curl -s -H "$H_AUTH" "$B/alerts" | grep -oE '"id":"rule_[a-z0-9]+"' | head -1 | sed 's/.*:"\(.*\)"/\1/')
check "GET /alerts/{id} 200" 200 -H "$H_AUTH" "$B/alerts/$RULE_ID"
check "PUT /alerts/{id} 200" 200 -H "$H_AUTH" -H "$H_JSON" -X PUT "$B/alerts/$RULE_ID" \
  -d '{"name":"改","enabled":true,"metric":"conns","op":">","threshold":100,"for_seconds":30}'
check "GET /alerts/nonexist 404" 404 -H "$H_AUTH" "$B/alerts/nonexist"
check_contains "GET /alerts/events" 200 '"items":' -H "$H_AUTH" "$B/alerts/events"
check "DELETE /alerts/{id} 204" 204 -H "$H_AUTH" -X DELETE "$B/alerts/$RULE_ID"

section "8. Logs"
check_contains "GET /configs/srv1/logs lines" 200 '"lines":' -H "$H_AUTH" "$B/configs/srv1/logs"
check_contains "GET /configs/srv1/logs/files items" 200 '"items":' -H "$H_AUTH" "$B/configs/srv1/logs/files"
check "DELETE /configs/srv1/logs 204" 204 -H "$H_AUTH" -X DELETE "$B/configs/srv1/logs"
check "DELETE /configs/nonexist/logs 404" 404 -H "$H_AUTH" -X DELETE "$B/configs/nonexist/logs"

section "9. System"
check_contains "GET /system/info uptime_s" 200 '"uptime_s":' -H "$H_AUTH" "$B/system/info"
check "GET /system/cpu 200" 200 -H "$H_AUTH" "$B/system/cpu"
check "GET /system/memory 200" 200 -H "$H_AUTH" "$B/system/memory"
check "GET /system/disk 200" 200 -H "$H_AUTH" "$B/system/disk"
check "GET /system/network 200" 200 -H "$H_AUTH" "$B/system/network"
check "GET /system/process 200" 200 -H "$H_AUTH" "$B/system/process"

section "10. Import/Export"
check "GET /configs/srv1/export 200 TOML" 200 -H "$H_AUTH" "$B/configs/srv1/export"
check "GET /export/all 200 ZIP" 200 -H "$H_AUTH" "$B/export/all"
check "POST /import/text 200" 200 -H "$H_AUTH" -H "$H_JSON" -X POST "$B/import/text" \
  -d '{"id":"imported","text":"bindPort = 7305\n"}'
check "POST /import/text 缺 text 400" 400 -H "$H_AUTH" -H "$H_JSON" -X POST "$B/import/text" -d '{"id":"x"}'
check_contains "导入后 GET 字段对齐" 200 '"bindPort":7305' -H "$H_AUTH" "$B/configs/imported"

section "11. 已删端点"
check "GET /configs/srv1/proxies 404" 404 -H "$H_AUTH" "$B/configs/srv1/proxies"
check "POST /nathole/discover 405" 405 -H "$H_AUTH" -X POST "$B/nathole/discover"

section "12. 清理"
check "POST /configs/srv1/stop 200" 200 -H "$H_AUTH" -X POST "$B/configs/srv1/stop"
sleep 2
check_contains "stop 后 stopped" 200 '"state":"stopped"' -H "$H_AUTH" "$B/configs/srv1/status"
check "DELETE /configs/srv1 204" 204 -H "$H_AUTH" -X DELETE "$B/configs/srv1"
check "DELETE 已删的 404" 404 -H "$H_AUTH" -X DELETE "$B/configs/srv1"
check "DELETE /configs/srv1_copy 204" 204 -H "$H_AUTH" -X DELETE "$B/configs/srv1_copy"
check "DELETE /configs/imported 204" 204 -H "$H_AUTH" -X DELETE "$B/configs/imported"

echo
echo "============================================================"
echo "Summary: PASS=$PASS  FAIL=$FAIL"
if [ $FAIL -gt 0 ]; then
  echo "Failed tests:"
  for t in "${FAILED_TESTS[@]}"; do echo "  - $t"; done
fi
echo "============================================================"
exit $FAIL
