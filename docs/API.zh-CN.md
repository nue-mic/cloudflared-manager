# cloudflared-manager API 详细参考（cfdmgrd · v1）

> 本文件依据当前 [`internal/api`](../internal/api/)、[`internal/manager`](../internal/manager/)、
> [`pkg/cfdconfig`](../pkg/cfdconfig/)、[`internal/metrics`](../internal/metrics/)、
> [`internal/cfdbin`](../internal/cfdbin/) 的 Go 源码**实地核对**生成，覆盖路径、请求体、
> 响应体、字段大小写与错误码。Go 源码为权威；凡与
> [`internal/api/openapi.yaml`](../internal/api/openapi.yaml) 不一致之处，请同步修复两者。
>
> 这是一个 **无头的 cloudflared 连接器（token 模式）多实例管理器**：守护进程
> `cfdmgrd` 把每个 cloudflared 连接器作为**独立子进程**拉起
> （`cloudflared tunnel --no-autoupdate run`，token 由 `TUNNEL_TOKEN` 环境变量注入），
> 通过 HTTP/JSON + WebSocket 暴露隧道 CRUD、生命周期、历史曲线、告警、日志、
> 导入导出、系统监控与 cloudflared 二进制版本管理等管理面。

---

## 0. 全局约定

| 项目 | 值 |
|---|---|
| 监听地址 | `CFDM_HTTP_ADDR`，默认 `:8080` |
| 数据目录 | `CFDM_DATA_DIR`，默认 `/var/lib/cfdmgrd`（Windows 为 `%ProgramData%\cfdmgrd`）。子目录：`profiles/`（每实例一个 `<id>.yaml`）、`logs/`（`<id>.log`）、`stores/`、`bin/cloudflared/`、`meta.json` |
| 鉴权 | 除 `GET /api/v1/health` 与 `/api/docs/*` 外，所有 `/api/v1/*` 都要求 `Authorization: Bearer <CFDM_API_TOKEN>` |
| Content-Type | 除特别说明（`/raw`、`/import/*`、`/validate`、`/export/*`、WS）外，**请求/返回均为 `application/json; charset=utf-8`** |
| JSON 严格性 | 后端 `decodeJSON` 启用 `DisallowUnknownFields()`，请求体多带一个未声明 key 直接 **`400`** |
| 401 时机 | 缺失或错误 Bearer Token；前端拦截器会清理 token 并跳转 `/login` |
| WebSocket 子路径 | `/api/v1/events`、`/api/v1/configs/{id}/logs/tail` —— 浏览器无法自定义 WS Header，故支持 `?token=...` 查询参数；CORS / Origin 由 `CFDM_CORS_ORIGINS` 控制 |

### 0.1 错误响应统一信封

所有非 2xx 业务错误统一返回（来源 [`apiresp/apiresp.go`](../internal/api/apiresp/apiresp.go)）：

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
| `forbidden` | 403 | 鉴权通过但禁止访问（如自更新被禁用） |
| `not_found` | 404 | 通用未找到（如二进制版本未安装） |
| `conflict` | 409 | 资源冲突 |
| `validation_failed` | 400 | 业务校验失败 |
| `internal_error` | 500 / 503 | 服务端异常 / 子系统未就绪（如度量存储或二进制存储禁用返回 503） |
| `config_not_found` | 404 | 实例 ID 不存在 |
| `config_already_exists` | 409 | 实例 ID 已存在 |
| `invalid_state` | 400 / 409 | 状态机违例（如已运行不能 start、无法在当前部署模式下自更新） |
| `upstream_failure` | 502 | 远程下载 / GitHub release 拉取 / 导入 URL 失败 |

来源：[`apiresp/apiresp.go`](../internal/api/apiresp/apiresp.go)、[`errors.go`](../internal/api/errors.go)、[`helpers.go`](../internal/api/helpers.go)。

### 0.2 ⚠️ 大小写风格速查（本项目第一大坑）

两套 JSON 命名风格并存，**写错 key 时 Go 的 `encoding/json` 大小写不敏感会静默写成功、回读拿不到**，必须看准：

| 子树 / 对象 | 命名风格 | 来源 |
|---|---|---|
| `config`（业务隧道配置）= `TunnelConfigV1` | **camelCase** | [`pkg/cfdconfig/tunnel.go`](../pkg/cfdconfig/tunnel.go) |
| `cfdmgr`（管理器元数据）= `MgrMeta` | **camelCase**（`name` / `manualStart`） | [`manager/manager.go`](../internal/manager/manager.go) |
| `Snapshot`（运行时状态） | **snake_case**（`log_path` / `last_error` / `started_at` / `metrics_port`） | [`manager/instance.go`](../internal/manager/instance.go) |
| 告警 `AlertRule` / `AlertEvent` | **snake_case**（`inst_id` / `for_seconds` / `rule_id` / `fired_at`） | [`metrics/store_alerts.go`](../internal/metrics/store_alerts.go) |
| 二进制 `BinaryItem`（List）/ `AvailableRelease` / `VersionMeta`（Install） | **snake_case**（`source_url` / `is_active` / `tag_name` / `published_at` / `downloaded_at`） | [`cfdbin/store.go`](../internal/cfdbin/store.go)、[`cfdbin/download.go`](../internal/cfdbin/download.go) |
| 系统监控 `/system/*` | **snake_case** | [`internal/sysinfo`](../internal/sysinfo/) |
| WS `Event` 外层信封 | **snake_case**（`config_id`） | [`eventbus/types.go`](../internal/eventbus/types.go) |
| 历史流量曲线 `points[]` | **snake_case** 外层；点内为 `ts/in/out/conns` | [`internal/api/metrics.go`](../internal/api/metrics.go) |

> `TunnelConfigV1` 内部还有沿用 cloudflared 习惯的不规则 camelCase：`edgeIpVersion`（不是 `edgeIPVersion`）、`edgeBindAddress`、`logLevel`、`transportLogLevel`、`postQuantum`、`advancedEnvOverrides`、`binaryVersion`、`gracePeriod`。

### 0.3 ⚠️ token（连接器令牌）写入语义（务必读完）

cloudflared 连接器 token 高度敏感（即整条隧道的凭据）。后端对它的处理：

- **响应永不回传明文**：所有返回 `ConfigEnvelope` 的端点（`GET/POST/PUT/PATCH /configs`、`/duplicate`、`/import/*`、`/raw` 的 PUT 等）都会把 `config.token` 剥离为 `""`，仅通过 `has_token`（bool）告知是否已存。见 [`configs.go: newEnvelope`](../internal/api/configs.go)。
- **`PUT /configs/{id}` 留空 = 保留现有**：因为响应不回传 token，前端编辑表单提交空 token 表示「不动现有令牌」；只有显式传入非空 token 才会覆盖。
- **`POST /configs/{id}/duplicate` 不复制 token**：副本 token 置空，需另行写入。
- **掩码预览**：`GET /configs/{id}/token` 返回 `{has_token, masked, length}`，`masked` 形如 `abcd••••••••wxyz`（首 4 + 圆点 + 末 4；长度 ≤ 8 全圆点），不可逆。
- **唯一能读到明文的端点是 `GET /configs/{id}/raw`**：它返回磁盘 YAML 原文（含 token），属于敏感操作，前端默认不暴露。
- **清空 token**：通过 `PUT /configs/{id}/raw` 写入不含 `token:` 的 YAML 完成（power-user 操作）。

### 0.4 ⚠️ traffic 三列是计数，不是字节

cloudflared 没有 per-tunnel 字节计数器，只暴露 Prometheus 指标。`GET /metrics/{id}/traffic` 的 `points[]` 中：

- `in` = **请求数增量**（区间内新增请求计数，不是流入字节）
- `out` = **错误数增量**（区间内新增错误计数，不是流出字节）
- `conns` = **当前 HA 连接数**（cloudflared 与边缘建立的高可用连接条数，非字节、非累计）

前端展示与告警阈值都要按「计数 / 速率」语义理解，切勿当作带宽。

### 0.5 配置环境变量（[`internal/appcfg/appcfg.go`](../internal/appcfg/appcfg.go)）

前缀统一为 `CFDM_`：

| 变量 | 默认 | 说明 |
|---|---|---|
| `CFDM_API_TOKEN` | （必填） | API 鉴权令牌，缺失则启动失败 |
| `CFDM_HTTP_ADDR` | `:8080` | 监听地址 |
| `CFDM_DATA_DIR` | `/var/lib/cfdmgrd` | 数据根目录（Windows 默认 `%ProgramData%\cfdmgrd`） |
| `CFDM_CORS_ORIGINS` | `*` | CORS / WS Origin 白名单（CSV） |
| `CFDM_LOG_LEVEL` | `info` | trace/debug/info/warn/error |
| `CFDM_DOCS_ENABLED` | `true` | 是否挂载 `/api/docs/*` |
| `CFDM_SELF_UPDATE_ENABLED` | `true` | 是否允许 Web 端一键自更新守护进程 |
| `CFDM_RELEASE_PROXY_BASES` | 7 个 `gh-raw` 代理域名 | cloudflared 二进制下载走的 Release 代理域名（CSV，自动故障转移） |
| `CFDM_RELEASE_PROXY_KEY` | `cloudflared-releases` | Release 代理的配置键 |
| `CFDM_DOWNLOAD_MIRRORS` | `https://gh-proxy.org/,https://gh-proxy.com/` | 旧 GitHub 镜像前缀（CSV，仅自更新用；二进制下载已改走 Release 代理） |
| `CFDM_GITHUB_TOKEN` | （空） | 可选（旧）；Release 代理无需 token |
| `CFDM_BINARIES_DIR` | `{DATA_DIR}/bin/cloudflared` | cloudflared 二进制存储根目录 |
| `CFDM_CLOUDFLARED_DEFAULT_VERSION` | `latest` | `POST /binaries/install` 省略 version 时的回退目标 |

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
{ "daemon": "2.0.0", "build_date": "2026-06-08T00:00:00Z" }
```

| 字段 | 类型 | 说明 |
|---|---|---|
| `daemon` | string | 守护进程版本，构建期 `-ldflags` 注入 |
| `build_date` | string | 构建时间，构建期注入（缺省 `unknown`） |

> cloudflared 二进制版本不在此处，请查 `GET /api/v1/binaries`。

### 1.3 `GET /api/v1/version/check` — 检查 daemon 最新版本

查询 GitHub 最新 release 并与当前版本对比，后端结果缓存约 1 小时；传 `?force=1`（或 `?refresh=1`）绕过缓存。字段为 **snake_case**。

| 字段 | 类型 | 说明 |
|---|---|---|
| `current` | string | 当前 daemon 版本 |
| `deployment_mode` | string | `docker` / `systemd` / `openrc` / `launchd` / `windows-service` / `manual` |
| `self_update_enabled` | bool | 是否允许 Web 端自更新（`CFDM_SELF_UPDATE_ENABLED`） |
| `has_update` | bool | 是否存在更新版本 |
| `can_self_update` | bool | 该部署「能力上」是否支持一键更新（部署模式支持 **且** 管理员启用）；前端再与 `has_update` 组合决定按钮是否可点 |
| `reason` | string | 不可更新或被禁用时的说明，正常为空串 |
| `latest` | string? | 最新版本 tag（仅查询成功时返回） |
| `changelog` | string? | release 正文 Markdown（仅成功时返回） |
| `html_url` | string? | release 页面链接（仅成功时返回） |
| `published_at` | string? | 发布时间（仅成功时返回） |
| `check_error` | string? | 查询失败时的错误信息（仅失败时返回） |

### 1.4 `POST /api/v1/system/update` — 一键自更新守护进程

触发分离式（detached）在线升级并立即返回 `202`，随后守护进程会被替换并重启；客户端应轮询 `/health` + `/version` 直到版本变化。传 `?force=1` 可在已是最新时强制重装。

成功 `202`：

```json
{ "status": "updating", "from": "2.0.0", "to": "2.0.1", "message": "更新已开始，服务即将重启，请稍候…" }
```

错误：

| HTTP | code | 触发 |
|---|---|---|
| `403` | `forbidden` | `CFDM_SELF_UPDATE_ENABLED=false` |
| `400` | `invalid_state` | 当前部署模式不支持自更新（如 docker / 手动运行） |
| `409` | `conflict` | 已是最新版本且未带 `?force=1` |
| `502` | `upstream_failure` | 无法获取最新版本（GitHub 拉取失败） |

---

## 2. 隧道配置 CRUD（`config` 子树 = camelCase）

业务配置对象为 [`TunnelConfigV1`](../pkg/cfdconfig/tunnel.go)，**全 camelCase**，YAML（落盘）与 JSON（API）共用同一组 tag。

### 2.1 `TunnelConfigV1` 字段表

| 字段（路径） | 类型 | 取值 / 约束 | 说明 |
|---|---|---|---|
| `token` | string | 长度 [100,1500]，base64 字符集；空 = 未配置（草稿允许） | 连接器令牌。**响应永不回传，详见 §0.3** |
| `edge.protocol` | string | `auto`(默认) / `http2` / `quic` | cloudflared ↔ 边缘传输协议 |
| `edge.edgeIpVersion` | string | `auto` / `4` / `6`；空 = 上游默认（4） | 拨号边缘的 IP 族（注意是 `edgeIpVersion`） |
| `edge.edgeBindAddress` | string | 不含空白字符 | 出站边缘连接的本地源 IP，IP 族会覆盖 `edgeIpVersion` |
| `edge.region` | string | `""`(全局) / `us` | 限定边缘路由区域 |
| `edge.postQuantum` | bool | 仅当 `protocol == "quic"` 时有效 | 强制后量子密钥交换；与非 quic 组合会被校验拒绝 |
| `reliability.retries` | int | [0,20]，0 = 用 cloudflared 默认(5) | 连接 / 协议重试上限 |
| `reliability.gracePeriod` | string | Go duration，范围 [1s,5m]，如 `"30s"` | 收到停止信号后等待在途请求完成的时长 |
| `logging.logLevel` | string | `debug` / `info` / `warn` / `error` / `fatal`；空 = 默认 | 应用级日志等级 |
| `logging.transportLogLevel` | string | 同上 | QUIC/HTTP2 传输层日志等级 |
| `identity.label` | string | ≤64，字符集 `[A-Za-z0-9_\-. ]` | 连接器显示名（cloudflared 无对应 env，启动时经 argv 透传） |
| `identity.tags` | map[string]string | key 匹配 `[A-Za-z_][A-Za-z0-9_]*` 且 ≤32，value ≤128 | 上报到 Zero Trust 面板的注解集 |
| `advancedEnvOverrides` | map[string]string | key 匹配 `^[A-Z][A-Z0-9_]*$`，且不得为保留键 | cloudflared 未建模 env 的逃生舱；保留键（`TUNNEL_TOKEN`/`NO_AUTOUPDATE`/`AUTOUPDATE_FREQ`/`TUNNEL_METRICS`/`TUNNEL_OUTPUT`/`TUNNEL_LOGFILE`/`TUNNEL_LOGDIRECTORY`）禁止覆盖 |
| `binaryVersion` | string | 空 / `current` = 跟随全局活跃版本；或具体 tag（如 `2026.5.2`） | 为该实例钉住 cloudflared 二进制版本 |

> 公共主机名 / ingress / 源站配置全部在 Cloudflare Zero Trust 面板维护，**不**由本模型管理。

### 2.2 `MgrMeta`（`cfdmgr` 子树 = camelCase）

| 字段 | 类型 | 说明 |
|---|---|---|
| `name` | string | 实例显示名 |
| `manualStart` | bool | true 则守护进程启动时不自动拉起该实例 |

### 2.3 `Snapshot`（运行时状态 = snake_case）

来源 [`manager/instance.go`](../internal/manager/instance.go)。

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | string | 实例 ID（= 配置文件名去扩展名） |
| `name` | string | 显示名（由 meta.json 注入） |
| `path` | string | 磁盘配置文件绝对路径（`.yaml`） |
| `log_path` | string | 日志文件路径（由 LogsDir 注入） |
| `state` | string | `stopped` / `starting` / `started` / `stopping` |
| `last_error` | string? | 最近一次错误（无则省略） |
| `started_at` | string? | 最近一次启动时间（RFC3339，未启动则省略） |
| `stopped_at` | string? | 最近一次停止时间（省略同上） |
| `binary_version` | string? | 该实例当前使用的 cloudflared 版本 |
| `pid` | int? | 子进程 PID，0 / 省略表示未运行 |
| `metrics_port` | int? | 分配给该实例的本地 metrics 端口（CRC32 哈希落于 [20241,20998]） |

### 2.4 `ConfigEnvelope`（多数 config 端点的响应体）

= `Snapshot` 的全部字段（平铺）+ 以下三项：

| 字段 | 类型 | 说明 |
|---|---|---|
| `config` | `TunnelConfigV1` | 业务配置，**`token` 已脱敏为 `""`** |
| `cfdmgr` | `MgrMeta` | 管理器元数据 |
| `has_token` | bool | 是否已存储非空 token |

### 2.5 端点

#### `GET /api/v1/configs` — 列出全部实例

返回 `200`：`{ "items": Snapshot[] }`（按用户排序）。注意 **列表项只是 Snapshot，不含 `config` 业务字段**；回填编辑表单需另取 `GET /configs/{id}`。

#### `POST /api/v1/configs` — 创建

请求体：

```json
{ "id": "my-tunnel", "config": { "token": "<base64...>", "edge": { "protocol": "auto" } }, "cfdmgr": { "name": "我的隧道", "manualStart": false } }
```

- `id` 与 `config` 必填，缺失返回 `400`。
- 成功 `201` 返回 `ConfigEnvelope`（token 已脱敏）。
- ID 已存在返回 `409 config_already_exists`。

#### `POST /api/v1/configs/reorder` — 保存显示顺序

请求体 `{ "order": ["id-a", "id-b", ...] }` → `204`。

#### `GET /api/v1/configs/{id}` — 取单个实例（回填编辑表单用）

返回 `200` `ConfigEnvelope`；`config.token` 永远为 `""`，凭 `has_token` 判断是否已存。不存在返回 `404 config_not_found`。

#### `PUT /api/v1/configs/{id}` — 全量替换

请求体 `{ "config": TunnelConfigV1, "cfdmgr": MgrMeta }`（`config` 必填）。

- **`config.token` 留空 = 保留现有 token**；非空才覆盖（详见 §0.3）。
- 成功 `200` 返回 `ConfigEnvelope`。

#### `PATCH /api/v1/configs/{id}` — 合并修改（RFC 7396）

请求体为对 `TunnelConfigV1` 的 JSON Merge-Patch（`Content-Type: application/json`，注意仍是 camelCase 字段）。`null` 值删除对应键；嵌套对象递归合并。成功 `200` 返回 `ConfigEnvelope`。

> Patch 基于当前磁盘配置（含明文 token）合并，因此 patch 不带 token 时 token 不变。

#### `DELETE /api/v1/configs/{id}` — 删除

停止并删除实例 → `204`。

#### `POST /api/v1/configs/{id}/duplicate` — 复制

请求体 `{ "new_id": "copy-1" }`（必填）。成功 `201` 返回新实例 `ConfigEnvelope`。**副本不复制 token**（置空）。

#### `GET /api/v1/configs/{id}/raw` — 磁盘 YAML 原文（敏感）

返回 `text/yaml`（`Content-Type: application/yaml`），**含明文 token**。前端默认不暴露。

#### `PUT /api/v1/configs/{id}/raw` — 覆盖磁盘 YAML

请求体为 cloudflared 风格 YAML 原文（`Content-Type` 任意，按 YAML 解析）。成功 `200` 返回 `ConfigEnvelope`。可用于清空 token（写入不含 `token:` 的 YAML）。

#### `GET /api/v1/configs/{id}/token` — token 掩码预览

返回 `200`：

```json
{ "has_token": true, "masked": "abcd••••••••wxyz", "length": 720 }
```

绝不返回明文。

---

## 3. 生命周期与状态

| 端点 | 方法 | 行为 | 成功响应 |
|---|---|---|---|
| `/api/v1/configs/{id}/start` | POST | 拉起子进程（缺 token 会失败） | `200` `Snapshot` |
| `/api/v1/configs/{id}/stop` | POST | 停止子进程 | `200` `Snapshot` |
| `/api/v1/configs/{id}/reload` | POST | **= stop + start**（cloudflared 无 in-place 热重载语义） | `200` `Snapshot` |
| `/api/v1/configs/{id}/status` | GET | 查询运行时状态 | `200` `Snapshot` |

错误：实例不存在 `404 config_not_found`；状态机违例（已运行再 start 等）`409 invalid_state`。
启动时若配置无 token，会以 `last_error` 记录并停回 `stopped`。

---

## 4. 历史流量曲线

### `GET /api/v1/metrics/{id}/traffic` — 降采样历史曲线

查询参数：

| 参数 | 默认 | 说明 |
|---|---|---|
| `scope` | `server` | `server` / `proxy`（按 cloudflared 指标维度，通常用 `server`） |
| `key` | `""` | scope=proxy 时的目标键 |
| `from` | 0 | 起始时间（unix 秒，0 = 不限） |
| `to` | （必填） | 结束时间（unix 秒）。**缺失返回 `400`** |
| `step` | 60 | 降采样步长（秒） |

返回 `200`：

```json
{
  "inst_id": "my-tunnel",
  "scope": "server",
  "key": "",
  "step": 60,
  "points": [ { "ts": 1717800000, "in": 12, "out": 0, "conns": 4 } ]
}
```

> **`in`/`out`/`conns` 是计数而非字节**：`in`=请求数增量、`out`=错误数增量、`conns`=当前 HA 连接数。详见 §0.4。

度量存储被禁用时返回 `503 internal_error`。

---

## 5. 告警规则与事件（snake_case）

### 5.1 `AlertRule` 字段表（[`metrics/store_alerts.go`](../internal/metrics/store_alerts.go)）

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | string | 规则 ID，创建时可省略（自动生成 `rule_<hex>`） |
| `name` | string | 规则名（必填） |
| `enabled` | bool | 是否启用 |
| `inst_id` | string | 目标实例 ID，`*` = 全部（省略时后端补 `*`） |
| `metric` | string | `conns` / `requests_rate` / `errors_rate`（旧名 `traffic_in_rate` / `traffic_out_rate` 作为别名仍接受） |
| `op` | string | `>` / `>=` / `<` / `<=` |
| `threshold` | number | 与指标值比较的阈值 |
| `for_seconds` | int | 需持续满足多久才触发（去抖） |
| `target` | string | 维度键，`""` / `*` 表示实例级 |
| `webhook` | string | 触发 / 解除时 POST 的可选 URL |

> 校验：`name`/`metric`/`op` 必填；`metric` 不在允许集返回 `400`（提示 `use conns|requests_rate|errors_rate`）；`op` 不在 `>|>=|<|<=` 返回 `400`。

### 5.2 `AlertEvent` 字段表

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | string | 事件 ID |
| `rule_id` | string | 关联规则 ID |
| `inst_id` | string | 实例 ID |
| `target` | string | 维度键 |
| `fired_at` | int64 | 触发时间（unix 秒） |
| `resolved_at` | int64 | 解除时间（unix 秒），仍在触发时为 `0` |
| `value` | number | 触发时的指标值 |
| `state` | string | `firing` / `resolved` |

### 5.3 端点

| 端点 | 方法 | 说明 | 响应 |
|---|---|---|---|
| `/api/v1/alerts/events` | GET | 列事件，支持 `?state=&from=&to=`（from/to 为 unix 秒，0=不限，最多 500 条按 `fired_at` 倒序） | `200` `{ "items": AlertEvent[] }` |
| `/api/v1/alerts` | GET | 列全部规则 | `200` `{ "items": AlertRule[] }` |
| `/api/v1/alerts` | POST | 创建规则（`id` 可省略） | `201` `AlertRule` / `400` |
| `/api/v1/alerts/{id}` | GET | 取单条规则 | `200` `AlertRule` / `404` |
| `/api/v1/alerts/{id}` | PUT | 替换规则（`id` 取自路径） | `200` `AlertRule` / `400` |
| `/api/v1/alerts/{id}` | DELETE | 删除规则 | `204` |

度量存储被禁用时所有告警端点返回 `503`。

---

## 6. 配置校验

### `POST /api/v1/validate` — 校验配置（不持久化）

- `Content-Type: application/json` → 请求体按 `TunnelConfigV1`（camelCase）解析。
- 其它 `Content-Type` → 请求体按 cloudflared YAML 解析。

无论成功失败均返回 `200`：

```json
{ "valid": true, "warnings": ["advancedEnvOverrides key FOO ... will be ignored"] }
```

| 字段 | 类型 | 说明 |
|---|---|---|
| `valid` | bool | 是否通过 |
| `errors` | string[]? | 校验失败原因（仅失败时） |
| `warnings` | string[]? | 非阻断告警（如 `advancedEnvOverrides` 含不在白名单的键） |

> 跨字段规则：`edge.postQuantum` 为 true 但 `edge.protocol != "quic"` 时返回 `valid:false`。

---

## 7. 日志

### 7.1 `GET /api/v1/configs/{id}/logs` — 拉取日志

查询参数：`lines`（默认 200）、`level`、`keyword`、`since`（保留，当前实现按实例 `log_view_since` 水位过滤）。返回 `200`：

```json
{ "lines": ["2026-06-08 12:00:00.000 ...", "..."], "next_offset": 0 }
```

> `next_offset` 在合并日志模式下恒为 `0`（不支持 offset 翻页，前端只用 `lines`）。实例不存在返回 `404`。

### 7.2 `GET /api/v1/configs/{id}/logs/files` — 列日志轮转副本

返回 `200`：`{ "items": [ { "path": "/.../my-tunnel.log", "rotated_at": "..." } ] }`（`rotated_at` 为可选轮转时间）。

### 7.3 `DELETE /api/v1/configs/{id}/logs` — 清空（仅置水位）

把该实例的 `log_view_since` 设为当前时间 → 后续 `GET /logs` 与 WS tail 跳过更早的行，**不删盘上文件** → `204`。

### 7.4 `WS /api/v1/configs/{id}/logs/tail` — 实时日志流

升级为 WebSocket（浏览器用 `?token=...` 传鉴权）。服务端每帧推送：

```json
{ "line": "2026-06-08 12:00:00.000 [INF] ..." }
```

早于 `log_view_since` 水位的行被丢弃。

### 7.5 `WS /api/v1/configs/{id}/logs/stream` — 结构化实时日志流 ⭐

升级为 WebSocket（浏览器用 `?token=...`）。相比 `logs/tail` 推原始字符串，本端点推**结构化条目**，前端据此做级别着色 / 过滤 / 去重。可选过滤参数：`?level=`（最低级别 debug|info|warn|error|fatal）、`?keyword=`（子串，匹配 message+raw，大小写不敏感）、`?conn_index=`（仅某条 edge 连接）、`?backlog=`（连接时回放历史行数，默认 500）。

每帧统一为 `{ "entries": [LogEntry, ...] }`：连接时先发一帧历史（按 backlog + 过滤 + 清空水位），之后每条新行一帧（长度 1）。**前端按 `seq` 去重**（服务端订阅先于快照，边界可能出现一条重复）。

`LogEntry`（**snake_case**，来源 [`logtail.Entry`](../internal/logtail/process_tailer.go)）：

| 字段 | 类型 | 说明 |
|---|---|---|
| `seq` | uint64 | 单调递增序号，前端去重键 |
| `time` | string | RFC3339 |
| `level` | string | debug \| info \| warn \| error \| fatal \| unknown |
| `message` | string | 解析出的消息（非 JSON 行等于整行） |
| `event` | int? | cloudflared event 码 |
| `conn_index` | int? | 关联 edge 连接号 |
| `tunnel_id` | string? | |
| `raw` | string | 原始行 |
| `fields` | object? | 解析出的完整 JSON 字段（供"展开 JSON"） |
| `source` | string | stderr \| stdout \| stream \| daemon |

### 7.6 `GET /api/v1/configs/{id}/live` — 实例实时状态（按需抓取，不落库）

按需抓取该实例 cloudflared `/metrics` 并解析，**不写时序库**。未运行或抓取失败时以 `200 + {"running": false}` 返回（非错误）。运行中返回 `LiveStatus`（**snake_case**，来源 [`metrics/live.go`](../internal/metrics/live.go)）：

| 字段 | 类型 | 说明 |
|---|---|---|
| `running` | bool | |
| `scraped_at` | int64 | unix 秒 |
| `ha_connections` | int | HA 连接数 |
| `requests_total` / `request_errors` / `response_5xx` | int64 | 累计计数 |
| `goroutines` / `resident_memory_bytes` | int / int64 | cloudflared 进程指标 |
| `version` | string? | build_info 版本 |
| `protocol` | string | quic \| http2 \| unknown |
| `connections[]` | EdgeConnection | 每条 edge 连接：`conn_index` / `location`(colo) / `rtt`(原生单位) / `lost_packets` |
| `error` | string? | 抓取失败原因（running=false 时可有） |

### 7.7 `GET /api/v1/configs/{id}/projection` — 运行参数投影

返回 cfdflags 把当前 YAML 投影出的真实运行参数，让用户看懂"到底用什么参数在跑"。**`TUNNEL_TOKEN` 始终脱敏（首4…末4），绝不回明文。**

```jsonc
{
  "env": { "TUNNEL_TRANSPORT_PROTOCOL": "auto", "TUNNEL_TOKEN": "eyJh…AB12", "NO_AUTOUPDATE": "true", "TUNNEL_OUTPUT": "json" },
  "argv": ["tunnel", "--no-autoupdate", "run"],
  "binary_version": "2026.5.2",
  "binary_path": "/var/lib/cfdmgrd/bin/cloudflared/2026.5.2/cloudflared"
}
```

---

## 8. 事件总线（WebSocket）

### `WS /api/v1/events` — 全局事件流

升级为 WebSocket（浏览器用 `?token=...`）。可选查询参数：`?since=<seq>`（回放历史事件）、`?types=`、`?config_ids=`（CSV 过滤）。连接后也可发送 `{ "action": "filter", "types": [...], "config_ids": [...] }` 动态改过滤、`{ "action": "unfilter" }` 取消。

每帧为一个 `Event`（外层 **snake_case**，来源 [`eventbus/types.go`](../internal/eventbus/types.go)）：

| 字段 | 类型 | 说明 |
|---|---|---|
| `seq` | uint64 | 单调递增序号（配合 `?since=` 断点回放） |
| `type` | string | 事件类型，见下表 |
| `config_id` | string? | 关联实例 ID（部分事件无） |
| `ts` | string | 事件时间（RFC3339） |
| `data` | object? | 类型相关负载 |

| `type` | `data` 形态 | 说明 |
|---|---|---|
| `config.changed` | — | 配置被创建 / 修改 |
| `config.deleted` | — | 配置被删除 |
| `instance.state` | `{ state, prev_state? }` | 实例状态变化 |
| `instance.error` | `{ message }` | 实例错误 |
| `alert` | （告警负载） | 告警触发 / 解除 |

---

## 9. 导入 / 导出

### 9.1 `POST /api/v1/import/file` — 上传 YAML 文件

`multipart/form-data`，字段 `file`（`.yaml` / `.yml`），可选 `id`（缺省取文件名去扩展名）。成功 `200` 返回 `ConfigEnvelope`。

### 9.2 `POST /api/v1/import/url` — 从 URL 导入

请求体 `{ "url": "https://...", "id": "可选" }`。仅接受公网 `http`/`https`（内置 SSRF 防护：拒绝回环 / 私网 / 链路本地等地址，下载失败统一回 `502 upstream_failure` 不回显内部错误）。成功 `200` 返回 `ConfigEnvelope`。

### 9.3 `POST /api/v1/import/text` — 从文本导入

请求体 `{ "id": "...", "text": "<cloudflared YAML>" }`（`id` 与 `text` 必填）。成功 `200` 返回 `ConfigEnvelope`。

### 9.4 `POST /api/v1/import/zip` — 导入备份包

`multipart/form-data`，字段 `file`（由 `/export/all` 产出的 zip）。已存在的同名配置会被覆盖；包内 `meta.json` 用于还原显示名 / 手动启动 / 排序。成功 `200`：

```json
{ "imported": ["tunnel-a", "tunnel-b"] }
```

### 9.5 `GET /api/v1/configs/{id}/export` — 导出单个配置

返回 `text/yaml` 下载（`Content-Disposition: attachment; filename="<id>.yaml"`），即磁盘原文（含 token）。

### 9.6 `GET /api/v1/export/all` — 导出全部为 zip

返回 `application/zip`，内含 `profiles/*.yaml`（每实例原文）+ `meta.json`。

---

## 10. 系统监控（snake_case）

均为只读 `GET`，字段全 snake_case，来源 [`internal/sysinfo`](../internal/sysinfo/)。

| 端点 | 返回 |
|---|---|
| `/api/v1/system/info` | 聚合快照：`{ uptime_s, data_dir, host, cpu, memory, disk, network, connections, process }`（各块 best-effort，单块采集失败则缺省） |
| `/api/v1/system/cpu` | CPU 指标（`?window=200ms` 控制采样窗口，上限 5s） |
| `/api/v1/system/memory` | 虚拟内存 + swap |
| `/api/v1/system/disk` | `{ "items": [...] }`（默认 `/` 与 data dir，`?paths=/a,/b` 追加） |
| `/api/v1/system/network` | `{ "items": [...] }` 每网卡字节 / 包计数 |
| `/api/v1/system/connections` | 全局 socket 计数摘要 + 守护进程子集 |
| `/api/v1/system/process` | 守护进程自身信息 |

---

## 11. cloudflared 二进制版本管理（snake_case）

存储根目录 `CFDM_BINARIES_DIR`（默认 `{DATA_DIR}/bin/cloudflared`），按 `<version>/cloudflared[.exe]` 落盘，活跃版本记于 `active.json`。

### 11.1 `GET /api/v1/binaries` — 已安装版本（`BinaryItem`）

返回 `200`：`{ "items": BinaryItem[] }`（按版本 tag 降序）。存储未配置时返回空列表。

`BinaryItem` 字段（来源 `cfdbin.InstalledVersion`）：

| 字段 | 类型 | 说明 |
|---|---|---|
| `version` | string | 版本 tag |
| `path` | string | 二进制绝对路径 |
| `sha256` | string? | 校验和（来自 meta.json） |
| `source_url` | string? | 下载源 URL |
| `mirror` | string? | 实际命中的镜像前缀（直连为空） |
| `downloaded_at` | string? | 下载时间 |
| `size_bytes` | int? | 字节大小 |
| `verified` | bool | 是否通过 SHA256 校验 |
| `is_active` | bool | 是否为当前活跃版本 |

### 11.2 `GET /api/v1/binaries/available` — 可下载版本（`AvailableRelease`）

从 GitHub 拉取最近 10 个 release。返回 `200`：`{ "items": AvailableRelease[] }`。

| 字段 | 类型 | 说明 |
|---|---|---|
| `tag_name` | string | release tag |
| `published_at` | string | 发布时间 |
| `html_url` | string | release 页面 |
| `asset_url` | string? | 当前平台对应资产的下载链接（匹配不到则空） |
| `sha256` | string? | 从 release 正文解析出的当前平台资产校验和 |

下载器未配置返回 `503`；GitHub 拉取失败返回 `502 upstream_failure`。

### 11.3 `POST /api/v1/binaries/install` — 安装某版本

请求体 `{ "version": "2026.5.2" }`（省略时回退 `CFDM_CLOUDFLARED_DEFAULT_VERSION`，默认 `latest`；`latest` 会被解析为具体 tag）。下载 → 校验 SHA256 → 落盘（macOS 资产解包后再校验内层二进制）。

成功 `201` 返回 `VersionMeta`（**注意：比 `BinaryItem` 多 `platform`/`arch`/`asset_name` 字段**）：

| 字段 | 类型 | 说明 |
|---|---|---|
| `version` | string | 解析后的具体 tag |
| `platform` | string | GOOS |
| `arch` | string | GOARCH |
| `asset_name` | string | 命中的资产文件名 |
| `sha256` | string | 校验和 |
| `source_url` | string | 直连下载 URL |
| `mirror` | string? | 命中的镜像前缀 |
| `downloaded_at` | string | 下载时间（UTC） |
| `size_bytes` | int | 字节大小 |
| `verified` | bool | 恒为 true |

错误：version tag 非法 `400`；下载 / 校验失败 `502 upstream_failure`；客户端取消或超时 `504`；存储未配置 `503`。

### 11.4 `POST /api/v1/binaries/{version}/activate` — 设为活跃版本

将 `{version}` 写入 `active.json`（须已安装）。成功 `200`：

```json
{ "version": "2026.5.2", "active": true }
```

version 未安装返回 `404 not_found`；version 非法 `400`。

### 11.5 `DELETE /api/v1/binaries/{version}` — 删除某版本

删除版本目录 → `204`。当前活跃版本不可删，返回 `409 conflict`；未安装返回 `404 not_found`；version 非法 `400`；存储未配置 `503`。

---

## 12. 端点速查

| # | 方法 | 路径 | 鉴权 |
|---|---|---|---|
| 1 | GET | `/api/v1/health` | 否 |
| 2 | GET | `/api/v1/version` | 是 |
| 3 | GET | `/api/v1/version/check` | 是 |
| 4 | POST | `/api/v1/system/update` | 是 |
| 5 | GET | `/api/v1/configs` | 是 |
| 6 | POST | `/api/v1/configs` | 是 |
| 7 | POST | `/api/v1/configs/reorder` | 是 |
| 8 | GET | `/api/v1/configs/{id}` | 是 |
| 9 | PUT | `/api/v1/configs/{id}` | 是 |
| 10 | PATCH | `/api/v1/configs/{id}` | 是 |
| 11 | DELETE | `/api/v1/configs/{id}` | 是 |
| 12 | POST | `/api/v1/configs/{id}/duplicate` | 是 |
| 13 | GET | `/api/v1/configs/{id}/raw` | 是 |
| 14 | PUT | `/api/v1/configs/{id}/raw` | 是 |
| 15 | GET | `/api/v1/configs/{id}/token` | 是 |
| 16 | POST | `/api/v1/configs/{id}/start` | 是 |
| 17 | POST | `/api/v1/configs/{id}/stop` | 是 |
| 18 | POST | `/api/v1/configs/{id}/reload` | 是 |
| 19 | GET | `/api/v1/configs/{id}/status` | 是 |
| 20 | GET | `/api/v1/metrics/{id}/traffic` | 是 |
| 21 | GET | `/api/v1/alerts/events` | 是 |
| 22 | GET | `/api/v1/alerts` | 是 |
| 23 | POST | `/api/v1/alerts` | 是 |
| 24 | GET | `/api/v1/alerts/{id}` | 是 |
| 25 | PUT | `/api/v1/alerts/{id}` | 是 |
| 26 | DELETE | `/api/v1/alerts/{id}` | 是 |
| 27 | POST | `/api/v1/validate` | 是 |
| 28 | GET | `/api/v1/configs/{id}/logs` | 是 |
| 29 | GET | `/api/v1/configs/{id}/logs/files` | 是 |
| 30 | DELETE | `/api/v1/configs/{id}/logs` | 是 |
| 31 | WS | `/api/v1/configs/{id}/logs/tail` | 是（`?token=`） |
| 32 | WS | `/api/v1/events` | 是（`?token=`） |
| 33 | POST | `/api/v1/import/file` | 是 |
| 34 | POST | `/api/v1/import/url` | 是 |
| 35 | POST | `/api/v1/import/text` | 是 |
| 36 | POST | `/api/v1/import/zip` | 是 |
| 37 | GET | `/api/v1/configs/{id}/export` | 是 |
| 38 | GET | `/api/v1/export/all` | 是 |
| 39 | GET | `/api/v1/system/info` | 是 |
| 40 | GET | `/api/v1/system/cpu` | 是 |
| 41 | GET | `/api/v1/system/memory` | 是 |
| 42 | GET | `/api/v1/system/disk` | 是 |
| 43 | GET | `/api/v1/system/network` | 是 |
| 44 | GET | `/api/v1/system/connections` | 是 |
| 45 | GET | `/api/v1/system/process` | 是 |
| 46 | GET | `/api/v1/binaries` | 是 |
| 47 | GET | `/api/v1/binaries/available` | 是 |
| 48 | POST | `/api/v1/binaries/install` | 是 |
| 49 | POST | `/api/v1/binaries/{version}/activate` | 是 |
| 50 | DELETE | `/api/v1/binaries/{version}` | 是 |

---

## 13. curl 速查

```bash
TOKEN=dev
BASE=http://localhost:8080

# 探活（无需鉴权）
curl -s $BASE/api/v1/health

# 创建隧道（token 提交后响应不会回传）
curl -s -X POST $BASE/api/v1/configs \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"id":"demo","config":{"token":"<base64...>","edge":{"protocol":"auto"}},"cfdmgr":{"name":"演示","manualStart":false}}'

# 编辑时留空 token = 保留现有
curl -s -X PUT $BASE/api/v1/configs/demo \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"config":{"token":"","edge":{"protocol":"quic","postQuantum":true}},"cfdmgr":{"name":"演示"}}'

# 看 token 掩码
curl -s $BASE/api/v1/configs/demo/token -H "Authorization: Bearer $TOKEN"

# 启动 / 状态
curl -s -X POST $BASE/api/v1/configs/demo/start -H "Authorization: Bearer $TOKEN"
curl -s $BASE/api/v1/configs/demo/status -H "Authorization: Bearer $TOKEN"

# 历史曲线（to 必填，in/out/conns 是计数不是字节）
curl -s "$BASE/api/v1/metrics/demo/traffic?from=0&to=$(date +%s)&step=60" -H "Authorization: Bearer $TOKEN"

# 校验 YAML
curl -s -X POST $BASE/api/v1/validate \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/yaml' \
  --data-binary @profile.yaml

# 安装并激活 cloudflared 二进制
curl -s -X POST $BASE/api/v1/binaries/install -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"version":"latest"}'
curl -s -X POST $BASE/api/v1/binaries/2026.5.2/activate -H "Authorization: Bearer $TOKEN"

# WebSocket（浏览器用 ?token= 传鉴权）
# ws://localhost:8080/api/v1/events?token=dev
# ws://localhost:8080/api/v1/configs/demo/logs/tail?token=dev
```
