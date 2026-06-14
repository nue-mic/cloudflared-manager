# cloudflared-manager · OpenWrt ipk 打包 — 设计文档

> 日期：2026-06-14 · 状态：已批准（用户「全都按推荐来，直接开干」）
> 目标：把守护进程 `cfdmgrd` 打成 OpenWrt 的**单个 `Architecture: all` ipk**，自带 LuCI 控制台、核心二进制联网下载、procd 服务与运行日志，一个包到处装，全程网页操作。

---

## 1. 方案

**自包含 `openwrt/` 目录 + nfpm「壳子包」**：ipk 仅 ~15KB、不含 CPU 二进制，只装 procd 服务脚本 + UCI 配置 + `cfdmgrd-fetch` 拉取器 + LuCI web 壳子。装包时不下载；用户在 LuCI 控制台或命令行触发 `cfdmgrd-fetch`，按本机 CPU 联网下载对应 cfdmgrd 静态二进制到 `/usr/bin/cfdmgrd`。一个 `all` 包覆盖所有受支持架构，甩掉 opkg 架构串映射。

cloudflared **连接器**二进制不在 ipk 内：由守护进程自带的「二进制自动更新」（`internal/cfdupdate`）在运行时自动下载到 `data_dir` 并随官方新版滚动更新。

## 2. 关键约束与适配（与 frpc-manager 同构，但有实质差异）

1. **命名**：包 `luci-app-cfdmgrd`、二进制 `cfdmgrd`、菜单「Cloudflared Manager」、env 前缀 `CFDM_*`、OpenWrt 默认端口 `:18080`（避开 uhttpd/常见占用）。
2. **⚠️ 受支持架构（硬约束）**：cfdmgrd 依赖纯 Go SQLite（`modernc.org/sqlite` → `modernc.org/libc`），在 **mips / mipsel / mips64 / mips64le / loongarch** 上无法编译，goreleaser 本就不发这些产物。故 OpenWrt **仅支持 `amd64 / arm64 / armv7 / armv6 / 386 / riscv64`**。`cfdmgrd-fetch` 检测到不支持架构时**明确报错 + 引导**（不发起注定 404 的下载）。README 列清适配范围。
3. **双二进制 + 自动更新协同**：`cfdmgrd-fetch` 只拉守护进程（proxy key `cfd-mgr-releases`，资产 `cfdmgrd_<ver>_linux_<arch>.tar.gz`）；cloudflared 由守护进程二进制自动更新负责。OpenWrt 默认：`CFDM_CFD_AUTOUPDATE_ENABLED=1`（拉 cloudflared，不碰 opkg，安全）、`CFDM_SELF_UPDATE_ENABLED=0`（管理器自更新会覆盖 opkg 装的二进制，关掉；升级走重装 ipk / `cfdmgrd-fetch`）。
4. **持久化与空间**：`data_dir` 默认 `/usr/lib/cfdmgrd`，须持久（切勿用 /tmp、/var tmpfs）；cfdmgrd ~15–20MB + cloudflared ~30MB，约需 50MB；`cfdmgrd-fetch` 装前 `df` 预检 `/usr/bin`（cfdmgrd 约 28MB），不足引导 extroot；data_dir 不可写仅告警不阻断。

## 3. 文件清单（新增 `openwrt/`）

```
openwrt/
├── README.md                        适配范围 / 安装 / 升级 / 排错（含 mips 不支持说明）
├── build-ipk.sh                     sed 渲染 nfpm 模板（绝对路径，cygpath 兼容）→ 单 all ipk
├── nfpm.yaml                        打包清单（__占位符__；depends luci-base/luci-compat；
│                                    replaces/conflicts 旧包名 cfdmgrd）
├── files/
│   ├── etc/config/cfdmgrd           UCI 默认配置
│   ├── etc/init.d/cfdmgrd           procd 服务：UCI→CFDM_* env、自动生成强随机令牌、
│   │                                respawn 崩溃自启、stdout/stderr→logd（logread 可看）
│   └── usr/sbin/cfdmgrd-fetch       核心下载：架构检测(限支持集)+gh-raw→公共代理→直连+进度日志+原子安装
├── scripts/{postinst,prerm,postrm}.sh   生命周期
└── luci-app-cfdmgrd/                控制台（LuCI web 壳子）
    ├── luasrc/controller/cfdmgr.lua    菜单 + RPC：info/save/download/download_status/control
    ├── luasrc/view/cfdmgr/main.htm     状态/配置/下载核心(异步+日志尾)/启停/打开管理后台
    └── root/{etc/uci-defaults/40_luci-cfdmgr, usr/share/rpcd/acl.d/luci-app-cfdmgrd.json}
```

## 4. UCI 配置项（`/etc/config/cfdmgrd` main 节）

`enabled`(1) / `http_addr`(:18080) / `token`(空→首启自动生成) / `data_dir`(/usr/lib/cfdmgrd) /
`log_level`(info) / `docs_enabled`(1) / `cors_origins`(*) / `self_update`(0, 管理器自更新) /
`cfd_autoupdate`(1, cloudflared 连接器自动更新) / 以及 fetch 用：`version`/`download_proxy`/`no_proxy`/`release_proxy_bases`/`install_proxy_key`。

init.d 注入：`CFDM_API_TOKEN/HTTP_ADDR/DATA_DIR/LOG_LEVEL/DOCS_ENABLED/CORS_ORIGINS/SELF_UPDATE_ENABLED/CFD_AUTOUPDATE_ENABLED`。

## 5. 三块核心能力落地

- **控制台**（LuCI「服务 → Cloudflared Manager」）：显示架构/已装版本/运行状态/是否在下载；表单配端口·令牌·data_dir·日志级别·目标版本·enabled；「下载/更新核心」异步按钮（锁文件 `/tmp/cfdmgrd-fetch.running` 防重入，日志写 `/tmp/cfdmgrd-fetch.log`）；启动/停止/重启/开机自启；「打开管理后台」跳 `http://<lan-ip>:<port>` 守护进程自带 React 面板。
- **核心下载**（`cfdmgrd-fetch`，命令行 + 控制台共用）：`uname -m`+字节序 → 支持集映射（不支持即报错引导）；三级下载（自建 gh-raw `cfd-mgr-releases` → 公共代理 → GitHub 直连），逐源校验 `tar -tzf`；进度按 ≥512KB 打点；解包后原子改名安装避免 text-busy；记录 `INSTALLED` 版本。
- **运行日志**：procd `stdout 1`/`stderr 1` 接入 logd → `logread -e cfdmgrd`；控制台「下载核心」区实时 `tail` fetch 日志；隧道级日志在守护进程 Web UI。

## 6. 生命周期脚本

- `postinst`：`IPKG_INSTROOT` 非空（镜像构建期）直接退出；`/etc/init.d/cfdmgrd enable`；刷 LuCI 菜单缓存 + `rpcd reload`；打印引导（去控制台下载核心）。**不自动下载**。
- `prerm`：停止 + 禁用服务。
- `postrm`：刷 LuCI 缓存；若 `cfdmgrd-fetch` 仍在（升级场景）则保留二进制；否则真卸载，删 `/usr/bin/cfdmgrd` + `/usr/lib/cfdmgrd`（保留用户 data_dir 若用户改过路径）。

## 7. CI 集成（复刻 frpc，融进现有发布）

`release.yml` 的 `goreleaser` job 在 `setup-go` 后、`goreleaser release` 前加一步：
```
go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest
./openwrt/build-ipk.sh --version "${{ needs.bump.outputs.version }}" --out openwrt-dist
```
`.goreleaser.yml` 加 `release.extra_files: [{glob: ./openwrt-dist/*.ipk}]`，让 ipk 随本次 Release 一并上传。`openwrt-dist/` 在 `dist/` 之外，`goreleaser --clean` 不会清它。**push main 自动发版时 ipk 自动产出**，无独立 job。

## 8. 验证

- 全部 `*.sh` 过 `bash -n`；`*.lua` 结构核对、`acl.json` 过 JSON 解析。
- 本地装 nfpm，跑 `build-ipk.sh --version <x>` 生成 ipk，`tar -tzf`/`ar t` 校验内容清单与权限（init.d 0755、config noreplace、fetch 0755）。
- `go build ./...` 仍绿（本次不改 Go 源）。
- 真机冒烟（用户侧）：x86/arm64 OpenWrt `opkg install` → 控制台下载核心 → 启动 → 打开后台。

## 9. 非目标（YAGNI）

- 每架构 ipk / OpenWrt SDK feed（all 壳子已覆盖）。
- mips/loong 支持（受 SQLite 限制，明确不支持）。
- OpenWrt 25.12+ 的 apk(APKv3) 包格式（README 注明需 SDK 另走；nfpm 产 ipk 适配 ≤24.10，主流在用）。
