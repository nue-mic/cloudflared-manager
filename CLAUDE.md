# CLAUDE.md — cloudflared-manager 项目指南

> 本文件为本仓库的项目级指令，供 Claude Code 在本项目中工作时遵循。
> 全局通用规范（语言、Windows Shell、Git、各专家 Skill）见用户级 `~/.claude/CLAUDE.md`，此处**不重复**，只记录本项目特有、且最容易踩坑的信息。

---

## 1. 这是什么

一个 **无头（headless）的 cloudflared 隧道管理器**：把繁琐的手写配置 + `systemctl` 手动管理 N 个 cloudflared 进程，变成「装上守护进程 → 打开网页 → 点鼠标增删改启停隧道」。

- 仅支持 **token 模式**（remote-managed tunnels）：ingress / public-hostname / origin 配置在 Cloudflare Zero Trust dashboard 里管，本项目只管「用哪个 token 跑、用什么连接参数跑、跑没跑」。
- 后端：Go 守护进程 `cfdmgrd`，**监管 N 个外部 `cloudflared` 子进程**（每实例一个独立子进程，经 `internal/process.Worker`），对外暴露 HTTP REST + WebSocket API。**注意：不再内嵌 frp**——cloudflared 二进制由面板自带 + UI 下载/多版本并存（`internal/cfdbin`）。
- 前端：React + TypeScript + Vite + Ant Design 单页应用，构建产物 `web/dist` 通过 `//go:embed` **嵌进 Go 二进制**，生产环境同域。
- 单二进制交付，自带 systemd/OpenRC/launchd/Windows 服务安装脚本。

## 2. 技术栈

| 层 | 技术 |
|---|---|
| 后端 | Go 1.25+、标准库 `net/http`、`log/slog`、`coder/websocket`、纯 Go SQLite（指标时序）、监管外部 `cloudflared` 子进程 |
| 前端 | React 19 + TypeScript + Vite 8 + Ant Design 6 + axios + CodeMirror + `yaml` |
| 交付 | 单二进制（embed dist）、Docker 多阶段、`scripts/install.sh` / `install.ps1`、统一管理命令 `cfm` |

## 3. 架构与目录

```
cmd/cfdmgrd/main.go      # 入口：子命令 serve / health / version
internal/
  api/                   # HTTP 层：server.go 路由 + 各 *.go handler + openapi.yaml
  manager/               # 核心：实例注册表 + 生命周期(opMu 串行化) + 配置加载 + 自启动 + 快照
  process/               # cloudflared 子进程监管（Spawn/Stop/Wait，跨平台信号）
  cfdbin/                # cloudflared 二进制下载/校验/多版本 store
  metrics/               # 采样器（拉 cloudflared --metrics）+ SQLite 时序 + 告警引擎
  selfupdate/            # 守护进程自更新（委托 install 脚本）
  appcfg/                # 环境变量配置加载（CFDM_* → Config）
  eventbus/              # 事件总线，驱动 WS /events 推送
  logtail/  sysinfo/     # 子进程日志 tail + 文件 tail、系统监控
pkg/
  cfdconfig/             # TunnelConfigV1 配置模型（token 模式）+ YAML/JSON codec + 校验
  cfdflags/              # TunnelConfigV1 → cloudflared TUNNEL_* env / argv 投影 + env 白名单
  cfdstate/              # ConfigState 状态枚举
  version/               # 版本号（构建期 -ldflags 注入）
  util/                  # 文件/字符串等小工具
web/src/
  api/{client.ts,types.ts,schema.d.ts}  # axios 客户端 + 手写类型 + 由 openapi 生成的 schema
  pages/                 # 每个路由一个页面（Configs 是最复杂的核心页）
  components/ events/ theme/
scripts/                 # install.sh / install.ps1（含生成的 cfm 管理命令）/ api-smoke.sh
docs/API.zh-CN.md        # 完整 API 字段表（前后端对接的权威参考）
```

请求大致链路：`api/server.go` 路由 → `api/<name>.go` handler → `manager` 操作实例 → `process.Worker` spawn/stop `cloudflared tunnel run` 子进程 → 变更经 `eventbus` 推送到前端 WS → 前端事件驱动刷新。指标链路：`metrics.Sampler` 每 10s 拉每个运行实例的 cloudflared `--metrics` 端点 → 解析 Prometheus → 落 SQLite + 评估告警。

## 4. 常用命令（根目录 Makefile）

```bash
make build-host   # 本机平台构建 daemon（会先构建前端 dist 再 go build）→ bin/cfdmgrd
make build        # Linux/amd64 构建（发布/镜像用）
make web          # 仅构建前端 dist
make test         # go test ./...
make vet          # go vet ./...
make run          # 本机构建并以 dev token 启动：CFDM_API_TOKEN=dev serve
make docker       # 多阶段镜像（自带 node+go，无需本地依赖）
```

前端单独操作在 `web/` 下：`npm run dev`（vite）、`npm run build`（`tsc -b && vite build`）、`npm run lint`、`npm run gen:api`（由 `internal/api/openapi.yaml` 生成 `src/api/schema.d.ts`）。

API 烟测：`BASE=http://127.0.0.1:8080 TOKEN=dev bash scripts/api-smoke.sh`（需 daemon 已在跑）。

## 5. 本地开发流程

前后端**分离调试**：

1. 起后端：`make run`（监听 `:8080`，token=`dev`，数据写 `./tmp/data`）。
2. 起前端：`cd web && npm run dev`（监听 `:5173`）。`vite.config` 已把 `/api`、WS 代理到 `:8080`，前端 `client.ts` 用**相对路径** baseURL，所以走代理即可，无需配 CORS。
3. 浏览器开 `http://localhost:5173`，首次需在登录页填 API token（dev 环境即 `dev`）。token 存 localStorage（键 `cfdmgr_api_token`），axios 拦截器自动加 `Authorization: Bearer`，401 统一跳登录。

> 真正启动一个实例需要 PATH 中有可用的 `cloudflared`（或经 UI/`/binaries` 下载到 store），且 token 长度 ∈ [100,1500]。本地无真二进制时，可写一个假 `cloudflared`（读 `TUNNEL_METRICS` 起一个 /metrics、输出 JSON 日志、阻塞至被 kill）放进 PATH 来端到端联调。

生产/单二进制：前端已 embed，直接访问 daemon 端口同域即可。

## 6. ⚠️ 前后端 API 字段绑定（本项目第一大坑）

**改任何 `web/src/**` 里调用 `/api/v1/...` 的代码前，必须先激活 `web-api-binding` Skill 并读 Go 源确认字段名。** 这不是建议，是硬约束。核心原因：

- **大小写/命名风格不统一**：`TunnelConfigV1` 子树（`config` 字段、raw YAML、validate JSON）走 **camelCase**；`Snapshot` / 系统监控 / `AlertRule` / `AlertEvent` / `BinaryItem` / `AvailableRelease` / WS 事件外层走 **snake_case**；`cfdmgr`（实例元数据 `name`/`manualStart`）是 **camelCase**。
- **cloudflared 的不规则 camelCase**：`edgeIpVersion`（不是 `edgeIPVersion`）、`postQuantum`、`gracePeriod`、`advancedEnvOverrides`、`binaryVersion`、`transportLogLevel` —— 写错 key 不报错，但回读拿不到。
- **Go `encoding/json` 大小写不敏感**：写错 key 也能写成功，回读却找不到 —— 隐蔽性极强，「类型检查通过 ≠ 对接正确」，必须看一次真实请求/响应。
- **嵌套结构不能拍平**：`TunnelConfigV1` 是嵌套的（`edge.*` / `reliability.*` / `logging.*` / `identity.*`）。前端用真 YAML 库（`yaml`）解析，**不要**手写扁平 k:v 解析器，否则嵌套字段被拍平成未知顶层键 → 后端 `DisallowUnknownFields` 直接 400。
- **token 永不回传**：任何信封响应里 `config.token` 都被剥离；`has_token` 表示是否已存；掩码预览走 `GET /configs/{id}/token`。提交时 token 留空 = 保留实例现有 token（PUT/PATCH 后端会保留）。
- **列表快照 ≠ 编辑定义**：`GET /configs` 返回的是 snake_case `Snapshot`（无 `config`）；回填编辑表单要去 `GET /configs/{id}` 取完整信封（`config` 为 camelCase，token 已脱敏）。
- `decodeJSON`（[internal/api/helpers.go](internal/api/helpers.go)）启用 `DisallowUnknownFields()`，前端多发一个 key 会直接 400。

权威字段表见 [docs/API.zh-CN.md](docs/API.zh-CN.md) 与 [internal/api/openapi.yaml](internal/api/openapi.yaml)。

## 7. 配置（全部经环境变量）

由 [internal/appcfg](internal/appcfg) 读取，前缀 `CFDM_`：

| 变量 | 默认 | 说明 |
|---|---|---|
| `CFDM_API_TOKEN` | （必填） | API 鉴权令牌，登录后台凭证 |
| `CFDM_HTTP_ADDR` | `:8080` | 监听地址；可只填端口（如 `8080`，自动归一化为 `:8080`）或 `:端口`/`ip:端口` |
| `CFDM_DATA_DIR` | `/var/lib/cfdmgrd` | 数据根目录（profiles/logs/stores/meta.json/metrics.db/bin） |
| `CFDM_CORS_ORIGINS` | `*` | CORS 白名单 |
| `CFDM_LOG_LEVEL` | `info` | trace/debug/info/warn/error |
| `CFDM_DOCS_ENABLED` | `true` | 是否开放 `/api/docs` |
| `CFDM_RELEASE_PROXY_BASES` | 7 个 `gh-raw` 代理域名 | cloudflared 二进制下载走的 Release 代理域名（CSV，自动故障转移）；见 docs/三方对接_RELEASE_API.md |
| `CFDM_RELEASE_PROXY_KEY` | `cloudflared-releases` | Release 代理的配置键 |
| `CFDM_DOWNLOAD_MIRRORS` | `https://gh-proxy.org/,https://gh-proxy.com/` | 旧 GitHub 镜像（仅自更新用；二进制下载已改走 Release 代理） |
| `CFDM_GITHUB_TOKEN` | （空） | 可选（旧）；Release 代理无需 token |
| `CFDM_BINARIES_DIR` | `$DATA_DIR/bin/cloudflared` | 二进制存放目录 |
| `CFDM_CLOUDFLARED_DEFAULT_VERSION` | `latest` | Install 省略 version 时的默认值 |
| `CFDM_SELF_UPDATE_ENABLED` | `true` | 是否开放 Web 端自更新（守护进程自身） |
| `CFDM_CFD_AUTOUPDATE_*` | 见下 | cloudflared **二进制**自动更新种子默认（仅 `meta.json` 无 `auto_update` 块时生效，之后以设置页持久化值为准） |

> **二进制自动更新**（`internal/cfdupdate`）：启动自举（无二进制则下载激活最新）+ 定时检查下载激活并**滚动重启**跟随活跃版本的实例，失败**自动回滚**；显式钉 `binaryVersion` 的实例不受影响。env 种子默认：`CFDM_CFD_AUTOUPDATE_ENABLED`(true) / `_MODE`(full|download|notify) / `_INTERVAL_HOURS`(24) / `_PRERELEASE`(false) / `_ROLLBACK`(true) / `_KEEP`(3) / `_HEALTH_GRACE`(8)。运行时改设置走 `PUT /api/v1/binaries/auto-update`（存 `meta.json`）。

安装后配置落在 `/etc/cfdmgrd/cfdmgrd.env`（Linux）；数据目录默认 `/var/lib/cfdmgrd`。

## 8. 版本与发布

- 版本号在**构建期由 `-ldflags` 注入** [pkg/version](pkg/version)，不要在源码里硬编码。
- cloudflared 二进制**不内嵌**：由 `internal/cfdbin` 下载/校验/多版本管理，UI「二进制管理」页可检查/下载/切换；实例可经 `binaryVersion` 字段钉某个版本。
- 发布走 CI（`.github/workflows/release.yml`），release 提交形如 `chore(release): vX.Y.Z [skip ci]`。
- 运维统一用安装脚本生成的 **`cfm` 命令**（共 18 个子命令），自动适配 systemd/OpenRC/launchd/Windows 服务。改动 `install.sh`/`install.ps1` 只对新装或下次 `cfm update` 生效。

  **服务管理（7 个）**：
  - `cfm start` — 启动 cfdmgrd 守护进程。
  - `cfm stop` — 停止 cfdmgrd 守护进程。
  - `cfm restart` — 重启守护进程（stop + start）。
  - `cfm status` — 查看服务运行状态（PID/活跃/退出码）。
  - `cfm logs [-f]` — 查看守护进程日志，`-f` 实时跟随输出。
  - `cfm enable` — 设置开机自启。
  - `cfm disable` — 取消开机自启。

  **信息查看（3 个）**：
  - `cfm info` — 显示监听地址、数据目录与 API 令牌（忘令牌时用）。
  - `cfm version` — 显示守护进程版本号与构建信息。
  - `cfm config [edit]` — 查看 `/etc/cfdmgrd/cfdmgrd.env`，加 `edit` 用默认编辑器打开。

  **安装维护（3 个）**：
  - `cfm install [--version=X]` — 安装/重装 cfdmgrd，`--version` 可指定版本（省略=latest）。
  - `cfm update [--version=X]` — 升级到指定版本（省略=latest），保留配置与数据。
  - `cfm uninstall [--purge]` — 卸载守护进程，`--purge` 同时删除数据目录。

  **进阶辅助（4 个，本次新增）**：
  - `cfm doctor` — 8 项健康自检：进程存活 / HTTP 端口 / API token / cloudflared 二进制 / 数据目录可写 / DNS 解析 / Cloudflare API 连通性 / Release 代理可达，输出 `X OK / Y WARN / Z FAIL` 汇总。
  - `cfm backup [<path>] [--include-logs]` — 打包 `meta.json` / `profiles/` / `metrics.db` / `bin/manifest` 为 `cfdmgrd-backup-YYYYMMDD-HHMMSS.tar.gz`，附 `backup-info.json` 元数据；`--include-logs` 才打 `logs/`。
  - `cfm restore <path> [--force]` — 解包并校验后停服恢复；`DATA_DIR` 已有数据需 `--force` 才清空覆盖。
  - `cfm watch [--interval=N]` — 终端实时面板（btop 风格，纯 bash + tput），`--interval` 设刷新秒数。

  - `cfm help` — 列出全部子命令与简要说明。

## 9. 提交规范

Conventional Commits + **中文描述**，与现有历史一致：`feat(scope): …`、`fix(scope): …`、`chore(deps): …`。示例：`feat(tunnel): 隧道增删改自动热重载`。

## 10. 其它约束

- **Windows 开发环境**：遵循全局 `windows-shell` 规范（禁 `&&`、bash 专有语法）。注意：含中文的 `.ps1` 必须带 UTF-8 BOM（PS 5.1 否则按 ANSI 误解析），但 `.cmd`/JSON/Go/TS 等不要 BOM。
- 修改 `internal/api` 的请求/响应结构后，记得同步 `openapi.yaml`、`docs/API.zh-CN.md`，必要时跑 `npm run gen:api` 重生成前端 schema。
- 验证以事实为准：声称「修好了」前，后端跑 `make test`/`go vet`，前端跑 `tsc -b`，涉及对接的再看一次真实 Network 请求（或跑 `scripts/api-smoke.sh`）。

## 11. GitHub API / Git 访问令牌（强制）

> ⚠️ 任何需要访问 `github.com/nue-mic/cloudflared-manager` 的 GitHub API（`actions/runs`、`pulls`、`issues`、release 状态、CI 日志等）或拉私有数据时，**必须**先加载本仓 token，不要再去问用户。

**优先顺序**（命中即停）：

1. 仓库内 `.claude.local/github-tokens.env`（已 git-ignored，**严禁** commit）
2. 全局 `~/.claude/secrets/github-tokens.env`（在仓库外，永远安全）

加载方式：

```bash
[ -f .claude.local/github-tokens.env ] && source .claude.local/github-tokens.env
[ -f "$HOME/.claude/secrets/github-tokens.env" ] && source "$HOME/.claude/secrets/github-tokens.env"
curl -H "Authorization: Bearer $GH_MIA_CLARK_TOKEN" https://api.github.com/repos/nue-mic/cloudflared-manager/...
```

**安全红线**：

- token 明文绝不进 commit message / PR body / 公开聊天 / 截图 / 任何 push 到远端的内容（否则 GitHub secret-scanning 立即吊销）
- `.gitignore` 已含 `.claude.local/` 与 `.claude/secrets/`，新建 token 文件请放在这两个目录之一
- 若 token 失效：要求用户重发，不要把无效 token 留在历史里

## 12. Cloudflare 账号直连集成（CF Integration）

在「token 模式跑进程」之上叠加的一层「直连 Cloudflare 官方 API、本地复刻隧道后台」能力。权威设计见 [docs/CF-集成设计.zh-CN.md](docs/CF-集成设计.zh-CN.md)，字段表见 [docs/API.zh-CN.md](docs/API.zh-CN.md) 的「Cloudflare 账号直连集成」节。

- **新增包**：
  - `internal/cfapi`：纯 Cloudflare API 客户端（无存储）。两种认证（API Token / 邮箱+Global Key）、envelope 解析、`DecodeTunnelToken`（base64(JSON{a,t,s}) → account_tag/tunnel_id，**本地解码做归属校验**）、cfd_tunnel CRUD、configurations(ingress)、connections、zones、DNS records。
  - `internal/cfaccount`：账号 + 实例绑定的**加密存储**。AES-256-GCM，DEK 在 `$DATA_DIR/secret.key`（首次自动生成 0600），文件 `$DATA_DIR/cf-store.json`（0600）。secret 永不回传；加载时自动加密迁移旧明文。
  - `internal/api/cf_*.go`：`cf.go`(共享 + `registerCFRoutes`)、`cf_accounts.go`、`cf_tunnels.go`、`cf_dns.go`、`cf_link.go`(绑定 + 公共主机名聚合)。
- **连线**：`appcfg.Config` 加 `CFStoreFile`/`SecretKeyFile`；`main.go` 建 `cfaccount.Store` 注入 `api.Deps.CFAccounts`，并订阅 eventbus `config.deleted` 清理绑定；store 为 nil 时整段路由不注册（降级）。
- **大小写坑（接 §6）**：账号视图/隧道/zone/DNS/绑定外层 = **snake_case**；隧道「配置」子树（`ingress[]`、`originRequest`）= cloudflared 原生 **camelCase**（`noTLSVerify`/`httpHostHeader`/`http2Origin`/`access.{required,teamName,audTag[]}` …），`originRequest` 后端用 `map[string]any` 原样直传，写错 key 不报错但不生效；`warp-routing` 含连字符。
- **前端**：`web/src/api/{client.ts(cfApi),types.ts(CF*)}`（手写契约，未走 schema.d.ts）；页面 `pages/CFAccounts.tsx`、`pages/CFConsole.tsx`、组件 `components/instance/InstanceCFPanel.tsx`。改这些前端文件前同样遵守 `web-api-binding` 纪律。
