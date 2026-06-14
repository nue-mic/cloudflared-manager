# cfdmgrd · OpenWrt 打包（单个 all ipk）

把无头 cloudflared 隧道管理器 `cfdmgrd` 打成 OpenWrt 的**一个** ipk 包：`Architecture: all`，**一个包到处装**，不分 CPU 架构。装上即由 **procd** 守护，配置走 **UCI**（`/etc/config/cfdmgrd`），可改端口/登录令牌、开机自启、一键启停、彻底装卸。全程可在路由器网页（LuCI「服务 → Cloudflared Manager」）里操作。

> 本目录自包含：ipk 生成逻辑、随包脚本、服务/配置/LuCI 控制台文件全在这里。发布时由 CI（[.github/workflows/release.yml](../.github/workflows/release.yml) 的 `goreleaser` job）调 [build-ipk.sh](build-ipk.sh) 生成单个 `luci-app-cfdmgrd_<版本>-1_all.ipk` 并随对应 GitHub Release 一并上传。

---

## 一个 all 包怎么做到「到处装」

`cfdmgrd` 是按 CPU 编译的 Go 二进制，本来需要每个架构一个包。本方案用**「壳子包 + 安装时自取二进制」**把它收敛成一个 `all` 包：

```
all ipk（仅 ~15KB，不含二进制）
├── LuCI web 控制台（控制器 + 视图 + ACL + uci-defaults）  ← 网页里操作一切
├── /etc/init.d/cfdmgrd         procd 服务脚本（UCI→CFDM_* env，运行日志进 logd）
├── /etc/config/cfdmgrd         UCI 配置（端口/令牌/数据目录/自更新开关…）
├── /usr/sbin/cfdmgrd-fetch     二进制拉取器（架构检测 + 自建源下载）
└── /usr/lib/cfdmgrd/VERSION    随包版本号

opkg install 时 → 只装壳子（不下载二进制）→ enable 服务
用户开 LuCI（服务 → Cloudflared Manager）→ 点「下载/更新核心」→
  cfdmgrd-fetch：
  ① uname -m 识别本机 CPU → 映射到 goreleaser 资产架构
  ② 拉 cfdmgrd_<版本>_linux_<架构>.tar.gz，下载优先级：
       ⒈ 自建 gh-raw 源（首选）   {base}/cfd-mgr-releases/v<版本>/<file>
       ⒉ 公共 GitHub 代理（兜底）  {proxy}https://github.com/.../releases/download/...
       ⒊ GitHub 直连（最后兜底）
  ③ 解出二进制装到 /usr/bin/cfdmgrd
→ 在 LuCI 里配端口/令牌、启动、点「打开管理后台」管隧道
```

**cloudflared 连接器二进制**不在 ipk 内、也不由 `cfdmgrd-fetch` 拉：由 **cfdmgrd 守护进程自带的二进制自动更新**在启动后自动下载到数据目录（默认 `/usr/lib/cfdmgrd`），并随官方新版滚动更新（UCI `option cfd_autoupdate '1'`，默认开）。

---

## ⚠️ 受支持架构（重要）

`cfdmgrd` 依赖纯 Go SQLite（`modernc.org/sqlite`）做指标时序库，其底层 `modernc.org/libc` 在 **mips / mipsel / mips64 / mips64le / loongarch** 上无法编译，因此**这些架构没有发布产物**。`cfdmgrd-fetch` 检测到会直接报错引导。

| `uname -m` | 是否支持 | 拉取的资产架构 |
|---|---|---|
| x86_64 | ✅ | amd64 |
| aarch64 | ✅ | arm64 |
| armv7l / armhf | ✅ | armv7 |
| armv6l | ✅ | armv6 |
| i386/i686 | ✅ | 386 |
| riscv64 | ✅ | riscv64 |
| mips / mipsel / mips64le | ❌ 不支持（SQLite 限制） | — |
| loongarch64 | ❌ 不支持（SQLite 限制） | — |

> 常见可用机型：x86 软路由、arm64 路由（MT7622/MT7981、RK33xx、树莓派等）、较新 armv7 路由、riscv64 设备。**纯 mips 路由（多数老式 NOR flash 家用路由）不支持**——可在旁路由/软路由上跑 cfdmgrd，仅用其管理远端隧道。

## ⚠️ 空间约束

cfdmgrd 解压后约 15–20MB，cloudflared 连接器约 30MB，数据目录约需 **50MB** 持久空间：

| 设备 | 能否装 |
|---|---|
| 8/16MB NOR flash 家用路由（无外置存储） | ❌ 装不下（`cfdmgrd-fetch` 预检空间并报错引导 extroot） |
| 任意设备 + USB/SD 做 [extroot](https://openwrt.org/docs/guide-user/additional-software/extroot_configuration) | ✅ |
| 128MB+ NAND 机型（MT7621/798x 等） | ✅ |
| x86 软路由 | ✅ 推荐 |

`cfdmgrd-fetch` 下载前会 `df` 预检 `/usr/bin` 所在分区（cfdmgrd 约需 28MB），不足则中止并提示配置 extroot；数据目录另需为 cloudflared 留约 30MB。

---

## 安装与使用（全程网页操作）

```sh
# 上传 luci-app-cfdmgrd_<版本>-1_all.ipk 到路由器后：
opkg update
opkg install luci-base luci-compat   # 一般已装
opkg install luci-app-cfdmgrd_<版本>-1_all.ipk
```

装好后打开路由器后台 → **服务(Services) → Cloudflared Manager**：

1. **核心下载**页：点「下载 / 更新核心」，按本机 CPU 自动拉取 cfdmgrd（带实时进度日志）。
2. **概览 / 控制**页：配监听端口、登录令牌（留空=首启自动生成强随机）、数据目录；点「启动」。
3. 点「打开管理后台」跳到 cfdmgrd 自带的隧道管理面板（增删改启停隧道、看每实例日志、二进制自动更新设置）。

也可纯命令行：

```sh
cfdmgrd-fetch latest                 # 下载/更新最新核心
uci set cfdmgrd.main.http_addr=':18080'
uci set cfdmgrd.main.token='<你的令牌>'   # 留空则首启自动生成
uci commit cfdmgrd
/etc/init.d/cfdmgrd enable
/etc/init.d/cfdmgrd start
logread -e cfdmgrd                   # 查看运行日志
uci get cfdmgrd.main.token           # 忘记令牌时查看
```

## UCI 配置项（`/etc/config/cfdmgrd` main 节）

| 选项 | 默认 | 说明 |
|---|---|---|
| `enabled` | `1` | 是否启用（0=不启动） |
| `http_addr` | `:18080` | 监听地址 `:端口` 或 `ip:端口` |
| `token` | 空 | API 登录令牌；留空首启自动生成 |
| `data_dir` | `/usr/lib/cfdmgrd` | 数据根目录（须持久，含 cloudflared 二进制） |
| `log_level` | `info` | trace/debug/info/warn/error |
| `docs_enabled` | `1` | 是否开放 `/api/docs` |
| `cors_origins` | `*` | CORS 白名单 |
| `self_update` | `0` | 管理器自身自更新（OpenWrt 默认关，避免与 opkg 冲突） |
| `cfd_autoupdate` | `1` | cloudflared 连接器自动更新（默认开） |
| `version` | 空 | cfdmgrd-fetch 拉取的版本；留空=随包，`latest`=最新 |
| `no_proxy` | `0` | 1=跳过自建源+公共代理直连 GitHub |
| `download_proxy` / `release_proxy_bases` / `install_proxy_key` | — | 高级：覆盖下载源 |

## 升级 / 卸载

```sh
# 升级：重装新版 ipk（保留 UCI 配置与数据目录），再到控制台「下载/更新核心」拉新版 cfdmgrd
opkg install luci-app-cfdmgrd_<新版本>-1_all.ipk

# 卸载（保留用户数据目录）
opkg remove luci-app-cfdmgrd
```

> 管理器升级**不走** Web 端自更新（默认关闭，会与 opkg 冲突）：重装 ipk 或 `cfdmgrd-fetch <新版本>`。cloudflared 连接器的升级则由守护进程自动更新负责，与本包无关。

---

## 本地手动打包（开发用）

```sh
go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest
./openwrt/build-ipk.sh --version 0.0.14 --out dist-ipk
# 产出 dist-ipk/luci-app-cfdmgrd_0.0.14-1_all.ipk
```

## 备注：OpenWrt 25.12+ (apk)

OpenWrt 25.12 起默认包管理器从 opkg 切到 apk(APKv3)。本目录用 nfpm 产 **ipk**，适配 OpenWrt ≤24.10（当前主流）。25.12+ 设备如需原生 apk 包，需另走 OpenWrt SDK 制作 feed；ipk 在 25.12 上可经兼容层或手动解包安装，非首选。
