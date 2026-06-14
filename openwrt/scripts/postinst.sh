#!/bin/sh
# =============================================================================
# nfpm postinstall — 只装 web 壳子，不自动下载二进制。
#   核心二进制由 LuCI 网页（服务 → Cloudflared Manager → 下载核心）或命令行
#   cfdmgrd-fetch 触发下载。opkg/apk 安装时执行（镜像构建期 $IPKG_INSTROOT 非空跳过）。
# =============================================================================
[ -n "${IPKG_INSTROOT}" ] && exit 0

# 启用服务（开机自启）；二进制下载安装后才能真正启动
[ -x /etc/init.d/cfdmgrd ] && /etc/init.d/cfdmgrd enable >/dev/null 2>&1

# 立即刷新 LuCI 菜单/模块缓存并重载 rpcd，让菜单与 ACL 立即出现
# （opkg 场景；apk 场景由 /etc/uci-defaults/40_luci-cfdmgr 在下次启动兜底）
rm -f  /tmp/luci-indexcache* 2>/dev/null
rm -rf /tmp/luci-modulecache 2>/dev/null
/etc/init.d/rpcd reload >/dev/null 2>&1

_addr="$(uci -q get cfdmgrd.main.http_addr 2>/dev/null)"
[ -n "$_addr" ] || _addr=":18080"

echo ""
echo "==================================================================="
echo " luci-app-cfdmgrd 已安装 ✓（web 壳子）"
echo "-------------------------------------------------------------------"
echo " 打开路由器后台 → 服务(Services) → Cloudflared Manager："
echo "   ① 下载 / 更新核心二进制 cfdmgrd"
echo "   ② 配置端口 / 登录令牌"
echo "   ③ 启动服务，再点「打开管理后台」管理隧道"
echo ""
echo " 也可命令行下载核心: cfdmgrd-fetch latest"
echo " cfdmgrd 自带后台: http://<路由器IP>${_addr}"
echo " 提示: cloudflared 连接器二进制由守护进程启动后自动下载，无需手动处理。"
echo "==================================================================="
echo ""

exit 0
