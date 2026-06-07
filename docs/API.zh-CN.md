# frpsmgrd API 详细参考（frps 服务端管理器 · v1）

> ⚠️ **DEPRECATED**：本文档是 frps-manager 时代的 API 字段表，**与 v2.0.0
> cloudflared-manager 的 API 已不兼容**。新 API 字段以
> `internal/api/configs.go` / `internal/api/binaries.go` Go 源码
> 为权威，前端类型定义见 `web/src/api/types.ts`。完整 OpenAPI 文档
> 重写留待后续 PR。

> 本文件依据当前 [`internal/api`](../internal/api/) 与 [`internal/manager`](../internal/manager/) 实地核对生成，覆盖路径、请求体、响应体、错误码的全部字段。
> 凡是与 [`internal/api/openapi.yaml`](../internal/api/openapi.yaml) 不一致之处，请同步修复两者；正常情况下二者完全等价。

---

## 0. 全局约定

| 项目 | 值 |
|---|---|
| 监听地址 | `FRPSMGR_HTTP_ADDR`，默认 `:8080` |
| 数据目录 | `FRPSMGR_DATA_DIR`，默认 `/data`（子目录：`profiles/`、`logs/`、`stores/`、`meta.json`） |
| 鉴权 | 除 `/api/v1/health` 与 `/api/docs/*` 外，所有 `/api/v1/*` 都要求 `Authorization: Bearer <FRPSMGR_API_TOKEN>` |
| Content-Type | 除特别说明（`/raw`、`/import/*`、`/validate`、`/export/*`、WS）外，**请求/返回均为 `application/json; charset=utf-8`** |
| JSON 严格性 | 后端 `decodeJSON` 启用 `DisallowUnknownFields()`，请求体多带一个 key 直接 **`400`** |
| 401 时机 | 缺失或错误 Bearer Token；前端拦截器会清理 token 并跳转 `/login` |
| 路径 ID 限制 | 不允许路径分隔符与 shell 特殊字符（`/ \ : ? * < > | " '`），不能以 `.` 开头，长度 ≤ 64 |
| WebSocket 子路径 | `/api/v1/events`、`/api/v1/configs/{id}/logs/tail` —— 浏览器无法自定义 WS Header，故支持 `?token=...` 查询参数；CORS 由 `FRPSMGR_CORS_ORIGINS` 控制 |

### 0.1 错误响应统一信封

所有非 2xx 业务错误统一返回：

```json
{
  "error": {
    "code": "bad_request",
    "message": "id and config are required",
    "details": { "...optional": "..." }
  }
}
```

| `code` | 典型 HTTP | 说明 |
|---|---|---|
| `bad_request` | 400 | 请求体 / 参数不合法 |
| `unauthorized` | 401 | Token 缺失或无效 |
| `forbidden` | 403 | 鉴权通过但禁止访问 |
| `not_found` | 404 | 通用未找到 |
| `conflict` | 409 | 资源冲突 |
| `validation_failed` | 400 | 业务校验失败 |
| `internal_error` | 500 / 503 | 服务端异常 / 子系统未就绪（如度量存储禁用） |
| `config_not_found` | 404 | 实例 ID 不存在 |
| `config_already_exists` | 409 | 实例 ID 已存在 |
| `invalid_state` | 409 | 状态机违例（如未运行不能 reload、已运行不能 start） |
| `upstream_failure` | 502 | 经 worker loopback 访问 frps 失败、远程下载失败 |

来源：[`apiresp/apiresp.go`](../internal/api/apiresp/apiresp.go)、[`errors.go`](../internal/api/errors.go)、[`helpers.go`](../internal/api/helpers.go)。

### 0.2 关键架构事实（绑定字段前必须先看一眼）

- **子进程模型**：每个运行中的 frps 都是父进程 re-exec 自身得到的独立子进程（`frps-worker`，见 [`cmd/frpsmgrd/frps_worker.go`](../cmd/frpsmgrd/frps_worker.go)）。原因是上游 `mem.StatsCollector` 是进程级全局单例，同进程跑多个 frps 会把所有实例流量混在一起、无法按实例分离。
- **`webServer` 强制 loopback**：worker 启动前父进程预分配 `127.0.0.1:N` 空闲端口，强制覆盖 `webServer.addr/port/user/password`，账密随机；父进程通过该 loopback 反向读取 frps 原生 `/api/serverinfo`、`/api/proxy/{type}`、`/api/clients` 等运行时指标。**用户配置里的 `webServer` 字段会被忽略**，外部无法访问 frps 自身 dashboard，管理面统一走本守护进程的 HTTP API。
- **reload = 重启**：frps 服务端参数没有 in-place 热重载语义。`POST .../reload` 的实现就是 `stop()` → `start()`（见 [`manager/instance.go#reload`](../internal/manager/instance.go)）。
- **每实例独立日志**：`<FRPSMGR_DATA_DIR>/logs/<id>.log`，worker 的 stdout/stderr 全量落盘。`DELETE .../logs` 只更新 `log_view_since` 水位，不删盘上文件。
- **配置数据模型分两种 JSON 命名风格（最大踩坑点）**：
  - **业务配置（`config` 字段）= camelCase**：对应 [`pkg/config.ServerConfigV1`](../pkg/config/server.go)，内嵌上游 `github.com/fatedier/frp/pkg/config/v1.ServerConfig`，字段如 `bindPort` / `vhostHTTPPort` / `vhostHTTPSPort` / `kcpBindPort` / `quicBindPort` / `auth.method` / `transport.tcpMux` / `webServer.password` / `log.level` 等。
  - **快照 / 元数据 / 事件 / 告警 / 流量 = snake_case**：`Snapshot`（`id` / `name` / `path` / `state` / `last_error` / `started_at` / `stopped_at`），`TrafficPoint`、`AlertRule`、`AlertEvent`、WS `Event` 全部 snake_case。
  - **管理器元数据（`frpsmgr` 字段）= camelCase**：[`manager.MgrMeta`](../internal/manager/manager.go) 只有 `name` 与 `manualStart` 两个字段，不写入 frps TOML，落 `meta.json`。
- **`/runtime/*` 是 frps 原生 JSON 透传**：守护进程经 worker loopback 代理 frps 原生 `/api/serverinfo`、`/api/proxy/{type}`、`/api/proxies/{name}`、`/api/clients` 后**原样回写**响应体；字段形态以 frps 上游为准（**camelCase**），不是本项目 Snapshot 的 snake_case 风格。

### 0.3 配置环境变量（[`internal/appcfg`](../internal/appcfg/appcfg.go)）

| 变量 | 默认 | 说明 |
|---|---|---|
| `FRPSMGR_API_TOKEN` | （必填） | API 鉴权令牌 |
| `FRPSMGR_HTTP_ADDR` | `:8080` | 监听地址 |
| `FRPSMGR_DATA_DIR` | `/data` | 数据根目录 |
| `FRPSMGR_CORS_ORIGINS` | `*` | CORS 白名单（CSV） |
| `FRPSMGR_LOG_LEVEL` | `info` | trace/debug/info/warn/error |
| `FRPSMGR_DOCS_ENABLED` | `true` | 是否挂载 `/api/docs/*` |

---

## 1. 鉴权与健康

### 1.1 `GET /api/v1/health` — 探活（无需鉴权）

无请求体。返回 `200`：

```json
{ "status": "ok", "uptime_s": 12 }
```

### 1.2 `GET /api/v1/version` — 版本

需要鉴权。返回 `200`：

```json
{ "daemon": "1.2.23", "frp": "0.69.1", "build_date": "unknown" }
```

`daemon`、`build_date` 由构建期 `-ldflags` 注入；`frp` 取自 `github.com/fatedier/frp/pkg/util/version.Full()`。

### 1.3 `GET /api/v1/version/check` — 检查最新版本

查询 GitHub 最新 release 并与当前版本对比。后端结果缓存约 1 小时；传 `?force=1` 绕过缓存。
字段为 **snake_case**（与 `/api/v1/system/*` 一致）。

| 字段 | 类型 | 说明 |
|---|---|---|
| `current` | string | 当前 daemon 版本 |
| `frp` | string | 内嵌 frp 版本 |
| `deployment_mode` | string | `docker` / `systemd` / `openrc` / `launchd` / `windows-service` / `manual` |
| `self_update_enabled` | bool | 是否允许 Web 端自更新（`FRPSMGR_SELF_UPDATE_ENABLED`） |
| `has_update` | bool | 是否有更新版本 |
| `can_self_update` | bool | 该部署是否支持一键更新（Docker / 手动运行为 false） |
| `reason` | string | 不可更新或被禁用时的说明，正常为空串 |
| `latest` | string? | 最新版本 tag（仅查询成功时返回） |
| `changelog` | string? | release 正文（Markdown，仅成功时返回） |
| `html_url` | string? | release 页面链接（仅成功时返回） |
| `published_at` | string? | 发布时间（仅成功时返回） |
| `check_error` | string? | 查询失败时的错误信息（仅失败时返回） |

```json
{
  "current": "1.2.23", "frp": "0.69.1", "deployment_mode": "systemd",
  "self_update_enabled": true, "has_update": true, "can_self_update": true,
  "reason": "", "latest": "v1.2.32", "changelog": "## 修复\n- ...",
  "html_url": "https://github.com/mia-clark/frps-manager/releases/tag/v1.2.32",
  "published_at": "2026-06-06T00:00:00Z"
}
```

### 1.4 `POST /api/v1/system/update` — 一键更新并重启

启动一个**脱离进程**下载最新版、替换二进制并重启服务，立即返回 `202`。客户端随后轮询
`/api/v1/version` 直到 `daemon` 变化即视为完成。受 `FRPSMGR_SELF_UPDATE_ENABLED` 开关控制，
且仅对服务化部署可用（Docker / 手动运行会被拒绝）。传 `?force=1` 可在已是最新时强制重装。

| 状态码 | 含义 |
|---|---|
| `202` | 更新已开始，服务即将重启；body 含 `{status, from, to, message}` |
| `403` | 管理员已禁用 Web 端自更新 |
| `400` | 当前部署方式不支持一键更新（Docker / 手动） |
| `409` | 已是最新版本（未带 `force=1`） |
| `502` | 无法获取最新版本（网络受限等） |

```json
{ "status": "updating", "from": "1.2.23", "to": "v1.2.32", "message": "更新已开始，服务即将重启，请稍候…" }
```

### 1.5 `/api/docs/*` — 内嵌 API 文档（默认开启，无需鉴权）

| 路径 | 说明 |
|---|---|
| `GET /api/docs` | 301 → `/api/docs/` |
| `GET /api/docs/` | Scalar UI（HTML） |
| `GET /api/docs/openapi.yaml` | 内嵌 OpenAPI 3.1 Spec（YAML） |
| `GET /api/docs/openapi.json` | 同上（Content-Type 不同，主体仍为 YAML 文本） |

`FRPSMGR_DOCS_ENABLED=false` 可整片下线。

---

## 2. 实例配置（Configs）

> 实例 ID = 磁盘上 `<profiles_dir>/<id>.toml` 的文件名去后缀。Manager 在内存保留实例对象，每次 `GET` 时**从磁盘重新解析** TOML（避免 in-memory 漂移），见 [`manager.Manager#Get`](../internal/manager/manager.go)。

### 2.1 `GET /api/v1/configs` — 列出全部实例

无请求体。返回 `200`：

```json
{
  "items": [
    {
      "id": "edge-tokyo",
      "name": "edge-tokyo",
      "path": "/data/profiles/edge-tokyo.toml",
      "state": "started",
      "started_at": "2026-06-05T12:30:11+08:00"
    }
  ]
}
```

`Snapshot` 字段（[`instance.go#Snapshot`](../internal/manager/instance.go)，**snake_case**）：

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | string | 实例 ID |
| `name` | string | 用户备注名（来自 `meta.json`），空时回填为 `id` |
| `path` | string | TOML 绝对路径 |
| `state` | string | `started` / `stopped` / `starting` / `stopping` / `unknown` |
| `last_error` | string | 最近一次错误，`omitempty` |
| `started_at` | RFC3339 | 启动时间，未启动则不出现 |
| `stopped_at` | RFC3339 | 停止时间，从未启动过则不出现 |

排序：先按 `meta.json` 中 `sort` 顺序，未出现的 ID 追加在末尾并按 ID 字典序排列。

### 2.2 `POST /api/v1/configs` — 新建实例

请求体：

```json
{
  "id": "edge-tokyo",
  "config": {
    "bindPort": 7000,
    "vhostHTTPPort": 8080,
    "auth": { "method": "token", "token": "abc" },
    "log": { "level": "info" }
  },
  "frpsmgr": { "name": "Tokyo Edge", "manualStart": false }
}
```

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `id` | string | √ | 实例 ID |
| `config` | object | √ | `ServerConfigV1`，见 §11 |
| `frpsmgr` | object | × | `{name, manualStart}`，缺省时 `name` 回填 ID、`manualStart=false` |

后端会：
1. 校验 ID。
2. `sc.Complete()` 把上游默认值填回 config（如 `bindAddr=0.0.0.0`、`heartbeatTimeout=90`）。
3. `MarshalTOML()` 写盘（JSON 桥确保 key 为 camelCase）。
4. 写 `meta.json`（name / manualStart / sort）。
5. 不自动启动 — 用 §3.1 显式启动。

返回 `201` + [`ConfigEnvelope`](#27-configenvelope-响应信封)。  
冲突 → `409 / config_already_exists`；缺少 `id`/`config` → `400 / bad_request`。

### 2.3 `GET /api/v1/configs/{id}` — 取单个实例

无请求体。返回 `200` + `ConfigEnvelope`（Snapshot snake_case + `config` camelCase + `frpsmgr` camelCase）。

不存在 → `404 / config_not_found`。

### 2.4 `PUT /api/v1/configs/{id}` — 整体替换

请求体：

```json
{
  "config": { "bindPort": 7000, "...": "..." },
  "frpsmgr": { "name": "新名字", "manualStart": true }
}
```

| 字段 | 必填 | 说明 |
|---|---|---|
| `config` | √ | 完整 `ServerConfigV1` |
| `frpsmgr` | × | 缺省时不变 |

后端 `Complete() → MarshalTOML() → 原子写盘 → meta 更新 → 若 `state=started` 则自动 reload（= restart）`。

返回 `200` + `ConfigEnvelope`；`config` 缺失 → `400`；不存在 → `404`。

### 2.5 `PATCH /api/v1/configs/{id}` — RFC 7396 Merge Patch

请求体 ≤ 1 MiB；**不走 `decodeJSON`**（不会因未知 key 直接 400）。  
逻辑：当前 `ServerConfigV1` JSON → 与 patch 做对象合并（`null` 删除该键）→ 重新解析到 `ServerConfigV1` → 写盘。

示例（只改日志级别）：

```json
{ "log": { "level": "debug" } }
```

`frpsmgr` 不在 patch 顶层时保持不变。运行中实例会自动 restart。

### 2.6 `DELETE /api/v1/configs/{id}` — 删除

无请求体。流程：`stop()` → `os.Remove(<path>)` → 清理 meta.json 中对应 ID → 触发 `config.deleted` 事件。返回 `204`。

不存在 → `404`。

### 2.7 `POST /api/v1/configs/{id}/duplicate` — 克隆

```json
{ "new_id": "edge-tokyo-copy" }
```

复制 `config` 与 `frpsmgr`（含 `manualStart`）。返回 `201` + 新 `ConfigEnvelope`。

冲突 → `409`；缺 `new_id` → `400`。

### 2.8 `POST /api/v1/configs/reorder` — 持久化展示顺序

```json
{ "order": ["edge-tokyo", "edge-osaka", "edge-seoul"] }
```

未知 ID 静默丢弃。返回 `204`。

### 2.9 `GET /api/v1/configs/{id}/raw` — 读取原始 TOML

`Content-Type: application/toml`，body 为字节流，直接 `os.ReadFile`。

### 2.10 `PUT /api/v1/configs/{id}/raw` — 写入原始 TOML

请求体 ≤ 4 MiB，`Content-Type: application/toml` 或 `text/plain`。  
后端 `ParseServerTOML()` 解析校验 → 原子写盘 → 运行中自动 restart。返回 `200` + `ConfigEnvelope`。

解析失败 → `400 / bad_request`（`message` 含 `parse: ...`）。

### 2.11 `ConfigEnvelope` 响应信封

```jsonc
{
  // ----- Snapshot 顶层（snake_case）-----
  "id": "edge-tokyo",
  "name": "Tokyo Edge",
  "path": "/data/profiles/edge-tokyo.toml",
  "state": "started",
  "started_at": "2026-06-05T12:30:11+08:00",

  // ----- 完整 ServerConfigV1（camelCase，见 §11）-----
  "config": { "bindPort": 7000, "vhostHTTPPort": 8080, "auth": { "method": "token", "token": "abc" } },

  // ----- 管理器元数据（camelCase）-----
  "frpsmgr": { "name": "Tokyo Edge", "manualStart": false }
}
```

---

## 3. 生命周期

| 路径 | 方法 | 行为 | 成功返回 |
|---|---|---|---|
| `/api/v1/configs/{id}/start` | POST | 启动实例 | `200` + Snapshot |
| `/api/v1/configs/{id}/stop` | POST | 停止实例（已停止幂等成功） | `200` + Snapshot |
| `/api/v1/configs/{id}/reload` | POST | **重启**实例（= stop + start） | `200` + Snapshot |
| `/api/v1/configs/{id}/status` | GET | 取当前 Snapshot | `200` + Snapshot |

错误：

- 不存在 → `404 / config_not_found`。
- 启动已运行实例 → `409 / invalid_state`：`"already running"`。
- 停止已停止实例 → 仍 `200`（无副作用）。
- reload 时 `start()` 失败 → `400 / invalid_state`。

`reload` 对 frps 等价于 `stop()+start()`，因为 frps 服务端参数（bindPort、vhost*、auth、tls 等）必须新建进程才能生效；本守护进程不再假装"在线热加载"。

---

## 4. 校验

### 4.1 `POST /api/v1/validate` — 校验配置但不落盘

请求体 ≤ 4 MiB。

| 请求 Content-Type | 请求体形态 |
|---|---|
| `application/json` | `ServerConfigV1` 对象 |
| 其它（`application/toml`、`text/plain` 等） | 原始 frps TOML 文本 |

后端：解析 → `Complete()` → `validation.ValidateServerConfig()`。**无论合法与否都返回 `200`**，结果在 body：

```json
{ "valid": true }
```

```json
{ "valid": true, "warnings": ["xxx is deprecated"] }
```

```json
{ "valid": false, "errors": ["bindPort: required"] }
```

---

## 5. 运行时监控（`/runtime/*`）

> ⚠️ **重要**：`/runtime/*` 端点是守护进程经 worker loopback **代理 frps 原生 API 后透传响应**。响应 JSON 的字段形态以上游 frps 为准（**camelCase**），不是本项目 Snapshot snake_case 风格。
>
> 后端实现见 [`internal/api/runtime.go`](../internal/api/runtime.go)：父进程通过 `manager.Loopback(id)` 拿到子进程随机绑定的 `127.0.0.1:N` 与随机账密，发起 HTTP Basic 请求，把响应体原样回写。

通用错误：

- 实例不存在 → `404 / config_not_found`。
- 实例未运行 → `409 / invalid_state`：`"instance is not running"`。
- worker loopback 不通 / frps 返回非 200 → `502 / upstream_failure`。

### 5.1 `GET /api/v1/runtime/{id}/overview` — 实例总览

透传 frps 原生 `/api/serverinfo`。响应（**camelCase**）：

```jsonc
{
  "version": "0.69.1",
  "bindPort": 7000,
  "vhostHTTPPort": 8080,
  "vhostHTTPSPort": 8443,
  "kcpBindPort": 7000,
  "quicBindPort": 7001,
  "subdomainHost": "frps.example.com",
  "maxPoolCount": 5,
  "maxPortsPerClient": 0,
  "heartbeatTimeout": 90,
  "totalTrafficIn": 102400000,
  "totalTrafficOut": 51200000,
  "curConns": 12,
  "clientCounts": 3,
  "proxyTypeCount": { "tcp": 5, "http": 2, "udp": 1 }
}
```

字段以上游 [`fatedier/frp@v0.69.1/server/dashboard_api.go`](https://github.com/fatedier/frp/blob/v0.69.1/server/dashboard_api.go) 为准；新版本可能新增字段，前端应防御式解析。

### 5.2 `GET /api/v1/runtime/{id}/proxies` — 全部代理（聚合）

守护进程顺次调 frps `/api/proxy/{tcp,udp,http,https,stcp,sudp,xtcp,tcpmux}` 并把每个 `proxies` 数组拼成一个扁平列表。响应（**camelCase**）：

```jsonc
{
  "proxies": [
    {
      "name": "ssh-cn",
      "type": "tcp",
      "conf": {
        "name": "ssh-cn",
        "type": "tcp",
        "remotePort": 6000,
        "transport": { "useEncryption": true }
      },
      "clientVersion": "0.69.1",
      "lastStartTime": "2026-06-05 12:00:00",
      "lastCloseTime": "2026-06-05 11:55:00",
      "status": "online",
      "todayTrafficIn": 102400,
      "todayTrafficOut": 51200,
      "curConns": 3
    }
  ]
}
```

注意：聚合过程对**单个类型失败**容错（仅第一个类型失败才会向上抛 502），其余继续累加。

### 5.3 `GET /api/v1/runtime/{id}/proxies/{name}` — 单条代理详情

透传 frps 原生 `/api/proxies/{name}`。响应形态以上游为准，建议**防御式解析**。常见字段：

```jsonc
{
  "name": "ssh-cn",
  "type": "tcp",
  "conf": { "remotePort": 6000, "transport": { "useEncryption": true } },
  "clientVersion": "0.69.1",
  "lastStartTime": "2026-06-05 12:00:00",
  "lastCloseTime": "2026-06-05 11:55:00",
  "status": "online",
  "todayTrafficIn": 102400,
  "todayTrafficOut": 51200,
  "curConns": 3,
  "err": ""
}
```

代理不存在时上游返回 404，守护进程会把它包成 `502 / upstream_failure: frps loopback /api/proxies/{name} returned 404`。前端按 502 处理即可。

### 5.4 `GET /api/v1/runtime/{id}/clients` — 当前活跃 frpc 客户端

透传 frps 原生 `/api/clients`。响应（**camelCase**，shape 以上游为准）：

```jsonc
{
  "clients": [
    {
      "id": "abc123",
      "user": "edge-tokyo",
      "version": "0.69.1",
      "hostname": "edge-tokyo",
      "os": "linux",
      "arch": "amd64",
      "lastStartTime": "2026-06-05 12:00:00",
      "runId": "..."
    }
  ]
}
```

---

## 6. 历史流量（`/metrics/*`）

后端：每个 frps worker 每分钟被采样一次（流入/流出字节差、当前连接数），写入 SQLite `traffic_points` 表。详见 [`internal/metrics/store.go`](../internal/metrics/store.go) 与 [`internal/metrics/sampler.go`](../internal/metrics/sampler.go)。

**度量存储未启用或不可用** → `503 / internal_error`：`"metrics store disabled"`。

### 6.1 `GET /api/v1/metrics/{id}/traffic` — 单实例历史流量曲线

Query 参数：

| 名称 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `scope` | string | `server` | `server` = 实例总量；`proxy` = 单条代理 |
| `key` | string | `""` | 当 `scope=proxy` 时填代理名 |
| `from` | int64（Unix 秒） | 0 | 0 = 不下界 |
| `to` | int64（Unix 秒） | （必填） | **缺失 → 400 / bad_request** |
| `step` | int64（秒） | 60 | 桶大小，最小 1 |

返回（**snake_case**）：

```json
{
  "inst_id": "edge-tokyo",
  "scope": "server",
  "key": "",
  "step": 60,
  "points": [
    { "ts": 1717576800, "in": 1048576, "out": 524288, "conns": 12 },
    { "ts": 1717576860, "in": 2097152, "out": 1048576, "conns": 15 }
  ]
}
```

聚合语义：同一桶内 `in/out` 求和（区间增量），`conns` 取最大。

---

## 7. 告警

存储同 §6 的 SQLite，表 `alert_rules` 与 `alert_events`。规则结构见 [`internal/metrics/store_alerts.go`](../internal/metrics/store_alerts.go)。

度量存储未启用 → `503`。

### 7.1 `GET /api/v1/alerts` — 列规则

```json
{
  "items": [
    {
      "id": "rule_a1b2c3",
      "name": "edge-tokyo 连接数超 100",
      "enabled": true,
      "inst_id": "edge-tokyo",
      "metric": "conns",
      "op": ">",
      "threshold": 100,
      "for_seconds": 60,
      "target": "",
      "webhook": "https://example.com/hook"
    }
  ]
}
```

### 7.2 `POST /api/v1/alerts` — 创建规则

请求体 = `AlertRule`（与 §7.1 元素相同）。

- `id` 缺省时服务端生成 `rule_xxxxxx`（12 hex）。
- 必填：`name` / `metric` / `op`。
- `inst_id` 缺省时设为 `"*"`（匹配全部实例）。
- `metric` 枚举：`conns` / `traffic_in_rate` / `traffic_out_rate`。
- `op` 枚举：`>` / `>=` / `<` / `<=`。
- `target`：代理名；空或 `"*"` 表示 server scope。
- `for_seconds`：触发去抖（持续多少秒才 fire）。
- `webhook`：可选，fire/resolve 都会 POST 一份事件 JSON。

返回 `201` + 完整 `AlertRule`。缺字段 → `400 / bad_request`。

### 7.3 `GET /api/v1/alerts/{id}` — 取单条

返回 `200` + `AlertRule`；不存在 → `404 / config_not_found`（这里复用了同一 code，关注 HTTP 状态即可）。

### 7.4 `PUT /api/v1/alerts/{id}` — 替换

请求体 = `AlertRule`（`id` 取自 path）。返回 `200` + `AlertRule`。

### 7.5 `DELETE /api/v1/alerts/{id}`

返回 `204`。

### 7.6 `GET /api/v1/alerts/events` — 列事件

Query：

| 名称 | 类型 | 说明 |
|---|---|---|
| `state` | string | `firing` / `resolved`，缺省 = 全部 |
| `from` | int64（Unix 秒） | 0 = 不下界 |
| `to` | int64（Unix 秒） | 0 = 不上界 |

返回（按 `fired_at` 降序，最多 500 条）：

```json
{
  "items": [
    {
      "id": "evt_x1y2z3",
      "rule_id": "rule_a1b2c3",
      "inst_id": "edge-tokyo",
      "target": "",
      "fired_at": 1717576800,
      "resolved_at": 0,
      "value": 123.0,
      "state": "firing"
    }
  ]
}
```

`resolved_at = 0` 表示仍在 firing。

---

## 8. 日志

### 8.0 模型

每个 frps worker 的 stdout/stderr 全量落到该实例独立的 `<FRPSMGR_DATA_DIR>/logs/<id>.log`。本节接口都基于这个文件（与历史版本"合并日志 + 前缀过滤"截然不同）。

`DELETE` 不删盘上文件，只更新 `meta.json` 中该实例的 `log_view_since` 时间戳（Unix 毫秒），后续 `GET` / `WS` 跳过时间戳早于水位的行。

### 8.1 `GET /api/v1/configs/{id}/logs` — 离线查询尾部

Query：

| 名称 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `lines` | int | 200 | 返回最多多少行 |

返回：

```json
{ "lines": ["2026-06-05 12:30:11.546 [I] start frps success", "..."], "next_offset": 0 }
```

`next_offset` 始终为 `0`（兼容字段，不支持 offset 翻页）。文件不存在 → `200` + 空数组。  
实例不存在 → `404 / config_not_found`。

### 8.2 `GET /api/v1/configs/{id}/logs/files` — 列轮转副本

```json
{
  "items": [
    { "path": "/data/logs/edge-tokyo.log" },
    { "path": "/data/logs/edge-tokyo.log.2026-06-04", "rotated_at": "2026-06-04T00:00:00Z" }
  ]
}
```

### 8.3 `DELETE /api/v1/configs/{id}/logs` — 重置视图水位

不删盘上文件。把当前时刻（`time.Now().UnixMilli()`）写到 `meta.json` 中该实例的 `log_view_since`。返回 `204`。

### 8.4 `GET /api/v1/configs/{id}/logs/tail` — WebSocket 实时流

- 协议：`Upgrade: websocket`
- 鉴权：`?token=<bearer>` 查询参数
- 每帧：`{"line": "..."}`
- 服务端每 30s ping 保活；任一方关闭 → 结束
- `log_view_since` 同样作用于实时流（早于水位的行被丢弃）

---

## 9. 导入 / 导出

### 9.1 `POST /api/v1/import/file` — 单文件上传

`multipart/form-data`：

| 字段 | 必填 | 说明 |
|---|---|---|
| `file` | √ | `.toml` / `.ini` / `.conf`，≤ 4 MiB |
| `id` | × | 不填则用文件名去后缀 |

返回 `200` + `ConfigEnvelope`。

### 9.2 `POST /api/v1/import/url` — 从 URL 拉取

```json
{ "url": "https://...", "id": "optional_id" }
```

`url` 必填；下载 ≤ 4 MiB，15s 超时。失败 → `502 / upstream_failure`。

### 9.3 `POST /api/v1/import/text` — 直接粘贴

```json
{ "id": "edge-tokyo", "text": "bindPort = 7000\n...", "format": "toml" }
```

`id` 与 `text` 必填，`format` 仅作元信息。

### 9.4 `POST /api/v1/import/zip` — 批量 ZIP 备份

`multipart/form-data` 的 `file` 字段，≤ 32 MiB，内含 `*.toml/*.ini/*.conf`。重名覆盖。

```json
{ "imported": ["edge-tokyo", "edge-osaka"] }
```

### 9.5 `GET /api/v1/configs/{id}/export` — 单实例下载

`Content-Type: application/toml`，`Content-Disposition: attachment; filename="{id}.toml"`。

### 9.6 `GET /api/v1/export/all` — 全部 ZIP

`Content-Type: application/zip`，`Content-Disposition: attachment; filename="frps-manager-export-YYYYmmdd-HHMMSS.zip"`，内含 `profiles/*.{toml,ini,conf}`。

---

## 10. 系统监控

### 10.1 `GET /api/v1/system/info` — 汇总快照

返回（best-effort，任一字段失败仅省略不报错）：

| 顶层字段 | 类型 | 说明 |
|---|---|---|
| `uptime_s` | int64 | 守护进程已运行秒 |
| `data_dir` | string | 数据目录 |
| `host` | object | `hostname / os / platform / platform_version / kernel_version / kernel_arch / virtualization / uptime_seconds / boot_time` |
| `cpu` | object | `logical_count / physical_count / model_name / mhz_per_core / usage_percent / per_core[] / load_avg_1/5/15` |
| `memory` | object | `total / available / used / used_percent / free / swap_total / swap_used`（字节） |
| `disk` | array | 元素 `path / fstype / total / used / free / used_percent` |
| `network` | array | 元素 `name / bytes_sent / bytes_recv / packets_sent / packets_recv` |
| `connections` | object | `tcp_total / udp_total / tcp_by_status{ESTABLISHED:...} / owned_tcp_conns / owned_udp_conns` |
| `process` | object | `pid / cpu_percent / rss_bytes / vms_bytes / num_threads / num_goroutines / open_files / start_time` |

### 10.2 子接口

| 路径 | 方法 | 说明 | 备注 |
|---|---|---|---|
| `/api/v1/system/cpu` | GET | 单独取 cpu 块 | query `window=200ms`（≤5s） |
| `/api/v1/system/memory` | GET | 单独取 memory 块 |  |
| `/api/v1/system/disk` | GET | 返回 `{items: [...]}` | query `paths=/a,/b`（CSV） |
| `/api/v1/system/network` | GET | 返回 `{items: [...]}` |  |
| `/api/v1/system/connections` | GET | 单独取 connections 块 |  |
| `/api/v1/system/process` | GET | 单独取 process 块 |  |

任一收集器失败 → `500 / internal_error`。

---

## 11. `ServerConfigV1` 数据模型（业务层 camelCase）

完整字段以 [`github.com/fatedier/frp/pkg/config/v1.ServerConfig`](https://pkg.go.dev/github.com/fatedier/frp@v0.69.1/pkg/config/v1#ServerConfig) 为准。本守护进程**不重新声明字段**（也不偷偷过滤），上游新加字段自动接住。

下面列出**高频字段**，全部 **camelCase**：

```jsonc
{
  "bindAddr": "0.0.0.0",
  "bindPort": 7000,

  // 多协议绑定
  "kcpBindPort": 7000,
  "quicBindPort": 7001,
  "quic": { "keepalivePeriod": 10, "maxIdleTimeout": 30, "maxIncomingStreams": 100000 },

  // vhost 反向代理（注意：HTTP/HTTPS 是大写）
  "vhostHTTPPort": 8080,
  "vhostHTTPSPort": 8443,
  "vhostHTTPTimeout": 60,

  // tcpmux
  "tcpmuxHTTPConnectPort": 1337,
  "tcpmuxPassthrough": false,

  // 通用
  "subDomainHost": "frps.example.com",
  "custom404Page": "/path/to/404.html",
  "proxyBindAddr": "",
  "maxPortsPerClient": 0,
  "maxPoolCount": 5,
  "heartbeatTimeout": 90,
  "userConnTimeout": 10,

  // 端口白名单（与端口段一致）
  "allowPorts": [
    { "start": 2000, "end": 3000 },
    { "single": 6000 }
  ],

  // 鉴权
  "auth": {
    "method": "token",
    "additionalScopes": ["HeartBeats", "NewWorkConns"],
    "token": "abc",
    "oidc": {
      "issuer": "https://login.example.com",
      "audience": "frps",
      "skipExpiryCheck": false,
      "skipIssuerCheck": false
    }
  },

  // 传输层
  "transport": {
    "tcpMux": true,
    "tcpMuxKeepaliveInterval": 30,
    "tcpKeepAlive": 7200,
    "maxPoolCount": 5,
    "heartbeatTimeout": 90,
    "tls": {
      "force": false,
      "certFile": "",
      "keyFile": "",
      "trustedCaFile": ""
    }
  },

  // ⚠️ 用户配置的 webServer 会被守护进程在 worker 启动时强制覆盖为 127.0.0.1 + 随机账密
  "webServer": {
    "addr": "127.0.0.1",
    "port": 7400,
    "user": "admin",
    "password": "admin",
    "tls": { "certFile": "", "keyFile": "", "trustedCaFile": "" },
    "pprofEnable": false
  },

  "log": {
    "to": "console",
    "level": "info",
    "maxDays": 3,
    "disablePrintColor": false
  },

  // SSH 隧道网关
  "sshTunnelGateway": {
    "bindPort": 0,
    "privateKeyFile": "",
    "autoGenPrivateKeyPath": "",
    "authorizedKeysFile": ""
  },

  // HTTP API 插件
  "httpPlugins": [
    {
      "name": "user-manager",
      "addr": "127.0.0.1:9000",
      "path": "/handler",
      "ops": ["Login", "NewProxy"]
    }
  ]
}
```

### 11.1 上游不规则 camelCase 陷阱（写错 key 不报错，但回读拿不到）

- `vhostHTTPPort`（**不是** `vhostHttpPort`）
- `vhostHTTPSPort`
- `vhostHTTPTimeout`
- `tcpmuxHTTPConnectPort`
- `kcpBindPort` / `quicBindPort`（`Bind` 而非 `bind` 之外的形式）
- `tokenEndpointURL`（OIDC，**不是** `tokenEndpointUrl`）
- `transport.tcpMux`（**不是** `tcpmux`）
- `transport.tcpMuxKeepaliveInterval`

Go `encoding/json` 默认大小写不敏感匹配，前端写错 key **也能反序列化成功**，但回读字段名错对不上前端模型，UI 上看到"配置丢失"。

### 11.2 `frpsmgr` 管理器元数据（camelCase）

| 字段 | 类型 | 说明 |
|---|---|---|
| `name` | string | 用户备注名（列表展示用） |
| `manualStart` | bool | `true` = 启动 daemon 时不自动 Start；`false`/缺省 = 启动 daemon 时自动 Start |

不写入 frps TOML，落 [`meta.json`](../internal/manager/manager.go)（与 `sort`、`log_view_since` 同文件）。

---

## 12. WebSocket 全局事件 `/api/v1/events`

升级为 WebSocket。

### 12.1 初始过滤（query 可选）

```
?types=instance.state,proxy.status&config_ids=a,b&since=12345
```

`since` = 上次收到的最大 `seq`，用于断线重连时回放 ring buffer。

### 12.2 客户端帧

```json
{ "action": "filter", "types": ["instance.state"], "config_ids": ["edge-tokyo"] }
```

```json
{ "action": "unfilter" }
```

### 12.3 服务端帧（每帧一个 `Event` 对象，**snake_case**）

| 字段 | 类型 | 说明 |
|---|---|---|
| `seq` | uint64 | 单调自增序号 |
| `type` | string | 见下表 |
| `config_id` | string | 关联实例 ID（部分事件可省） |
| `ts` | RFC3339 | 发生时间 |
| `data` | object | 各 `type` 对应的载荷 |

| `type` | `data` 字段 |
|---|---|
| `instance.state` | `{state, prev_state}` |
| `instance.error` | `{message}` |
| `proxy.status` | `{name, type, status, remote_addr, error}` |
| `proxy.connections` | `{name, type, cur_conns}` |
| `config.changed` | （无 data） |
| `config.deleted` | （无 data） |
| `log.line` | `{line}` |
| `alert` | 见告警事件载荷 |

服务端每 30s 发 ping。

---

## 13. HTTP 状态码总览

| 状态 | 含义 | 何时出现 |
|---|---|---|
| `200` | 成功 | GET / 大多数 PUT / PATCH / POST |
| `201` | 已创建 | `POST /configs`、`/configs/{id}/duplicate`、`POST /alerts`、（部分 import 在已存在场景仍 200） |
| `204` | 无内容 | DELETE / reorder / `DELETE /logs` |
| `400` | 请求体或参数不合法 | 未知 JSON key、缺必填、merge patch 解析失败 |
| `401` | 未鉴权 | Bearer Token 缺失 / 无效 |
| `404` | 未找到 | 实例 / 规则不存在 |
| `409` | 状态机或资源冲突 | 已存在、未运行不能 reload、已运行不能 start |
| `500` | 内部错误 | 序列化 / 持久化 / 系统监控收集器失败 |
| `502` | 上游失败 | worker loopback 不通、远程下载失败 |
| `503` | 服务不可用 | 度量存储未启用（`/metrics/*`、`/alerts/*`） |

---

## 14. 与上一代（frpc 客户端管理器）的差异速查

| 维度 | 旧（frpc manager） | 现（frps manager） |
|---|---|---|
| 业务模型 | `ClientConfigV1`（含 `serverAddr/serverPort/proxies[]/visitors[]/nathole`） | `ServerConfigV1`（含 `bindPort/vhost*/auth/transport`，**不含 proxies/visitors** — 由客户端运行时注册） |
| 进程模型 | 单进程多 frpc.Service | 每实例一个 re-exec 子进程（`frps-worker`） |
| 运行时数据 | `Snapshot.proxies[]` 内嵌 | 不在 Snapshot；改走 `/runtime/*` 透传 frps 原生 mem/clients |
| reload | frpc 支持 in-place 热更 | frps 必须重启进程，本守护进程的 reload = stop + start |
| 命名风格 | Snapshot snake_case，ClientConfig camelCase | Snapshot snake_case，ServerConfig camelCase，runtime 端点 frps 原生 camelCase |
| `/proxies*` 端点 | 存在（增删改查 + toggle） | **已删除** — frps 不管理代理定义 |
| `/nathole/discover` | 存在 | **已删除** — STUN 探测属于 frpc 视角，与 frps 管理器无关 |
| 合并日志 | `frpc.log` 单文件 + 前缀过滤 | 每实例独立 `<id>.log` |
