#!/usr/bin/env bash
# =============================================================================
# build-ipk.sh — 生成单个「all 架构」的 luci-app-cfdmgrd OpenWrt .ipk
#
#   该包不含 CPU 二进制，只装壳子（procd 脚本 + UCI 配置 + cfdmgrd-fetch + LuCI 控制台）。
#   一个包到处可装，由控制台/命令行 cfdmgrd-fetch 按本机 CPU 联网下载对应版本的
#   cfdmgrd 二进制到 /usr/bin/cfdmgrd。cloudflared 连接器由守护进程运行时自动下载。
#
# 参数：
#   --version <x.y.z>   版本号（也可用环境变量 VERSION）。这是 cfdmgrd-fetch
#                       默认会去拉取的二进制版本，必须与 GitHub Release 一致。必填。
#   --release <n>       包修订号，默认 1   ->  包版本 <x.y.z>-<n>
#   --out <dir>         输出目录，默认 dist-ipk
#   --help
#
# 示例：
#   VERSION=0.0.14 ./openwrt/build-ipk.sh --out dist-ipk
#   ./openwrt/build-ipk.sh --version 0.0.14
#
# 依赖： nfpm (go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest)
# =============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
NFPM_CONF="$SCRIPT_DIR/nfpm.yaml"

OUT="dist-ipk"
VERSION="${VERSION:-}"
PKG_RELEASE="${PKG_RELEASE:-1}"

C_GRN=''; C_YLW=''; C_RED=''; C_RST=''
if [ -t 1 ]; then C_GRN='\033[0;32m'; C_YLW='\033[0;33m'; C_RED='\033[0;31m'; C_RST='\033[0m'; fi
info() { printf "%b\n" "[*] $*"; }
ok()   { printf "%b\n" "${C_GRN}[+]${C_RST} $*"; }
die()  { printf "%b\n" "${C_RED}[x]${C_RST} $*" >&2; exit 1; }

usage() { sed -n '2,28p' "$0" | sed 's/^# \{0,1\}//'; }

# 路径转 nfpm 能识别的本地形式（Windows/MSYS 下用 cygpath，Linux 原样）
to_native() {
	if command -v cygpath >/dev/null 2>&1; then cygpath -m "$1"; else printf '%s' "$1"; fi
}
# 转义 sed 替换串里的特殊字符（& \ 及分隔符 |）
sed_escape() { printf '%s' "$1" | sed -e 's/[\\&|]/\\&/g'; }

# ---------------------------------------------------------------------------
# 参数解析
# ---------------------------------------------------------------------------
while [ $# -gt 0 ]; do
	case "$1" in
		--version)  VERSION="${2:-}"; shift 2 ;;
		--release)  PKG_RELEASE="${2:-}"; shift 2 ;;
		--out)      OUT="${2:-}"; shift 2 ;;
		-h|--help)  usage; exit 0 ;;
		*)          die "未知参数: $1 (--help 查看用法)" ;;
	esac
done

command -v nfpm >/dev/null 2>&1 || die "未找到 nfpm，请先安装: go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest"
[ -n "$VERSION" ] || die "缺少版本号：用 --version 或环境变量 VERSION"
VERSION="${VERSION#v}"   # 统一去掉前导 v

# 随包文件
INITD_SRC="$ROOT_DIR/openwrt/files/etc/init.d/cfdmgrd"
CONFIG_SRC="$ROOT_DIR/openwrt/files/etc/config/cfdmgrd"
FETCH_SRC="$ROOT_DIR/openwrt/files/usr/sbin/cfdmgrd-fetch"
POSTINST="$ROOT_DIR/openwrt/scripts/postinst.sh"
PRERM="$ROOT_DIR/openwrt/scripts/prerm.sh"
POSTRM="$ROOT_DIR/openwrt/scripts/postrm.sh"
# LuCI web 控制台
LUA_CTRL="$ROOT_DIR/openwrt/luci-app-cfdmgrd/luasrc/controller/cfdmgr.lua"
LUA_VIEW="$ROOT_DIR/openwrt/luci-app-cfdmgrd/luasrc/view/cfdmgr/main.htm"
ACL_LUCI="$ROOT_DIR/openwrt/luci-app-cfdmgrd/root/usr/share/rpcd/acl.d/luci-app-cfdmgrd.json"
UCIDEF_LUCI="$ROOT_DIR/openwrt/luci-app-cfdmgrd/root/etc/uci-defaults/40_luci-cfdmgr"
for f in "$INITD_SRC" "$CONFIG_SRC" "$FETCH_SRC" "$POSTINST" "$PRERM" "$POSTRM" \
         "$LUA_CTRL" "$LUA_VIEW" "$ACL_LUCI" "$UCIDEF_LUCI" "$NFPM_CONF"; do
	[ -f "$f" ] || die "缺少随包文件: $f"
done

mkdir -p "$OUT"; OUT="$(cd "$OUT" && pwd)"
WORK="$(mktemp -d 2>/dev/null || mktemp -d -t cfdmgrd-ipk)"
cleanup() { [ -n "${WORK:-}" ] && rm -rf "$WORK"; }
trap cleanup EXIT INT TERM

# 生成随包 VERSION 文件（cfdmgrd-fetch 默认拉此版本）
VERSION_SRC="$WORK/VERSION"
printf '%s\n' "$VERSION" > "$VERSION_SRC"

# 渲染 nfpm 模板
YAML="$WORK/nfpm.yaml"
sed -e "s|__PKG_VERSION__|$(sed_escape "$VERSION")|g" \
    -e "s|__PKG_RELEASE__|$(sed_escape "$PKG_RELEASE")|g" \
    -e "s|__INITD_SRC__|$(sed_escape "$(to_native "$INITD_SRC")")|g" \
    -e "s|__CONFIG_SRC__|$(sed_escape "$(to_native "$CONFIG_SRC")")|g" \
    -e "s|__FETCH_SRC__|$(sed_escape "$(to_native "$FETCH_SRC")")|g" \
    -e "s|__VERSION_SRC__|$(sed_escape "$(to_native "$VERSION_SRC")")|g" \
    -e "s|__POSTINST__|$(sed_escape "$(to_native "$POSTINST")")|g" \
    -e "s|__PRERM__|$(sed_escape "$(to_native "$PRERM")")|g" \
    -e "s|__POSTRM__|$(sed_escape "$(to_native "$POSTRM")")|g" \
    -e "s|__LUA_CTRL__|$(sed_escape "$(to_native "$LUA_CTRL")")|g" \
    -e "s|__LUA_VIEW__|$(sed_escape "$(to_native "$LUA_VIEW")")|g" \
    -e "s|__ACL_LUCI__|$(sed_escape "$(to_native "$ACL_LUCI")")|g" \
    -e "s|__UCIDEF_LUCI__|$(sed_escape "$(to_native "$UCIDEF_LUCI")")|g" \
    "$NFPM_CONF" > "$YAML"

OUTFILE="$OUT/luci-app-cfdmgrd_${VERSION}-${PKG_RELEASE}_all.ipk"
info "打包 all 架构包: 版本 ${VERSION}-${PKG_RELEASE}"
nfpm package -f "$(to_native "$YAML")" -p ipk -t "$(to_native "$OUTFILE")" >/dev/null \
	|| die "nfpm 打包失败"

sz="$(wc -c < "$OUTFILE" 2>/dev/null | tr -d ' ')"
ok "$(basename "$OUTFILE")  [all]  ${sz} bytes  ->  ${OUT}"
