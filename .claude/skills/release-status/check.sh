#!/bin/sh
# =============================================================================
# release-status 主脚本 —— 全模块发布状态快照 / 定时跟踪
#   用法:
#     bash check.sh              # 快照: CI runs + tags + release 资产
#     bash check.sh wait [TAG]   # 定时跟踪到 TAG（默认最新 tag）release 完整发布
#
# 设计原则（针对 Windows git-bash 环境踩过的坑）:
#   1) 不用 /tmp 硬编码 —— git-bash 的 /tmp ≠ Windows Python 的 /tmp
#      → 用 mktemp 拿到双方都能解析的真实路径
#   2) Python 读 GitHub API JSON 必须 encoding='utf-8'
#      → Windows 默认 GBK, 不指定就 UnicodeDecodeError
#   3) GitHub API 匿名速率限 60 req/h —— 跑多了就 403
#      → fallback 用 HEAD 探测 https://github.com/.../releases/tag/v...
#        release page 200 + asset URL 302 = 已完整发布（与 API 一致）
#   4) curl 在国内偶发 SSL peer failure
#      → 关键步骤加 --retry 2，并在 fallback 路径覆盖
# =============================================================================

set -eu

REPO="${REPO:-nue-mic/frps-manager}"
BIN_NAME="${BIN_NAME:-frpsmgrd}"           # 资产前缀, 用于 HEAD fallback 探测
API="https://api.github.com/repos/${REPO}"
WEB="https://github.com/${REPO}"
EXPECTED_ASSETS="${EXPECTED_ASSETS:-14}"   # frps-manager .goreleaser 实际产出: 13 平台 + checksums.txt

# 颜色（管道里禁用）
if [ -t 1 ]; then
    GRN=$(printf '\033[0;32m'); YLW=$(printf '\033[0;33m'); RED=$(printf '\033[0;31m')
    BLU=$(printf '\033[0;34m'); BOLD=$(printf '\033[1m'); RST=$(printf '\033[0m')
else
    GRN=; YLW=; RED=; BLU=; BOLD=; RST=
fi

# 自动清理临时目录
TMP="$(mktemp -d 2>/dev/null || mktemp -d -t rs-XXXXXX)"
trap 'rm -rf "$TMP"' EXIT

# Windows git-bash 下 mktemp 返回的是 /c/Users/... 风格路径，
# Python 直接接受没问题；为保险给 python 时用绝对正斜杠形式
py_path() {
    # cygpath 在 git-bash 自带，转 windows 风格让 python 100% 认
    if command -v cygpath >/dev/null 2>&1; then cygpath -m "$1"
    else echo "$1"
    fi
}

api_get() {
    # api_get <api 子路径> <out 文件>
    # 返回 0=200 命中, 1=403/速率限, 2=404/其它失败
    _u="${API}$1"; _o="$2"
    _http=$(curl -fsSL --retry 2 -o "$_o" -w "%{http_code}" "$_u" 2>/dev/null || echo "000")
    case "$_http" in
        200) return 0 ;;
        403) return 1 ;;
        *)   return 2 ;;
    esac
}

head_status() {
    # head_status <完整 URL> → 输出 HTTP code
    curl -fsSL -o /dev/null --retry 2 -w "%{http_code}" -I "$1" 2>/dev/null || echo "000"
}

# ---------------------------------------------------------------------------
# 表 A — 最近 N 次 CI runs
# ---------------------------------------------------------------------------
print_runs_table() {
    _f="$TMP/runs.json"; _p="$(py_path "$_f")"
    if api_get "/actions/runs?per_page=12" "$_f"; then
        PYTHONIOENCODING=utf-8 python -c "
import json
d = json.load(open(r'$_p', encoding='utf-8'))
rows = d['workflow_runs'][:12]
print('  {:10} {:14} {:10} {}'.format('工作流','状态','结论','commit'))
print('  ' + '-' * 50)
for r in rows:
    st = r['status']; cc = r.get('conclusion') or '-'
    badge = '🟢' if cc=='success' else ('🔴' if cc=='failure' else ('🔵' if st=='in_progress' else '⚪'))
    print('  {} {:8} {:14} {:10} {}'.format(badge, r['name'], st, cc, r['head_sha'][:7]))
"
    else
        printf "  ${YLW}API 速率限 (403), 跳过 CI runs 表${RST}\n"
    fi
}

# ---------------------------------------------------------------------------
# 表 B — 最近 5 个 tag 的发布状态（API 优先 + HEAD fallback）
# ---------------------------------------------------------------------------
list_recent_tags() {
    # 用 git ls-remote 直查目标仓库 URL（不耗 API 配额，跨仓库也稳）
    # 不用本地 origin：当前 cwd 可能在另一个仓库，origin 会指错
    git ls-remote --tags "${WEB}.git" 2>/dev/null \
        | grep -v '\^{}' \
        | sed 's#.*refs/tags/##' \
        | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' \
        | sort -V \
        | tail -5
}

# 查 release 详情 (API 200) → 输出: <资产数> <draft|released>
# fallback: HEAD 探测 → 输出: <"~">  <released?|missing>
inspect_release() {
    _tag="$1"
    _f="$TMP/r_${_tag}.json"; _p="$(py_path "$_f")"
    if api_get "/releases/tags/${_tag}" "$_f"; then
        PYTHONIOENCODING=utf-8 python -c "
import json
d = json.load(open(r'$_p', encoding='utf-8'))
a = d.get('assets', [])
st = 'draft' if d.get('draft') else 'released'
print('{} {}'.format(len(a), st))
"
    else
        # HEAD fallback: release page + 1 个标志性 asset
        page=$(head_status "${WEB}/releases/tag/${_tag}")
        asset=$(head_status "${WEB}/releases/download/${_tag}/${BIN_NAME}_${_tag#v}_linux_amd64.tar.gz")
        if [ "$page" = "200" ] && [ "$asset" = "302" ]; then
            echo "~ released"   # 资产数未知
        elif [ "$page" = "200" ]; then
            echo "~ no-assets"
        else
            echo "0 missing"
        fi
    fi
}

print_tags_table() {
    printf "  ${BOLD}%-10s %8s %12s${RST}\n" "Tag" "资产数" "状态"
    printf "  %s\n" "------------------------------------"
    for tag in $(list_recent_tags); do
        # shellcheck disable=SC2046
        set -- $(inspect_release "$tag")
        count="$1"; status="$2"
        case "$status" in
            released)
                if [ "$count" = "~" ]; then
                    badge="🟢"; note="(HEAD 探测)"
                elif [ "$count" -ge "$EXPECTED_ASSETS" ] 2>/dev/null; then
                    badge="🟢"; note=""
                else
                    badge="🟡"; note="(资产不足期望 $EXPECTED_ASSETS)"
                fi
                ;;
            draft)     badge="📝"; note="(草稿)" ;;
            no-assets) badge="🟡"; note="(无资产)" ;;
            *)         badge="🔴"; note="(缺失)" ;;
        esac
        printf "  %s %-10s %8s %12s %s\n" "$badge" "$tag" "$count" "$status" "$note"
    done
}

# ---------------------------------------------------------------------------
# 表 C — Docker 镜像 (GHCR) 探测
# ---------------------------------------------------------------------------
print_docker_status() {
    # GHCR manifest 头需要 token, 用 HEAD 探测公共镜像页面更省事
    _url="https://github.com/${REPO}/pkgs/container/$(basename "$REPO")"
    code=$(head_status "$_url")
    if [ "$code" = "200" ]; then
        printf "  🟢 GHCR 镜像页可访问: %s\n" "$_url"
        printf "     最新 tag: ${BOLD}ghcr.io/%s:latest${RST}\n" "$REPO"
    else
        printf "  🟡 GHCR 探测 HTTP %s (不一定异常, 可能登录需要)\n" "$code"
    fi
}

# ---------------------------------------------------------------------------
# 快照模式
# ---------------------------------------------------------------------------
mode_snapshot() {
    printf "${BOLD}${BLU}┌─ 发布状态快照: %s ─┐${RST}\n" "$REPO"
    printf "\n${BOLD}A) 最近 CI runs${RST}\n"
    print_runs_table

    printf "\n${BOLD}B) 最近 5 个 tag → release${RST}  (期望资产 ≥ %s)\n" "$EXPECTED_ASSETS"
    print_tags_table

    printf "\n${BOLD}C) Docker 镜像${RST}\n"
    print_docker_status

    printf "\n${BOLD}图例${RST}: 🟢 完成 / 🔵 进行中 / 🟡 部分 / 🔴 失败 / 📝 草稿 / ~ 未知(用 HEAD 探测)\n"
}

# ---------------------------------------------------------------------------
# 跟踪模式: 定时轮询到 TAG release 完整发布
# ---------------------------------------------------------------------------
mode_wait() {
    _tag="${1:-}"
    if [ -z "$_tag" ]; then
        _tag=$(list_recent_tags | tail -1)
        printf "未指定 tag, 用最新: ${BOLD}%s${RST}\n" "$_tag"
    fi
    _interval=30   # 秒
    _maxiter=30    # × 30s = 15 分钟上限
    _i=0
    printf "${BOLD}等待 %s release 完整发布 (≥ %s 资产) ...${RST}\n\n" "$_tag" "$EXPECTED_ASSETS"
    while [ "$_i" -lt "$_maxiter" ]; do
        _i=$((_i + 1))
        # shellcheck disable=SC2046
        set -- $(inspect_release "$_tag")
        count="$1"; status="$2"
        ts=$(date '+%H:%M:%S')
        case "$status" in
            released)
                if [ "$count" = "~" ]; then
                    printf "  [%s] %s 🟢 released (HEAD 探测, 资产已上传)\n" "$ts" "$_tag"
                    printf "\n${GRN}${BOLD}✅ %s 已完整发布${RST}\n" "$_tag"
                    printf "   下载页: %s/releases/tag/%s\n" "$WEB" "$_tag"
                    return 0
                elif [ "$count" -ge "$EXPECTED_ASSETS" ] 2>/dev/null; then
                    printf "  [%s] %s 🟢 released, 资产 %s\n" "$ts" "$_tag" "$count"
                    printf "\n${GRN}${BOLD}✅ %s 已完整发布${RST}\n" "$_tag"
                    printf "   下载页: %s/releases/tag/%s\n" "$WEB" "$_tag"
                    return 0
                else
                    printf "  [%s] %s 🟡 released 但资产仅 %s (期望 ≥ %s), 继续等...\n" \
                        "$ts" "$_tag" "$count" "$EXPECTED_ASSETS"
                fi
                ;;
            draft)     printf "  [%s] %s 📝 草稿中...\n"   "$ts" "$_tag" ;;
            no-assets) printf "  [%s] %s 🟡 release 已创建, 资产未上传...\n" "$ts" "$_tag" ;;
            *)         printf "  [%s] %s 🔵 未生成 (CI 仍在跑)...\n" "$ts" "$_tag" ;;
        esac
        sleep "$_interval"
    done
    printf "\n${YLW}⏰ 已等待 %s 秒仍未完成${RST}\n" $((_maxiter * _interval))
    printf "   手动查: %s/actions\n" "$WEB"
    return 1
}

# ---------------------------------------------------------------------------
# 入口
# ---------------------------------------------------------------------------
case "${1:-snapshot}" in
    wait)         shift; mode_wait "$@" ;;
    snapshot|"")  mode_snapshot ;;
    -h|--help|help)
        cat <<EOF
release-status —— frpc-manager 全模块发布状态查询
用法:
  bash check.sh              # 快照: 列 CI runs + tags + Docker
  bash check.sh wait [TAG]   # 定时跟踪 (默认最新 tag), 30s/次, 上限 15 分钟
环境变量:
  REPO              默认 nue-mic/frpc-manager
  EXPECTED_ASSETS   release 期望资产数, 默认 19
EOF
        ;;
    *) printf "${RED}未知命令: %s${RST} (用 -h 看帮助)\n" "$1"; exit 2 ;;
esac
