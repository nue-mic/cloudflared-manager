#!/bin/sh
# =============================================================================
# nfpm postremove — 清理 cfdmgrd-fetch 装到 /usr/bin 的二进制（opkg 不追踪它，
#   因为是按需联网下载的，不在包文件清单里，需手动删）。
#
#   升级判定：postrm 在包文件删除后执行；若升级，新包的 cfdmgrd-fetch 仍在，
#   则跳过删除二进制（交给新包流程重新拉取）；仅真正卸载时清理。
#   保留用户 data_dir（隧道配置/数据），避免误删用户资产。
#   镜像构建阶段 ($IPKG_INSTROOT 非空) 跳过。
# =============================================================================
[ -n "${IPKG_INSTROOT}" ] && exit 0

# 刷新 LuCI 菜单缓存（升级/卸载都做）
rm -f  /tmp/luci-indexcache* 2>/dev/null
rm -rf /tmp/luci-modulecache 2>/dev/null

# 升级场景：新包 fetcher 仍在 -> 不删二进制
[ -x /usr/sbin/cfdmgrd-fetch ] && exit 0

# 真正卸载：删下载的二进制与随包元数据目录里的下载产物，
# 但保留用户 data_dir（默认 /usr/lib/cfdmgrd 同目录，仅删 INSTALLED/VERSION 标记，
# 不递归删整个目录，避免连用户隧道数据一起删）。
rm -f /usr/bin/cfdmgrd
rm -f /usr/lib/cfdmgrd/INSTALLED 2>/dev/null

exit 0
