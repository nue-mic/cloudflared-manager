# cloudflared-manager（cfdmgrd）

> 一个用浏览器就能管理多套 **cloudflared 隧道** 的「无头 cloudflared 管理器」。
> 一个守护进程同时托管多份 cloudflared 隧道实例，自带 Web 管理面板 + 完整
> REST + WebSocket API，开机自启、token 可视化编辑、运行时监控、历史指标曲线、
> 告警，专为服务器 / Docker 设计。

简单说：你不用再手动写 cloudflared 配置文件、用 `systemctl` 一个个管理隧道
进程了。装上它，打开网页，点点鼠标就能新增 / 启停 / 编辑 / 监控你的所有
cloudflared 隧道实例。

> 仅支持 **token 模式**（remote-managed tunnels）— ingress / public hostname
> / origin 配置在 Cloudflare Zero Trust dashboard 里管。
> cloudflared 二进制由面板自带 + UI 升级 / 多版本并存。

---

## ✨ 能力一览

- 🖥️ **Web 管理面板**：打开 `http://你的IP:端口/` 就是后台
- 🧩 **多实例并行**：一个守护进程同时管 N 份 cloudflared，每个独立子进程
- 🛠️ **token + cloudflared 参数可视化编辑**（YAML 双向）
- 📦 **cloudflared 二进制管理**：自带版本 + UI 检查更新 / 下载 / 切换版本
- 📡 **实时日志**：JSON 结构化解析 + 过滤 + 多实例并行查看
- 📈 **历史指标**：SQLite 时序，每 10s 拉 cloudflared `/metrics` 端点
- 🚨 **告警引擎**：阈值规则（HA 连接数 / 错误率 / 重连频率） + webhook
- 🔌 **REST + WebSocket API**：方便二次开发
- 🔐 **Bearer 鉴权**
- 📊 **系统监控**：CPU / 内存 / 磁盘 / 网络 / 连接 / 进程

---

## 🚀 一键安装

### Linux / macOS

```sh
curl -fsSL https://raw.githubusercontent.com/mia-clark/cloudflared-manager/main/scripts/install.sh | sh
```

非交互：
```sh
curl -fsSL https://raw.githubusercontent.com/mia-clark/cloudflared-manager/main/scripts/install.sh | sh -s -- -y -p 9000 -t 我的令牌
```

国内（镜像加速）：
```sh
curl -fsSL https://gh-proxy.com/raw.githubusercontent.com/mia-clark/cloudflared-manager/main/scripts/install.sh | sh
```

全自动更新（保留端口/令牌/数据，只换程序并重启）：
```sh
curl -fsSL https://raw.githubusercontent.com/mia-clark/cloudflared-manager/main/scripts/install.sh | sh -s -- --update --force
```

### Windows（管理员 PowerShell）

```powershell
irm https://raw.githubusercontent.com/mia-clark/cloudflared-manager/main/scripts/install.ps1 | iex
```

指定端口 + 令牌：
```powershell
$env:CFDM_PORT=9000; $env:CFDM_API_TOKEN='我的令牌'; $env:ASSUME_YES=1; irm https://raw.githubusercontent.com/mia-clark/cloudflared-manager/main/scripts/install.ps1 | iex
```

### Docker

```sh
docker run -d --name cfdmgrd \
  -p 8080:8080 \
  -v cfdmgrd-data:/data \
  -e CFDM_API_TOKEN=your-token \
  ghcr.io/mia-clark/cloudflared-manager:latest
```

---

## ⚙️ 配置环境变量

| 变量 | 默认 | 说明 |
|---|---|---|
| `CFDM_API_TOKEN` | （必填） | API 鉴权令牌 |
| `CFDM_HTTP_ADDR` | `:8080` | 监听地址 |
| `CFDM_DATA_DIR` | `/var/lib/cfdmgrd` | 数据根目录 |
| `CFDM_CORS_ORIGINS` | `*` | CORS 白名单 |
| `CFDM_LOG_LEVEL` | `info` | 日志级别（trace/debug/info/warn/error） |
| `CFDM_DOCS_ENABLED` | `true` | 暴露 /api/docs |
| `CFDM_DOWNLOAD_MIRRORS` | `https://gh-proxy.org/,https://gh-proxy.com/` | cloudflared 二进制下载镜像（CSV） |
| `CFDM_GITHUB_TOKEN` | （空） | 可选；提升 GitHub API 限流 |
| `CFDM_BINARIES_DIR` | `$DATA_DIR/bin/cloudflared` | 二进制存放目录 |

安装后配置文件位置：
- **Linux**：`/etc/cfdmgrd/cfdmgrd.env`（数据目录 `/var/lib/cfdmgrd`）
- **macOS**：launchd plist（数据目录 `/usr/local/var/cfdmgrd`）
- **Windows**：NSSM 服务环境变量（数据目录 `%ProgramData%\cfdmgrd\data`）

改完后 `cfm restart` 生效。

---

## 🛠️ 运维命令（`cfm`）

安装脚本会自动生成 `cfm` 管理命令（已加入 PATH），自动适配 systemd / OpenRC / launchd / Windows 服务：

```
cfm start           # 启动服务
cfm stop            # 停止服务
cfm restart         # 重启服务
cfm status          # 查看运行状态
cfm logs [-f]       # 查看日志（加 -f 实时跟踪）
cfm enable          # 设置开机自启
cfm disable         # 取消开机自启

cfm info            # 显示完整运行信息（地址/令牌/路径/状态）← 忘了令牌看这个
cfm config [edit]   # 查看（或 edit 编辑）配置文件
cfm version         # 显示版本信息

cfm install [参数]  # 重新安装（参数透传给 install.sh / install.ps1）
cfm update          # 更新到最新版（保留端口/令牌/数据）
cfm uninstall       # 卸载

cfm help            # 显示帮助
```

---

## 📋 安装脚本参数

| 参数 | 作用 |
|---|---|
| `-p, --port <端口>` | 指定监听端口；传 `random` 随机端口；省略则交互/默认 `8080` |
| `-t, --token <令牌>` | 指定 API 令牌；省略则交互输入，留空自动生成强随机令牌 |
| `-v, --version <版本>` | 指定版本（如 `v2.0.0`）；省略安装最新版 |
| `-y, --yes` | 全自动模式，端口默认 + 令牌随机 |
| `-u, --update` | 全自动更新（保留现有端口/令牌/数据） |
| `-f, --force` | 配合 `--update`，即使已是最新也强制重装 |
| `--uninstall` | 卸载 |
| `--proxy <URL>` | 指定单一下载代理（如 `https://my.mirror/`），跳过内置代理数组 |
| `--no-proxy` | 跳过所有代理，直连 GitHub |
| `-h, --help` | 帮助 |

也支持环境变量：`CFDM_PORT=9000 CFDM_API_TOKEN=xxx ASSUME_YES=1 CFDM_DOWNLOAD_PROXY=https://my.mirror/`。

---

## 🧭 用起来

| 用途 | 地址 / 命令 |
|---|---|
| **Web 管理面板** | `http://你的IP:端口/` |
| **在线 API 文档** | `http://你的IP:端口/api/docs/`（Scalar UI） |
| **健康检查** | `curl http://你的IP:端口/api/v1/health` |
| **调用 API** | `curl -H "Authorization: Bearer 你的令牌" http://你的IP:端口/api/v1/version` |

第一次打开 Web 面板，需要填入安装时设置/生成的 **API 令牌** 才能登录。忘了令牌？执行 `cfm info` 查看。

---

## 🏗️ 架构（一句话版）

```
浏览器/API 客户端
       │ Bearer
       ▼
┌──────────────────────────────────────────────┐
│ cfdmgrd (父进程, REST+WS+embed 前端)          │
│  ├── manager: 实例注册表 + 生命周期           │
│  ├── eventbus: WS 推送（状态/告警）           │
│  ├── metrics: 采样器+SQLite+告警引擎          │
│  └── workers: 管理 N 个 cloudflared 子进程   │
└──────────────────────────────────────────────┘
       │ spawn cloudflared --tunnel run --token ...
       ▼
┌──────────────────────────────────────────────┐
│ cloudflared × N（每个独立子进程）             │
│  经 --metrics 端点暴露 Prometheus 指标        │
│  日志经 stdout/stderr 管道采集               │
└──────────────────────────────────────────────┘
       │ QUIC / HTTP/2
       ▼
   Cloudflare 网络（自动管理隧道路由）
```

设计细节见 [`docs/superpowers/specs/2026-06-06-cloudflared-manager-design.md`](docs/superpowers/specs/2026-06-06-cloudflared-manager-design.md)。

---

## 🛠️ 开发与构建

```bash
make run            # 本地直接运行（含 dev token）
make test           # 单测
make build          # 交叉编译 Linux 静态二进制 → bin/cfdmgrd
make build-host     # 本地平台二进制
make docker         # 构建镜像
```

前端单独操作：

```bash
cd web
npm ci
npm run dev         # vite 起 :5173，代理到后端 :8080
npm run build       # 构建 dist（embed 进 Go 二进制）
npm run lint
npm run gen:api     # 由 openapi.yaml 重生成 src/api/schema.d.ts
```

### 目录结构

```
cmd/cfdmgrd/          # 守护进程入口
internal/api/         # HTTP/WS 路由 + 中间件 + OpenAPI spec
internal/manager/     # 实例注册表 + 生命周期 + worker 子进程监管
internal/eventbus/    # 进程内事件（WS 推送源）
internal/logtail/     # 文件 tail（WS 日志）
internal/sysinfo/     # 系统监控
internal/appcfg/      # 环境变量解析（CFDM_*）
pkg/cfdconfig/        # cloudflared token 模式配置模型
pkg/cfdflags/         # cloudflared 命令行标志集
pkg/version/          # 版本号（-ldflags 注入）
web/                  # 前端 React+Vite+AntD（产物 embed 进二进制）
deploy/               # Dockerfile + docker-compose + .env.example
docs/                 # 部署文档 + OpenAPI + 设计/规划历史
scripts/              # install.sh / install.ps1 / api-smoke.sh
```

---

## ❓ 常见问题

- **打开网页提示 401？** 令牌填错。执行 `cfm info` 查看当前令牌。
- **服务起不来 / 端口被占用？** 换端口：改 `CFDM_HTTP_ADDR=:新端口` 后 `cfm restart`；或重装用 `-p`。
- **创建 cloudflared 实例后启动失败？** Web 面板的「日志」tab 看子进程 stderr；检查 token 是否有效。
- **想换成开机不自启？** `cfm disable`（跨平台通用）。
- **忘记 API 令牌？** `cfm info` 看配置信息。

---

## 📚 文档

- API 参考：`http://你的IP:端口/api/docs/`（在线 Scalar UI）
- 详细 spec：`docs/superpowers/specs/2026-06-06-cloudflared-manager-design.md`

---

## 📄 许可证

GPL-3.0
