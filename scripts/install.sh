#!/bin/sh
# =============================================================================
# cfdmgrd 一键安装脚本 (cloudflared-manager)
#
#   支持: macOS / 各类 Linux (systemd / OpenRC / 通用回退)
#   下载: 自动选择 curl 或 wget
#   功能: 自动识别系统架构 -> 下载对应二进制 -> 安装 -> 注册系统服务 -> 开机自启
#
# 一行安装 (推荐, 支持交互):
#   sh -c "$(curl -fsSL https://raw.githubusercontent.com/mia-clark/cloudflared-manager/main/scripts/install.sh)"
#   sh -c "$(wget -qO- https://raw.githubusercontent.com/mia-clark/cloudflared-manager/main/scripts/install.sh)"
#
# 非交互 / 自定义示例:
#   sh install.sh --yes --port 9000 --token mysecret
#   sh install.sh --port random
#   sh install.sh --uninstall
#
# 环境变量 (等价于命令行参数, 便于自动化):
#   CFDM_PORT=9000  CFDM_API_TOKEN=xxx  CFDM_VERSION=v1.2.10  ASSUME_YES=1
# =============================================================================

set -eu

# ----------------------------------------------------------------------------
# 常量配置
# ----------------------------------------------------------------------------
REPO="mia-clark/cloudflared-manager"
BIN_NAME="cfdmgrd"
INSTALL_DIR="/usr/local/bin"
SERVICE_NAME="cfdmgrd"
DEFAULT_PORT="8080"

# ----------------------------------------------------------------------------
# GitHub release 下载代理候选 (按用户指定顺序: 公开4家在前, 自建6家在后)
#   - URL 拼装格式: ${PROXY}https://github.com/USER/REPO/releases/download/...
#   - 安装时按此顺序挨个尝试; 每家失败/伪200自动跳下一家; 全失败回落直连
#   - 数据基于 2026-06-05 实测（与姊妹仓库 frpc-manager 共享同一份代理列表）
#   - 用户可通过 CFDM_DOWNLOAD_PROXY=URL 强制指定单家; CFDM_NO_PROXY=1 跳过全部代理
DL_PROXIES="
https://gh-proxy.com/
https://ghfast.top/
https://github.tbedu.top/
https://gh.idayer.com/
https://docker.srv1.qzz.io/
https://dk-proxy.srv1.qzz.io/
https://dk-proxy.966788.xyz/
https://dk-proxy.srv0.qzz.io/
https://docker.srv0.qzz.io/
https://docker.966788.xyz/
"

# 这些值会在 detect_platform / 参数解析阶段被填充
OS=""
ARCH=""
DATA_DIR=""
ENV_FILE=""
DOWNLOADER=""
VERSION="${CFDM_VERSION:-}"
PORT="${CFDM_PORT:-}"
TOKEN="${CFDM_API_TOKEN:-}"
ASSUME_YES="${ASSUME_YES:-0}"
FORCE="0"
ACTION="install"
TMP_DIR=""
DL_PROXY_OVERRIDE="${CFDM_DOWNLOAD_PROXY:-}"  # 用户强制指定单家代理
DL_NO_PROXY="${CFDM_NO_PROXY:-0}"             # 1=完全跳过代理直连

# ----------------------------------------------------------------------------
# 输出辅助 (带颜色, 非 TTY 自动降级为纯文本)
# ----------------------------------------------------------------------------
if [ -t 1 ]; then
    C_RED='\033[0;31m'; C_GRN='\033[0;32m'; C_YLW='\033[0;33m'
    C_BLU='\033[0;34m'; C_BOLD='\033[1m'; C_RST='\033[0m'
else
    C_RED=''; C_GRN=''; C_YLW=''; C_BLU=''; C_BOLD=''; C_RST=''
fi
info()  { printf "%b\n" "${C_BLU}[*]${C_RST} $*"; }
ok()    { printf "%b\n" "${C_GRN}[+]${C_RST} $*"; }
warn()  { printf "%b\n" "${C_YLW}[!]${C_RST} $*"; }
err()   { printf "%b\n" "${C_RED}[x]${C_RST} $*" >&2; }
die()   { err "$*"; exit 1; }

cleanup() { [ -n "$TMP_DIR" ] && [ -d "$TMP_DIR" ] && rm -rf "$TMP_DIR"; return 0; }
trap cleanup EXIT INT TERM

# ----------------------------------------------------------------------------
# 参数解析
# ----------------------------------------------------------------------------
usage() {
    cat <<EOF
${C_BOLD}cfdmgrd 一键安装脚本${C_RST}

用法: sh install.sh [选项]

选项:
  -p, --port <端口>     指定监听端口; 传 "random" 表示随机端口; 省略则交互/默认 ${DEFAULT_PORT}
  -t, --token <令牌>    指定 API 令牌; 省略则交互输入, 留空则生成强随机令牌
  -v, --version <版本>  指定版本 (如 v1.2.10); 省略则安装最新版
  -y, --yes             非交互模式, 端口用默认值、令牌自动随机生成
  -u, --update          全自动更新到最新版 (保留现有端口/令牌/数据, 仅换二进制并重启)
  -f, --force           配合 --update: 即使已是最新版也强制重装
      --uninstall       卸载 (停止服务 + 删除二进制/服务文件)
      --proxy <URL>     指定单一下载代理 (如 https://my.mirror/), 跳过内置数组
      --no-proxy        跳过所有代理, 直连 GitHub 下载
  -h, --help            显示帮助

参数可任意组合, 已传入的参数不再交互询问。示例:
  sh install.sh                                 # 全交互: 逐项询问端口/令牌
  sh install.sh -p 9000                         # 指定端口, 仅询问令牌
  sh install.sh -t my-secret-token              # 指定令牌, 仅询问端口
  sh install.sh -p 9000 -t my-secret-token      # 端口+令牌都指定, 零交互
  sh install.sh -y -p 9000 -t my-secret         # 完全静默安装
  sh install.sh --port random                   # 随机端口
  sh install.sh -v v1.2.10 -p 8888              # 指定版本+端口
  sh install.sh --update                        # 全自动更新到最新版
  sh install.sh --update -v v1.2.11             # 更新到指定版本
  sh install.sh --update --force                # 强制重装当前最新版
  sh install.sh --uninstall                     # 卸载

环境变量等价形式 (适合 CI/自动化):
  CFDM_PORT=9000 CFDM_API_TOKEN=xxx ASSUME_YES=1 sh install.sh
  CFDM_DOWNLOAD_PROXY=https://my.mirror/  # 等价 --proxy
  CFDM_NO_PROXY=1                          # 等价 --no-proxy

下载策略:
  默认按内置代理数组挨个尝试 (公开代理 4 家在前, 自建 6 家在后), 第一个能
  下载并解开为合法 tar.gz 的就用; 全部代理失败回落直连 GitHub。
EOF
}

parse_args() {
    while [ $# -gt 0 ]; do
        case "$1" in
            -p|--port)     PORT="${2:-}"; shift 2 ;;
            -t|--token)    TOKEN="${2:-}"; shift 2 ;;
            -v|--version)  VERSION="${2:-}"; shift 2 ;;
            -y|--yes)      ASSUME_YES=1; shift ;;
            -u|--update)   ACTION="update"; shift ;;
            -f|--force)    FORCE=1; shift ;;
            --uninstall)   ACTION="uninstall"; shift ;;
            --proxy)       DL_PROXY_OVERRIDE="${2:-}"; shift 2 ;;
            --no-proxy)    DL_NO_PROXY=1; shift ;;
            -h|--help)     usage; exit 0 ;;
            *)             die "未知参数: $1 (使用 --help 查看用法)" ;;
        esac
    done
}

# ----------------------------------------------------------------------------
# 平台探测: OS + ARCH, 并据此决定数据目录
# ----------------------------------------------------------------------------
# 探测本机字节序 (mips / mips64 需据此选择大小端二进制); od 缺失时默认小端
detect_endian() {
    if command -v od >/dev/null 2>&1 &&
       [ "$(printf '\1\2\3\4' | od -An -tx4 2>/dev/null | tr -d ' \n')" = "04030201" ]; then
        echo le
    elif command -v od >/dev/null 2>&1; then
        echo be
    else
        echo le
    fi
}

detect_platform() {
    uname_s="$(uname -s 2>/dev/null || echo unknown)"
    uname_m="$(uname -m 2>/dev/null || echo unknown)"

    case "$uname_s" in
        Linux)   OS="linux" ;;
        Darwin)  OS="darwin" ;;
        FreeBSD) OS="freebsd" ;;
        *)       die "不支持的操作系统: $uname_s (支持 Linux / macOS / FreeBSD)" ;;
    esac

    case "$uname_m" in
        x86_64|amd64)              ARCH="amd64" ;;
        aarch64|arm64)             ARCH="arm64" ;;
        armv7l|armv7|armhf|arm)    ARCH="armv7" ;;
        armv6l|armv6)              ARCH="armv6" ;;
        i386|i486|i586|i686|x86)   ARCH="386" ;;
        riscv64)                   ARCH="riscv64" ;;
        loongarch64|loong64)       ARCH="loong64" ;;
        mipsel|mipsle)             ARCH="mipsle" ;;
        mips64el|mips64le)         ARCH="mips64le" ;;
        mips)
            if [ "$(detect_endian)" = "le" ]; then ARCH="mipsle"; else ARCH="mips"; fi ;;
        mips64)
            if [ "$(detect_endian)" = "le" ]; then ARCH="mips64le"; else ARCH="mips64"; fi ;;
        *)                         die "不支持的 CPU 架构: $uname_m" ;;
    esac

    # macOS / FreeBSD 仅发布 amd64 与 arm64 版本
    case "$OS" in
        darwin|freebsd)
            case "$ARCH" in
                amd64|arm64) ;;
                *) die "${OS} 仅提供 amd64 / arm64 版本 (检测到 ${ARCH})" ;;
            esac ;;
    esac

    case "$OS" in
        darwin)  DATA_DIR="/usr/local/var/${SERVICE_NAME}" ;;
        freebsd) DATA_DIR="/var/db/${SERVICE_NAME}" ;;
        *)       DATA_DIR="/var/lib/${SERVICE_NAME}" ;;
    esac
    ENV_FILE="/etc/${SERVICE_NAME}/${SERVICE_NAME}.env"

    info "检测到平台: ${C_BOLD}${OS}/${ARCH}${C_RST}"
}

# ----------------------------------------------------------------------------
# 选择下载器: 优先 curl, 否则 wget
# ----------------------------------------------------------------------------
detect_downloader() {
    if command -v curl >/dev/null 2>&1; then
        DOWNLOADER="curl"
    elif command -v wget >/dev/null 2>&1; then
        DOWNLOADER="wget"
    else
        die "未找到 curl 或 wget, 请先安装其中之一"
    fi
    info "使用下载工具: ${C_BOLD}${DOWNLOADER}${C_RST}"
}

# 下载到标准输出. 用法: fetch_stdout <url>
fetch_stdout() {
    if [ "$DOWNLOADER" = "curl" ]; then
        curl -fsSL "$1"
    else
        wget -qO- "$1"
    fi
}

# 下载到文件. 用法: fetch_file <url> <dest>
fetch_file() {
    if [ "$DOWNLOADER" = "curl" ]; then
        curl -fSL --progress-bar --max-time 30 "$1" -o "$2"
    else
        wget -q --show-progress --timeout=30 -O "$2" "$1"
    fi
}

# 验证下载文件是合法 tar.gz (防"伪 200": 代理返回 HTML 错误页但 HTTP 200)
# 用法: verify_targz <file>; 返回 0=合法, 1=非法
verify_targz() {
    [ -s "$1" ] || return 1
    tar -tzf "$1" >/dev/null 2>&1
}

# 智能代理下载: 遍历候选数组, 第一个成功+合法的就用; 全失败回落直连
# 用法: try_download <github_url> <dest>
try_download() {
    _gh_url="$1"
    _dest="$2"

    # 优先级: --proxy/$CFDM_DOWNLOAD_PROXY > 内置数组 > 直连
    if [ -n "$DL_PROXY_OVERRIDE" ]; then
        _proxy="${DL_PROXY_OVERRIDE%/}/"   # 兜底加尾斜杠
        info "使用指定代理: ${_proxy}"
        fetch_file "${_proxy}${_gh_url}" "$_dest" 2>/dev/null || true
        verify_targz "$_dest" && return 0
        warn "指定代理失败/返回非法包, 回落直连"
        rm -f "$_dest"
    elif [ "$DL_NO_PROXY" != "1" ]; then
        for _proxy in $DL_PROXIES; do
            info "尝试代理: ${_proxy}"
            fetch_file "${_proxy}${_gh_url}" "$_dest" 2>/dev/null || { rm -f "$_dest"; continue; }
            if verify_targz "$_dest"; then
                ok "下载源: ${_proxy}"
                return 0
            fi
            warn "  -> 返回非法包 (伪 200?), 跳下一家"
            rm -f "$_dest"
        done
        warn "全部代理失败, 回落直连 GitHub"
    fi

    # 直连兜底
    info "直连: ${_gh_url}"
    fetch_file "$_gh_url" "$_dest" || return 1
    verify_targz "$_dest" || { err "直连下载的文件也不是合法 tar.gz"; return 1; }
    return 0
}

# ----------------------------------------------------------------------------
# 权限: 非 root 时通过 sudo 执行
# ----------------------------------------------------------------------------
SUDO=""
ensure_root() {
    if [ "$(id -u)" -ne 0 ]; then
        if command -v sudo >/dev/null 2>&1; then
            SUDO="sudo"
            info "部分操作需要管理员权限, 将通过 sudo 执行"
        else
            die "需要 root 权限, 但未找到 sudo. 请使用 root 用户运行"
        fi
    fi
}
# 以特权执行命令
priv() { $SUDO "$@"; }

# ----------------------------------------------------------------------------
# 交互读取 (从 /dev/tty 读, 这样 curl|sh 管道里也能交互)
#   用法: prompt <提示语> <默认值>  -> 结果写入全局 REPLY
# ----------------------------------------------------------------------------
REPLY=""
prompt() {
    _msg="$1"; _def="${2:-}"
    if [ "$ASSUME_YES" = "1" ] || [ ! -r /dev/tty ]; then
        REPLY="$_def"
        return 0
    fi
    if [ -n "$_def" ]; then
        printf "%b" "${C_YLW}? ${C_RST}${_msg} [${C_BOLD}${_def}${C_RST}]: " > /dev/tty
    else
        printf "%b" "${C_YLW}? ${C_RST}${_msg}: " > /dev/tty
    fi
    IFS= read -r REPLY < /dev/tty || REPLY=""
    [ -z "$REPLY" ] && REPLY="$_def"
}

# ----------------------------------------------------------------------------
# 生成随机令牌 / 随机端口
# ----------------------------------------------------------------------------
gen_token() {
    if command -v openssl >/dev/null 2>&1; then
        openssl rand -hex 24
    elif [ -r /dev/urandom ]; then
        LC_ALL=C tr -dc 'a-f0-9' < /dev/urandom 2>/dev/null | dd bs=48 count=1 2>/dev/null
    else
        # 退而求其次: 时间戳 + 进程号
        printf "frpsmgr%s%s" "$(date +%s)" "$$"
    fi
}

gen_random_port() {
    # 20000-60000 之间的随机端口
    if command -v awk >/dev/null 2>&1; then
        awk "BEGIN{srand($$ + $(date +%s 2>/dev/null || echo 0)); print int(20000 + rand()*40000)}"
    else
        # 用进程号兜底
        echo $(( 20000 + ($$ % 40000) ))
    fi
}

# 校验端口是否为 1-65535 的合法整数
valid_port() {
    case "$1" in
        ''|*[!0-9]*) return 1 ;;
    esac
    [ "$1" -ge 1 ] && [ "$1" -le 65535 ]
}

# ----------------------------------------------------------------------------
# 解析最新版本号 (GitHub API), 失败则提示手动指定
# ----------------------------------------------------------------------------
resolve_version() {
    if [ -n "$VERSION" ]; then
        # 统一补上 v 前缀
        case "$VERSION" in v*) ;; *) VERSION="v$VERSION" ;; esac
        info "使用指定版本: ${C_BOLD}${VERSION}${C_RST}"
        return 0
    fi
    info "正在查询最新版本..."
    _api="https://api.github.com/repos/${REPO}/releases/latest"
    _tag="$(fetch_stdout "$_api" 2>/dev/null \
        | grep '"tag_name"' \
        | head -n1 \
        | sed -E 's/.*"tag_name"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/')" || true
    [ -n "$_tag" ] || die "无法获取最新版本, 请用 --version 手动指定 (如 --version v1.2.10)"
    VERSION="$_tag"
    ok "最新版本: ${C_BOLD}${VERSION}${C_RST}"
}

# ----------------------------------------------------------------------------
# 决定端口与令牌 (交互 / 默认 / 随机)
# ----------------------------------------------------------------------------
resolve_port() {
    if [ "$PORT" = "random" ]; then
        PORT="$(gen_random_port)"
        ok "已生成随机端口: ${C_BOLD}${PORT}${C_RST}"
        return 0
    fi
    if [ -z "$PORT" ]; then
        prompt "请输入监听端口 (回车=默认 ${DEFAULT_PORT}, 输入 r=随机)" "$DEFAULT_PORT"
        PORT="$REPLY"
    fi
    if [ "$PORT" = "r" ] || [ "$PORT" = "random" ]; then
        PORT="$(gen_random_port)"
        ok "已生成随机端口: ${C_BOLD}${PORT}${C_RST}"
    fi
    valid_port "$PORT" || die "端口非法: '$PORT' (应为 1-65535)"
    info "监听端口: ${C_BOLD}${PORT}${C_RST}"
}

# TOKEN_SOURCE 记录令牌来源, 供安装前确认信息展示
TOKEN_SOURCE=""
resolve_token() {
    if [ -n "$TOKEN" ]; then
        TOKEN_SOURCE="命令行/环境变量指定"
    elif [ "$ASSUME_YES" != "1" ]; then
        prompt "请输入 API 令牌 (后台访问凭证, 回车=自动生成强随机令牌)" ""
        TOKEN="$REPLY"
        [ -n "$TOKEN" ] && TOKEN_SOURCE="手动输入"
    fi
    if [ -z "$TOKEN" ]; then
        TOKEN="$(gen_token)"
        TOKEN_SOURCE="自动生成"
        ok "已自动生成强随机 API 令牌"
    else
        info "API 令牌: ${TOKEN_SOURCE}"
    fi
}

# ----------------------------------------------------------------------------
# 安装前确认 (交互模式展示最终参数, 让用户过目; 静默/管道无 tty 则跳过)
# ----------------------------------------------------------------------------
confirm_install() {
    printf "\n%b\n" "${C_BOLD}即将安装, 请确认以下信息:${C_RST}"
    printf "  平台      : %s/%s\n" "$OS" "$ARCH"
    printf "  版本      : %s\n" "$VERSION"
    printf "  监听端口  : %s\n" "$PORT"
    printf "  API 令牌  : %s  (%s)\n" "$TOKEN" "$TOKEN_SOURCE"
    printf "  安装目录  : %s/%s\n" "$INSTALL_DIR" "$BIN_NAME"
    printf "  数据目录  : %s\n" "$DATA_DIR"
    printf "\n"
    if [ "$ASSUME_YES" = "1" ] || [ ! -r /dev/tty ]; then
        return 0
    fi
    prompt "确认继续? [Y/n]" "Y"
    case "$REPLY" in
        n|N|no|NO) die "已取消安装" ;;
    esac
}

# ----------------------------------------------------------------------------
# 下载并安装二进制
# ----------------------------------------------------------------------------
download_and_install() {
    _ver_num="${VERSION#v}"   # 文件名里的版本号不带 v
    _asset="${BIN_NAME}_${_ver_num}_${OS}_${ARCH}.tar.gz"
    _url="https://github.com/${REPO}/releases/download/${VERSION}/${_asset}"

    TMP_DIR="$(mktemp -d 2>/dev/null || mktemp -d -t frpsmgr)"
    info "目标: ${_url}"
    try_download "$_url" "${TMP_DIR}/${_asset}" || die "全部下载途径失败 (代理 + 直连)"

    info "解压安装包..."
    tar -xzf "${TMP_DIR}/${_asset}" -C "$TMP_DIR" || die "解压失败"
    [ -f "${TMP_DIR}/${BIN_NAME}" ] || die "安装包中未找到二进制 ${BIN_NAME}"

    info "安装到 ${INSTALL_DIR}/${BIN_NAME}"
    priv mkdir -p "$INSTALL_DIR"
    priv install -m 0755 "${TMP_DIR}/${BIN_NAME}" "${INSTALL_DIR}/${BIN_NAME}"
    ok "二进制安装完成: $(${INSTALL_DIR}/${BIN_NAME} version 2>/dev/null || echo "${INSTALL_DIR}/${BIN_NAME}")"
}

# ----------------------------------------------------------------------------
# 写入环境配置文件
# ----------------------------------------------------------------------------
write_env_file() {
    info "写入配置: ${ENV_FILE}"
    priv mkdir -p "$(dirname "$ENV_FILE")"
    priv mkdir -p "$DATA_DIR"
    # 通过临时文件再 install, 避免重定向到特权路径的麻烦
    _tmp_env="${TMP_DIR}/cfdmgrd.env"
    cat > "$_tmp_env" <<EOF
# cfdmgrd 运行配置 (由 install.sh 生成)
CFDM_API_TOKEN=${TOKEN}
CFDM_HTTP_ADDR=:${PORT}
CFDM_DATA_DIR=${DATA_DIR}
CFDM_LOG_LEVEL=info
CFDM_CORS_ORIGINS=*
CFDM_DOCS_ENABLED=true
# 是否允许在 Web 后台「关于」页一键自更新并重启 (true/false)
CFDM_SELF_UPDATE_ENABLED=true
EOF
    priv install -m 0600 "$_tmp_env" "$ENV_FILE"
}

# ----------------------------------------------------------------------------
# 注册系统服务: systemd / OpenRC / launchd / 回退
# ----------------------------------------------------------------------------
detect_init_system() {
    if [ "$OS" = "darwin" ]; then
        echo "launchd"; return
    fi
    if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
        echo "systemd"; return
    fi
    if command -v rc-update >/dev/null 2>&1; then
        echo "openrc"; return
    fi
    echo "none"
}

setup_systemd() {
    _unit="/etc/systemd/system/${SERVICE_NAME}.service"
    info "创建 systemd 服务: ${_unit}"
    _tmp_unit="${TMP_DIR}/${SERVICE_NAME}.service"
    cat > "$_tmp_unit" <<EOF
[Unit]
Description=cfdmgrd - cloudflared multi-instance manager
Documentation=https://github.com/${REPO}
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=${ENV_FILE}
ExecStart=${INSTALL_DIR}/${BIN_NAME} serve
Restart=on-failure
RestartSec=5
LimitNOFILE=65536
# 安全加固 (数据目录仍可写)
NoNewPrivileges=true
ProtectSystem=full
ReadWritePaths=${DATA_DIR}

[Install]
WantedBy=multi-user.target
EOF
    priv install -m 0644 "$_tmp_unit" "$_unit"
    priv systemctl daemon-reload
    priv systemctl enable "${SERVICE_NAME}" >/dev/null 2>&1 || true
    priv systemctl restart "${SERVICE_NAME}"
    ok "systemd 服务已启用并设置为开机自启"
}

setup_openrc() {
    _init="/etc/init.d/${SERVICE_NAME}"
    info "创建 OpenRC 服务: ${_init}"
    _tmp_init="${TMP_DIR}/${SERVICE_NAME}.openrc"
    cat > "$_tmp_init" <<EOF
#!/sbin/openrc-run
name="${SERVICE_NAME}"
description="cfdmgrd - cloudflared multi-instance manager"
command="${INSTALL_DIR}/${BIN_NAME}"
command_args="serve"
command_background=true
pidfile="/run/${SERVICE_NAME}.pid"
output_log="/var/log/${SERVICE_NAME}.log"
error_log="/var/log/${SERVICE_NAME}.log"

depend() {
    need net
}

start_pre() {
    set -a
    . "${ENV_FILE}"
    set +a
}
EOF
    priv install -m 0755 "$_tmp_init" "$_init"
    priv rc-update add "${SERVICE_NAME}" default >/dev/null 2>&1 || true
    priv rc-service "${SERVICE_NAME}" restart
    ok "OpenRC 服务已启用并设置为开机自启"
}

setup_launchd() {
    _label="com.miaclark.${SERVICE_NAME}"
    _plist="/Library/LaunchDaemons/${_label}.plist"
    info "创建 launchd 服务: ${_plist}"
    priv mkdir -p /var/log
    _tmp_plist="${TMP_DIR}/${_label}.plist"
    cat > "$_tmp_plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>${_label}</string>
    <key>ProgramArguments</key>
    <array>
        <string>${INSTALL_DIR}/${BIN_NAME}</string>
        <string>serve</string>
    </array>
    <key>EnvironmentVariables</key>
    <dict>
        <key>CFDM_API_TOKEN</key>
        <string>${TOKEN}</string>
        <key>CFDM_HTTP_ADDR</key>
        <string>:${PORT}</string>
        <key>CFDM_DATA_DIR</key>
        <string>${DATA_DIR}</string>
        <key>CFDM_LOG_LEVEL</key>
        <string>info</string>
        <key>CFDM_SELF_UPDATE_ENABLED</key>
        <string>true</string>
    </dict>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/var/log/${SERVICE_NAME}.log</string>
    <key>StandardErrorPath</key>
    <string>/var/log/${SERVICE_NAME}.log</string>
</dict>
</plist>
EOF
    priv install -m 0644 "$_tmp_plist" "$_plist"
    priv launchctl unload "$_plist" >/dev/null 2>&1 || true
    priv launchctl load -w "$_plist"
    ok "launchd 服务已加载并设置为开机自启"
}

setup_service() {
    _init="$(detect_init_system)"
    case "$_init" in
        systemd) setup_systemd ;;
        openrc)  setup_openrc ;;
        launchd) setup_launchd ;;
        none)
            warn "未识别到 systemd/OpenRC, 跳过服务注册。"
            warn "可手动后台运行: ${ENV_FILE} 已写入配置, 执行:"
            warn "  set -a; . ${ENV_FILE}; set +a; ${INSTALL_DIR}/${BIN_NAME} serve &"
            ;;
    esac
}

# ----------------------------------------------------------------------------
# 生成统一管理命令 cfm (封装 服务管理 / 更新 / 卸载 / 信息查看)
#   安装到 ${INSTALL_DIR}/cfm (该目录已在 PATH 上, 全局可直接调用 cfm <命令>)
# ----------------------------------------------------------------------------
install_cli() {
    _cli="${INSTALL_DIR}/cfm"
    info "安装管理命令: ${_cli}"
    # TMP_DIR 正常已由下载阶段创建; 兜底再建一次
    [ -n "$TMP_DIR" ] && [ -d "$TMP_DIR" ] || TMP_DIR="$(mktemp -d 2>/dev/null || mktemp -d -t frpsmgr)"
    _tmp_cli="${TMP_DIR}/cfm"

    # 头部: 注入安装期常量 (此 heredoc 不加引号, 变量会被展开并固化进脚本)
    cat > "$_tmp_cli" <<EOF
#!/bin/sh
# =============================================================================
# cfm — cfdmgrd 管理命令 (由 install.sh 自动生成, 请勿手动编辑)
#   用法: cfm <命令> [参数]   (cfm help 查看全部命令)
# =============================================================================
REPO="${REPO}"
BIN_NAME="${BIN_NAME}"
INSTALL_DIR="${INSTALL_DIR}"
SERVICE_NAME="${SERVICE_NAME}"
ENV_FILE="${ENV_FILE}"
DATA_DIR="${DATA_DIR}"
RAW_URL="https://raw.githubusercontent.com/${REPO}/main/scripts/install.sh"
EOF

    # 主体: 运行期逻辑 (单引号 heredoc, 保持 \$ 变量与转义原样写入)
    cat >> "$_tmp_cli" <<'FMS_EOF'
set -eu

if [ -t 1 ]; then
    C_RED='\033[0;31m'; C_GRN='\033[0;32m'; C_YLW='\033[0;33m'
    C_BLU='\033[0;34m'; C_BOLD='\033[1m'; C_RST='\033[0m'
else
    C_RED=''; C_GRN=''; C_YLW=''; C_BLU=''; C_BOLD=''; C_RST=''
fi
info()  { printf "%b\n" "${C_BLU}[*]${C_RST} $*"; }
ok()    { printf "%b\n" "${C_GRN}[+]${C_RST} $*"; }
warn()  { printf "%b\n" "${C_YLW}[!]${C_RST} $*"; }
err()   { printf "%b\n" "${C_RED}[x]${C_RST} $*" >&2; }
die()   { err "$*"; exit 1; }

# 非 root 时通过 sudo 执行特权操作
SUDO=""
if [ "$(id -u)" -ne 0 ] && command -v sudo >/dev/null 2>&1; then
    SUDO="sudo"
fi
priv() { $SUDO "$@"; }

PLIST="/Library/LaunchDaemons/com.miaclark.${SERVICE_NAME}.plist"

# 允许用镜像源覆盖 install.sh 下载地址 (适配国内网络): CFDM_INSTALL_URL=https://镜像/install.sh
if [ -n "${CFDM_INSTALL_URL:-}" ]; then RAW_URL="$CFDM_INSTALL_URL"; fi

# 运行期探测 init 系统 (与安装时解耦, 迁移/换系统也能用)
detect_init() {
    if [ "$(uname -s 2>/dev/null)" = "Darwin" ]; then echo "launchd"; return; fi
    if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then echo "systemd"; return; fi
    if command -v rc-service >/dev/null 2>&1; then echo "openrc"; return; fi
    echo "none"
}

# 下载到标准输出 (curl 优先, 回退 wget)
fetch() {
    if command -v curl >/dev/null 2>&1; then curl -fsSL "$1"
    elif command -v wget >/dev/null 2>&1; then wget -qO- "$1"
    else die "未找到 curl 或 wget, 无法联网执行该命令"; fi
}

# 从配置文件读取某个 KEY 的值 (无则空)
env_get() {
    [ -f "$ENV_FILE" ] || return 0
    grep "^$1=" "$ENV_FILE" 2>/dev/null | head -n1 | cut -d= -f2-
}

cmd_start() {
    case "$(detect_init)" in
        systemd) priv systemctl start "$SERVICE_NAME"; ok "服务已启动" ;;
        openrc)  priv rc-service "$SERVICE_NAME" start; ok "服务已启动" ;;
        launchd) priv launchctl load -w "$PLIST"; ok "服务已启动" ;;
        *)       die "未识别到服务管理器, 无法操作" ;;
    esac
}
cmd_stop() {
    case "$(detect_init)" in
        systemd) priv systemctl stop "$SERVICE_NAME"; ok "服务已停止" ;;
        openrc)  priv rc-service "$SERVICE_NAME" stop; ok "服务已停止" ;;
        launchd) priv launchctl unload "$PLIST"; ok "服务已停止" ;;
        *)       die "未识别到服务管理器, 无法操作" ;;
    esac
}
cmd_restart() {
    case "$(detect_init)" in
        systemd) priv systemctl restart "$SERVICE_NAME"; ok "服务已重启" ;;
        openrc)  priv rc-service "$SERVICE_NAME" restart; ok "服务已重启" ;;
        launchd) priv launchctl unload "$PLIST" >/dev/null 2>&1 || true
                 priv launchctl load -w "$PLIST"; ok "服务已重启" ;;
        *)       die "未识别到服务管理器, 无法操作" ;;
    esac
}
cmd_status() {
    case "$(detect_init)" in
        systemd) priv systemctl status "$SERVICE_NAME" --no-pager ;;
        openrc)  priv rc-service "$SERVICE_NAME" status ;;
        launchd) priv launchctl list 2>/dev/null | grep "$SERVICE_NAME" || echo "服务未在运行" ;;
        *)       die "未识别到服务管理器, 无法操作" ;;
    esac
}
cmd_enable() {
    case "$(detect_init)" in
        systemd) priv systemctl enable "$SERVICE_NAME"; ok "已设置开机自启" ;;
        openrc)  priv rc-update add "$SERVICE_NAME" default; ok "已设置开机自启" ;;
        launchd) priv launchctl load -w "$PLIST"; ok "已设置开机自启" ;;
        *)       die "未识别到服务管理器, 无法操作" ;;
    esac
}
cmd_disable() {
    case "$(detect_init)" in
        systemd) priv systemctl disable "$SERVICE_NAME"; ok "已取消开机自启" ;;
        openrc)  priv rc-update del "$SERVICE_NAME" default; ok "已取消开机自启" ;;
        launchd) priv launchctl unload -w "$PLIST"; ok "已取消开机自启" ;;
        *)       die "未识别到服务管理器, 无法操作" ;;
    esac
}
cmd_logs() {
    _follow=""
    case "${1:-}" in -f|--follow|follow) _follow=1 ;; esac
    case "$(detect_init)" in
        systemd)
            if [ -n "$_follow" ]; then priv journalctl -u "$SERVICE_NAME" -f
            else priv journalctl -u "$SERVICE_NAME" -n 200 --no-pager; fi
            ;;
        *)
            _log="/var/log/${SERVICE_NAME}.log"
            [ -f "$_log" ] || die "未找到日志文件: $_log"
            if [ -n "$_follow" ]; then priv tail -f "$_log"
            else priv tail -n 200 "$_log"; fi
            ;;
    esac
}
# 管理命令面板 (info 命令底部展示)
cli_panel() {
    printf "%b\n" "────────────────────────────────────────────"
    printf "%b\n" "  ${C_BOLD}管理命令 (已安装到 PATH, 任意目录可用):${C_RST}"
    printf "    ${C_BOLD}%-13s${C_RST} # %s\n" "cfm start"     "启动服务"
    printf "    ${C_BOLD}%-13s${C_RST} # %s\n" "cfm stop"      "停止服务"
    printf "    ${C_BOLD}%-13s${C_RST} # %s\n" "cfm restart"   "重启服务"
    printf "    ${C_BOLD}%-13s${C_RST} # %s\n" "cfm status"    "查看状态"
    printf "    ${C_BOLD}%-13s${C_RST} # %s\n" "cfm logs -f"   "实时日志"
    printf "    ${C_BOLD}%-13s${C_RST} # %s\n" "cfm info"      "查看完整信息"
    printf "    ${C_BOLD}%-13s${C_RST} # %s\n" "cfm config"    "查看/编辑配置"
    printf "    ${C_BOLD}%-13s${C_RST} # %s\n" "cfm update"    "更新到最新版"
    printf "    ${C_BOLD}%-13s${C_RST} # %s\n" "cfm uninstall" "卸载"
    printf "    ${C_BOLD}%-13s${C_RST} # %s\n" "cfm help"      "查看全部命令"
    printf "%b\n" "────────────────────────────────────────────"
}
# ----------------------------------------------------------------------------
# 外网 IP 探测 (与 install.sh 同款逻辑, 此处独立内嵌, 让 cfm 自包含)
# ----------------------------------------------------------------------------
PUBIP_V4_URLS="https://4.ipw.cn https://api.ip.sb/ip https://api.ipify.org https://ifconfig.me/ip https://ipv4.icanhazip.com http://members.3322.org/dyndns/getip"
PUBIP_V6_URLS="https://6.ipw.cn https://ipv6.icanhazip.com"

detect_public_ips() {
    _out="$(mktemp 2>/dev/null || echo "/tmp/cfm_pubips.$$")"
    : > "$_out"
    _pids=""
    for _u in $PUBIP_V4_URLS; do
        (
            if command -v curl >/dev/null 2>&1; then
                _r="$(curl -fsS4 --max-time 2 "$_u" 2>/dev/null | tr -d ' \r\n\t')"
            else
                _r="$(wget -qO- --timeout=2 "$_u" 2>/dev/null | tr -d ' \r\n\t')"
            fi
            _r="$(printf "%s" "$_r" | grep -Eo '([0-9]{1,3}\.){3}[0-9]{1,3}' | head -n1)"
            [ -n "$_r" ] && printf "%s\n" "$_r" >> "$_out"
        ) &
        _pids="$_pids $!"
    done
    for _u in $PUBIP_V6_URLS; do
        (
            if command -v curl >/dev/null 2>&1; then
                _r="$(curl -fsS6 --max-time 2 "$_u" 2>/dev/null | tr -d ' \r\n\t')"
            else
                _r="$(wget -qO- --timeout=2 "$_u" 2>/dev/null | tr -d ' \r\n\t')"
            fi
            case "$_r" in *:*:*) printf "%s\n" "$_r" >> "$_out" ;; esac
        ) &
        _pids="$_pids $!"
    done
    # shellcheck disable=SC2086
    wait $_pids 2>/dev/null
    awk 'NF && !seen[$0]++' "$_out" | tr '\n' ' '
    rm -f "$_out"
}

PUBLIC_IPS_CACHE=""; PUBLIC_IPS_CACHED=0
public_ips() {
    if [ "$PUBLIC_IPS_CACHED" = "0" ]; then
        PUBLIC_IPS_CACHE="$(detect_public_ips)"; PUBLIC_IPS_CACHED=1
    fi
    printf "%s" "$PUBLIC_IPS_CACHE"
}

print_url_line() {
    _label="$1"; _p="$2"; _path="${3:-}"
    printf "  %-8s : ${C_BOLD}http://127.0.0.1:%s%s${C_RST}\n" "$_label" "$_p" "$_path"
    _pubs="$(public_ips)"
    [ -n "$_pubs" ] || return 0
    for _ip in $_pubs; do
        case "$_ip" in
            *:*) printf "             ${C_BOLD}http://[%s]:%s%s${C_RST}  ${C_BLU}(外网)${C_RST}\n" "$_ip" "$_p" "$_path" ;;
            *)   printf "             ${C_BOLD}http://%s:%s%s${C_RST}  ${C_BLU}(外网)${C_RST}\n"   "$_ip" "$_p" "$_path" ;;
        esac
    done
}

cmd_info() {
    _addr="$(env_get CFDM_HTTP_ADDR)"; _port="${_addr#:}"; [ -n "$_port" ] || _port="8080"
    _token="$(env_get CFDM_API_TOKEN)"
    _ddir="$(env_get CFDM_DATA_DIR)";  [ -n "$_ddir" ] || _ddir="$DATA_DIR"
    _loglv="$(env_get CFDM_LOG_LEVEL)"; [ -n "$_loglv" ] || _loglv="info"
    _ver="$("${INSTALL_DIR}/${BIN_NAME}" version 2>/dev/null || echo 未知)"
    case "$(detect_init)" in
        systemd) _svc="/etc/systemd/system/${SERVICE_NAME}.service"
                 _state="$(systemctl is-active "$SERVICE_NAME" 2>/dev/null || true)"; [ -n "$_state" ] || _state="unknown"
                 _logc="journalctl -u ${SERVICE_NAME} -f" ;;
        openrc)  _svc="/etc/init.d/${SERVICE_NAME}"
                 if rc-service "$SERVICE_NAME" status >/dev/null 2>&1; then _state="active"; else _state="stopped"; fi
                 _logc="tail -f /var/log/${SERVICE_NAME}.log" ;;
        launchd) _svc="$PLIST"
                 if launchctl list 2>/dev/null | grep -q "$SERVICE_NAME"; then _state="active"; else _state="stopped"; fi
                 _logc="tail -f /var/log/${SERVICE_NAME}.log" ;;
        *)       _svc="(未注册)"; _state="unknown"; _logc="(无)" ;;
    esac
    printf "%b\n" "${C_BOLD}cfdmgrd 运行信息${C_RST}"
    printf "%b\n" "────────────────────────────────────────────"
    printf "  版本     : %s\n" "$_ver"
    printf "  服务状态 : %s\n" "$_state"
    print_url_line "访问地址" "$_port"
    print_url_line "API 文档" "$_port" "/api/docs"
    [ -n "$(public_ips)" ] && printf "  %b\n" "${C_YLW}注: 外网地址能否实际访问取决于防火墙/安全组/NAT 是否放行该端口${C_RST}"
    printf "  API 令牌 : ${C_BOLD}%s${C_RST}\n" "${_token:-(未读取到)}"
    printf "  监听地址 : %s\n" "${_addr:-:8080}"
    printf "  日志级别 : %s\n" "$_loglv"
    printf "  程序路径 : %s\n" "${INSTALL_DIR}/${BIN_NAME}"
    printf "  管理命令 : %s\n" "${INSTALL_DIR}/cfm"
    printf "  配置文件 : %s\n" "$ENV_FILE"
    printf "  数据目录 : %s\n" "$_ddir"
    printf "  服务文件 : %s\n" "$_svc"
    printf "  日志查看 : %s\n" "$_logc"
    cli_panel
}
cmd_config() {
    [ -f "$ENV_FILE" ] || die "配置文件不存在: $ENV_FILE"
    case "${1:-show}" in
        edit)
            priv "${EDITOR:-vi}" "$ENV_FILE"
            warn "如修改了配置, 请执行 cfm restart 使其生效"
            ;;
        *)  priv cat "$ENV_FILE" ;;
    esac
}
cmd_version()   { "${INSTALL_DIR}/${BIN_NAME}" version; }
cmd_update()    { fetch "$RAW_URL" | sh -s -- --update "$@"; }
cmd_install()   { fetch "$RAW_URL" | sh -s -- "$@"; }
cmd_uninstall() {
    fetch "$RAW_URL" | sh -s -- --uninstall
    priv rm -f "${INSTALL_DIR}/cfm" 2>/dev/null || true
}

usage() {
    printf "%b\n" "${C_BOLD}cfm — cfdmgrd 管理命令${C_RST}

用法: cfm <命令> [参数]

服务管理:
  start            启动服务
  stop             停止服务
  restart          重启服务
  status           查看运行状态
  logs [-f]        查看日志 (加 -f 实时跟踪)
  enable           设置开机自启
  disable          取消开机自启

信息查看:
  info             显示完整运行信息 (地址/令牌/路径/状态) + 命令面板
  config [edit]    查看 (或 edit 编辑) 配置文件
  version          显示版本信息

安装维护:
  install [参数]   重新安装 (参数透传给 install.sh)
  update [参数]    更新到最新版 (保留端口/令牌/数据)
  uninstall        卸载

  help             显示本帮助"
}

# 子命令收尾的一行轻提示, 引导查看完整命令清单
cli_tip() {
    printf "%b\n" "────────────────────────────────────────────"
    printf "%b\n" "${C_BOLD}💡 输入 cfm 查看全部命令${C_RST}"
    printf "%b\n" "────────────────────────────────────────────"
}

case "${1:-help}" in
    start)      shift; cmd_start "$@" ;;
    stop)       shift; cmd_stop "$@" ;;
    restart)    shift; cmd_restart "$@" ;;
    status)     shift; cmd_status "$@" || true ;;
    logs)       shift; cmd_logs "$@" ;;
    enable)     shift; cmd_enable "$@" ;;
    disable)    shift; cmd_disable "$@" ;;
    info)       shift; cmd_info "$@"; exit 0 ;;
    config)     shift; cmd_config "$@" ;;
    version|-v|--version) shift; cmd_version "$@" ;;
    update)     shift; cmd_update "$@" ;;
    install)    shift; cmd_install "$@" ;;
    uninstall)  shift; cmd_uninstall "$@"; exit 0 ;;
    help|-h|--help) usage; exit 0 ;;
    *)          err "未知命令: ${1}"; echo; usage; exit 2 ;;
esac

# 任意子命令执行完都补一行轻提示; help/uninstall 已提前 exit,
# logs -f 阻塞跟踪不会走到这里, 因此都不会触发
cli_tip
FMS_EOF

    priv install -m 0755 "$_tmp_cli" "$_cli"
    ok "管理命令已安装, 现在可直接使用: ${C_BOLD}cfm <命令>${C_RST}"
}

# ----------------------------------------------------------------------------
# 读取已安装二进制的版本号 (如 1.2.10), 未安装则为空
# ----------------------------------------------------------------------------
get_installed_version() {
    if [ -x "${INSTALL_DIR}/${BIN_NAME}" ]; then
        "${INSTALL_DIR}/${BIN_NAME}" version 2>/dev/null | awk '{print $2}'
    fi
}

# ----------------------------------------------------------------------------
# 从现有配置读取监听端口 (用于更新后做健康检查), 取不到则为空
# ----------------------------------------------------------------------------
read_env_port() {
    if [ -f "$ENV_FILE" ]; then
        _addr="$(grep '^CFDM_HTTP_ADDR=' "$ENV_FILE" 2>/dev/null | head -n1 | cut -d= -f2)"
        echo "${_addr#:}"
    elif [ "$OS" = "darwin" ]; then
        _plist="/Library/LaunchDaemons/com.miaclark.${SERVICE_NAME}.plist"
        if [ -f "$_plist" ] && [ -x /usr/libexec/PlistBuddy ]; then
            _addr="$(priv /usr/libexec/PlistBuddy -c \
                "Print :EnvironmentVariables:CFDM_HTTP_ADDR" "$_plist" 2>/dev/null)"
            echo "${_addr#:}"
        fi
    fi
}

# ----------------------------------------------------------------------------
# 重启已有服务 (不重写服务文件, 仅重启以加载新二进制)
# ----------------------------------------------------------------------------
restart_service() {
    case "$(detect_init_system)" in
        systemd)
            if [ -f "/etc/systemd/system/${SERVICE_NAME}.service" ]; then
                priv systemctl restart "${SERVICE_NAME}"
                ok "systemd 服务已重启"
            else
                warn "未发现 systemd 服务单元, 跳过重启 (可重新安装以注册服务)"
            fi
            ;;
        openrc)
            if [ -f "/etc/init.d/${SERVICE_NAME}" ]; then
                priv rc-service "${SERVICE_NAME}" restart
                ok "OpenRC 服务已重启"
            else
                warn "未发现 OpenRC 服务, 跳过重启"
            fi
            ;;
        launchd)
            _plist="/Library/LaunchDaemons/com.miaclark.${SERVICE_NAME}.plist"
            if [ -f "$_plist" ]; then
                priv launchctl unload "$_plist" >/dev/null 2>&1 || true
                priv launchctl load -w "$_plist"
                ok "launchd 服务已重启"
            else
                warn "未发现 launchd 服务, 跳过重启"
            fi
            ;;
        none)
            warn "未识别到服务管理器, 请手动重启进程"
            ;;
    esac
}

# ----------------------------------------------------------------------------
# 健康检查
# ----------------------------------------------------------------------------
health_check() {
    info "等待服务就绪..."
    _i=0
    while [ "$_i" -lt 10 ]; do
        if "${INSTALL_DIR}/${BIN_NAME}" health -addr "http://127.0.0.1:${PORT}" >/dev/null 2>&1; then
            ok "服务健康检查通过 ✓"
            return 0
        fi
        _i=$((_i + 1))
        sleep 1
    done
    warn "健康检查未通过 (服务可能仍在启动)。请稍后手动检查服务状态与日志。"
}

# ----------------------------------------------------------------------------
# 安装总流程
# ----------------------------------------------------------------------------
do_install() {
    printf "%b\n" "${C_BOLD}=== cfdmgrd 一键安装 ===${C_RST}"
    detect_platform
    detect_downloader
    ensure_root
    resolve_version
    resolve_port
    resolve_token
    confirm_install
    download_and_install
    write_env_file
    setup_service
    install_cli
    health_check
    print_summary
}

# 打印 cfm 管理命令清单 (安装 / 更新结尾共用, 方便用户直接照着敲)
print_cli_hint() {
    printf "%b\n" "────────────────────────────────────────────"
    printf "%b\n" "  ${C_BOLD}管理命令 (已安装到 PATH, 任意目录可用):${C_RST}"
    # %-13s 让命令列定宽左对齐 (最长 cfm uninstall = 13)，颜色码只在格式串里、不参与宽度计算，# 自然对齐
    printf "    ${C_BOLD}%-13s${C_RST} # %s\n" "cfm start"     "启动服务"
    printf "    ${C_BOLD}%-13s${C_RST} # %s\n" "cfm stop"      "停止服务"
    printf "    ${C_BOLD}%-13s${C_RST} # %s\n" "cfm restart"   "重启服务"
    printf "    ${C_BOLD}%-13s${C_RST} # %s\n" "cfm status"    "查看状态"
    printf "    ${C_BOLD}%-13s${C_RST} # %s\n" "cfm logs -f"   "实时日志"
    printf "    ${C_BOLD}%-13s${C_RST} # %s\n" "cfm info"      "查看完整信息"
    printf "    ${C_BOLD}%-13s${C_RST} # %s\n" "cfm config"    "查看/编辑配置"
    printf "    ${C_BOLD}%-13s${C_RST} # %s\n" "cfm update"    "更新到最新版"
    printf "    ${C_BOLD}%-13s${C_RST} # %s\n" "cfm uninstall" "卸载"
    printf "    ${C_BOLD}%-13s${C_RST} # %s\n" "cfm help"      "查看全部命令"
    printf "%b\n" "────────────────────────────────────────────"
}

# ----------------------------------------------------------------------------
# 外网 IP 探测
#   - 多源混合 (国内+境外, 每个超时 ~1.5s) 并发查询, 去重
#   - IPv4 + IPv6 都查, 都失败时静默返回空 (不阻塞主流程)
#   - 输出空格分隔的 IP 列表
# ----------------------------------------------------------------------------
PUBIP_V4_URLS="https://4.ipw.cn https://api.ip.sb/ip https://api.ipify.org https://ifconfig.me/ip https://ipv4.icanhazip.com http://members.3322.org/dyndns/getip"
PUBIP_V6_URLS="https://6.ipw.cn https://ipv6.icanhazip.com"

detect_public_ips() {
    _tmpdir="${TMP_DIR:-/tmp}"
    _out="${_tmpdir}/pubips.$$"
    : > "$_out"
    _pids=""
    for _u in $PUBIP_V4_URLS; do
        (
            if command -v curl >/dev/null 2>&1; then
                _r="$(curl -fsS4 --max-time 2 "$_u" 2>/dev/null | tr -d ' \r\n\t')"
            else
                _r="$(wget -qO- --timeout=2 "$_u" 2>/dev/null | tr -d ' \r\n\t')"
            fi
            _r="$(printf "%s" "$_r" | grep -Eo '([0-9]{1,3}\.){3}[0-9]{1,3}' | head -n1)"
            [ -n "$_r" ] && printf "%s\n" "$_r" >> "$_out"
        ) &
        _pids="$_pids $!"
    done
    for _u in $PUBIP_V6_URLS; do
        (
            if command -v curl >/dev/null 2>&1; then
                _r="$(curl -fsS6 --max-time 2 "$_u" 2>/dev/null | tr -d ' \r\n\t')"
            else
                _r="$(wget -qO- --timeout=2 "$_u" 2>/dev/null | tr -d ' \r\n\t')"
            fi
            case "$_r" in *:*:*) printf "%s\n" "$_r" >> "$_out" ;; esac
        ) &
        _pids="$_pids $!"
    done
    # shellcheck disable=SC2086
    wait $_pids 2>/dev/null
    awk 'NF && !seen[$0]++' "$_out" | tr '\n' ' '
    rm -f "$_out"
}

PUBLIC_IPS_CACHE=""
PUBLIC_IPS_CACHED=0
public_ips() {
    if [ "$PUBLIC_IPS_CACHED" = "0" ]; then
        PUBLIC_IPS_CACHE="$(detect_public_ips)"
        PUBLIC_IPS_CACHED=1
    fi
    printf "%s" "$PUBLIC_IPS_CACHE"
}

# 打印一行 "标签 : http://本机/外网... [path]"
print_url_line() {
    _label="$1"; _p="$2"; _path="${3:-}"
    printf "  %-8s : ${C_BOLD}http://127.0.0.1:%s%s${C_RST}\n" "$_label" "$_p" "$_path"
    _pubs="$(public_ips)"
    [ -n "$_pubs" ] || return 0
    for _ip in $_pubs; do
        case "$_ip" in
            *:*) printf "             ${C_BOLD}http://[%s]:%s%s${C_RST}  ${C_BLU}(外网)${C_RST}\n" "$_ip" "$_p" "$_path" ;;
            *)   printf "             ${C_BOLD}http://%s:%s%s${C_RST}  ${C_BLU}(外网)${C_RST}\n"   "$_ip" "$_p" "$_path" ;;
        esac
    done
}

print_summary() {
    printf "\n%b\n" "${C_GRN}${C_BOLD}✓ 安装完成!${C_RST}"
    printf "%b\n" "────────────────────────────────────────────"
    print_url_line "访问地址" "$PORT"
    print_url_line "API 文档" "$PORT" "/api/docs"
    [ -n "$(public_ips)" ] && printf "  %b\n" "${C_YLW}注: 外网地址能否实际访问取决于防火墙/安全组/NAT 是否放行该端口${C_RST}"
    printf "  API 令牌 : ${C_BOLD}%s${C_RST}\n" "$TOKEN"
    printf "  配置文件 : %s\n" "$ENV_FILE"
    printf "  数据目录 : %s\n" "$DATA_DIR"
    print_cli_hint
    warn "请妥善保存 API 令牌, 它是访问后台的唯一凭证!"
}

# ----------------------------------------------------------------------------
# 全自动更新流程 (保留现有端口/令牌/数据, 仅替换二进制并重启服务)
# ----------------------------------------------------------------------------
do_update() {
    printf "%b\n" "${C_BOLD}=== cfdmgrd 全自动更新 ===${C_RST}"
    detect_platform
    detect_downloader
    ensure_root

    if [ ! -x "${INSTALL_DIR}/${BIN_NAME}" ]; then
        die "未检测到已安装的 ${BIN_NAME} (${INSTALL_DIR}/${BIN_NAME})。请先执行安装, 而非更新。"
    fi

    _cur="$(get_installed_version)"
    info "当前已安装版本: ${C_BOLD}${_cur:-未知}${C_RST}"

    resolve_version                 # 解析目标版本 (默认最新, 或 -v 指定)
    _target="${VERSION#v}"

    if [ -n "$_cur" ] && [ "$_cur" = "$_target" ] && [ "$FORCE" != "1" ]; then
        ok "已是最新版本 (${_cur}), 无需更新。"
        info "如需强制重装请加 --force"
        return 0
    fi

    info "准备更新: ${C_BOLD}${_cur:-?}${C_RST} -> ${C_BOLD}${_target}${C_RST}"
    download_and_install            # 下载并覆盖二进制 (不动配置)
    install_cli                     # 顺带刷新管理命令 cfm 到最新
    restart_service                 # 重启以加载新二进制

    # 尽力做一次健康检查 (端口取自现有配置)
    PORT="$(read_env_port)"
    if [ -n "$PORT" ]; then
        health_check
    else
        warn "未能读取到现有端口, 跳过健康检查 (服务应已重启)"
    fi

    printf "\n%b\n" "${C_GRN}${C_BOLD}✓ 更新完成!${C_RST} 版本: ${_target}"
    if [ -n "$PORT" ]; then
        PUBLIC_IPS_CACHED=0
        print_url_line "访问地址" "$PORT"
        [ -n "$(public_ips)" ] && printf "  %b\n" "${C_YLW}注: 外网地址能否实际访问取决于防火墙/安全组/NAT 是否放行该端口${C_RST}"
    fi
    info "现有端口、API 令牌与数据均未改动。"
    print_cli_hint
}

# ----------------------------------------------------------------------------
# 卸载流程
# ----------------------------------------------------------------------------
do_uninstall() {
    printf "%b\n" "${C_BOLD}=== cfdmgrd 卸载 ===${C_RST}"
    detect_platform
    ensure_root

    _init="$(detect_init_system)"
    case "$_init" in
        systemd)
            priv systemctl stop "${SERVICE_NAME}" >/dev/null 2>&1 || true
            priv systemctl disable "${SERVICE_NAME}" >/dev/null 2>&1 || true
            priv rm -f "/etc/systemd/system/${SERVICE_NAME}.service"
            priv systemctl daemon-reload || true
            ok "已移除 systemd 服务"
            ;;
        openrc)
            priv rc-service "${SERVICE_NAME}" stop >/dev/null 2>&1 || true
            priv rc-update del "${SERVICE_NAME}" default >/dev/null 2>&1 || true
            priv rm -f "/etc/init.d/${SERVICE_NAME}"
            ok "已移除 OpenRC 服务"
            ;;
        launchd)
            _plist="/Library/LaunchDaemons/com.miaclark.${SERVICE_NAME}.plist"
            priv launchctl unload "$_plist" >/dev/null 2>&1 || true
            priv rm -f "$_plist"
            ok "已移除 launchd 服务"
            ;;
    esac

    priv rm -f "${INSTALL_DIR}/${BIN_NAME}"
    ok "已删除二进制 ${INSTALL_DIR}/${BIN_NAME}"

    priv rm -f "${INSTALL_DIR}/cfm"
    ok "已删除管理命令 ${INSTALL_DIR}/cfm"

    prompt "是否同时删除配置文件与数据目录 (${DATA_DIR})? [y/N]" "N"
    case "$REPLY" in
        y|Y|yes|YES)
            priv rm -rf "$(dirname "$ENV_FILE")" "$DATA_DIR"
            ok "已删除配置与数据"
            ;;
        *)
            info "保留配置文件 ${ENV_FILE} 与数据目录 ${DATA_DIR}"
            ;;
    esac
    ok "卸载完成"
}

# ----------------------------------------------------------------------------
# 入口
# ----------------------------------------------------------------------------
main() {
    parse_args "$@"
    case "$ACTION" in
        install)   do_install ;;
        update)    do_update ;;
        uninstall) do_uninstall ;;
    esac
}

main "$@"
