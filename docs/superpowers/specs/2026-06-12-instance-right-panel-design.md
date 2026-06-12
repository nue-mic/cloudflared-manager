# 设计：实例右侧 Tab 化详情面板 + 结构化实时日志

- 日期：2026-06-12
- 分支：`feat/instance-right-panel`
- 状态：已获用户批准（用户授权全程自主开发→测试→发版）

## 1. 背景与目标

当前 [Configs.tsx](../../../web/src/pages/Configs.tsx) 右栏是一块**静态信息面板**（ID/状态/错误/启动时间/路径 + 启停删按钮），无 Tab、无日志、无图表。日志/流量/告警/系统监控各自是独立页面，与实例上下文割裂。

目标：把右栏升级为**带常驻操作条 + 6 个 Tab 的实例详情面板**，重点是**每个实例下的结构化实时日志面板**（类 frpc-manager / Dozzle 体验），并顺带把已建好但未接线的后端能力（结构化日志引擎、cloudflared `/metrics` 实时解析、cfdflags 投影）暴露出来。

核心判断：后端 [process_tailer.go](../../../internal/logtail/process_tailer.go) 的 8000 行环形缓冲 + 结构化 `Entry` **已挂在子进程上**（[instance.go:297](../../../internal/manager/instance.go#L297) `io.MultiWriter(i.logSink, i.tailer)`），`instance.Tailer()` 也已暴露，只差一个 API 端点消费它。本设计 ~90% 是"把已建能力接线 + 前端组织成 Tab"。

## 2. 交互骨架

- 布局沿用现有「左实例列表 + 右详情」两栏（已批准：右栏内嵌 Tab，不用 Drawer）。
- 右栏顶部：实例名 + **双层状态徽章** + **常驻操作条**（启动/停止/重启/编辑/克隆/删除）。
- 下方 6 个 Tab：总览 / 日志 / 指标 / 连接 / 配置 / 事件。
- 双层状态徽章语义：①子进程是否在跑（`cfdstate`）②是否已连上 Cloudflare 边缘（`/live` 的 `ha_connections > 0`）。配免责说明："此处只反映隧道↔边缘连接；ingress 回源健康在 Cloudflare 侧"。

## 3. Tab 内容与数据来源

| Tab | 内容 | 数据来源 |
|---|---|---|
| 总览 | 关键数字卡（运行时长/PID/cloudflared 版本/metrics 端口/HA 连接/请求/错误）+ 本实例正在触发的告警横幅 + 最近错误 + 边缘健康免责 | `GET /status` + 新 `GET /configs/{id}/live` + `GET /alerts/events?state=firing`（前端按 `inst_id` 过滤） |
| 日志 ⭐ | 结构化实时日志（见 §5）：级别着色 / 级别+关键字过滤 / 跟随底部+上滚暂停+新数据浮标 / 暂停 / 清屏 / 下载 / 换行 / 时间戳开关 | 新 WS `GET /configs/{id}/logs/stream` |
| 指标 | 复用 Traffic 的 Recharts：请求/s、错误/s、错误率%、HA 连接数 + 时间范围切换 | 现成 `GET /metrics/{id}/traffic` |
| 连接 | 每条 edge 连接一张卡：conn_index / colo / 协议 / smoothed RTT / 丢包 | 新 `GET /configs/{id}/live` 的 `connections[]` |
| 配置 | 只读结构化 `TunnelConfigV1`（camelCase，token 脱敏）+ 投影出的真实 `TUNNEL_*` env/argv + 一键复制 | 现成 `GET /configs/{id}` + 新 `GET /configs/{id}/projection` |
| 事件 | 本实例生命周期时间线（启停/错误/告警/配置变更），彩色 tag | 现成 WS `/events`（前端按 `config_id` 过滤，会话级缓冲，零后端） |

## 4. 后端契约（权威，前端按此对接）

> 大小写约定：所有新响应走 **snake_case**（与 Snapshot/metrics/alert 一致）；`config` 子树仍是 camelCase（来自 `GET /configs/{id}`，不在本设计新增）。

### 4.1 WS `GET /api/v1/configs/{id}/logs/stream`（新）

- 鉴权：`?token=<api token>`（沿用现有 WS 模式）。
- query 过滤（全部可选）：
  - `level`：最低级别 `debug|info|warn|error`（映射 `logtail.Filter.MinLevel`）。
  - `keyword`：子串（大小写不敏感，匹配 message+raw）。
  - `conn_index`：精确匹配某连接号。
  - `backlog`：连接时回放的历史行数，默认 `500`，上限即 ring 容量。
- 帧格式（**统一**）：每帧 `{"entries":[Entry, ...]}`。
  - 连接建立先发一帧历史（`Tailer.Snapshot(filter, backlog)`，按 `LogViewSince` 水位过滤）。
  - 之后每条新行发一帧 `entries` 长度为 1（`Tailer.Subscribe(filter)`）。
  - 前端只需 `concat(msg.entries)`。
- `Entry`（snake_case，后端已定型于 `logtail.Entry`）：
  ```
  seq:uint64, time:RFC3339, level:string(info|warn|error|fatal|debug|unknown),
  message:string, event?:int, conn_index?:int, tunnel_id?:string,
  raw:string, fields?:object, source:string(stderr|stdout|stream|daemon)
  ```
- 30s ping 保活；客户端关闭即结束。
- 旧 `GET /configs/{id}/logs/tail`（原始文件行 `{"line":...}`）**保留不动**（向后兼容 api-smoke/文档）；UI 不再使用它。

### 4.2 `GET /api/v1/configs/{id}/live`（新）

按需抓取该实例 cloudflared `/metrics` 并用 `metrics.ParsePromText` 解析，**不落库**。

- 实例未运行或抓取失败：`200 {"running": false}`（可带 `error` 字段说明）。
- 运行中：
  ```jsonc
  {
    "running": true,
    "scraped_at": 1733980800,        // unix 秒
    "ha_connections": 4,
    "requests_total": 12345,
    "request_errors": 3,
    "response_5xx": 1,
    "goroutines": 37,
    "resident_memory_bytes": 24117248,
    "version": "2026.5.2",           // build_info 的 version，缺则 ""
    "protocol": "quic",              // 出现 quic_client_* → "quic"，否则 "http2"/"unknown"
    "connections": [
      { "conn_index": 0, "location": "SJC", "rtt_ms": 21.4, "lost_packets": 0 }
    ]
  }
  ```
- 所有字段尽力而为：cloudflared 不暴露的字段省略/置零。实际指标名以联调真二进制为准（`cloudflared_tunnel_ha_connections` / `cloudflared_tunnel_total_requests` / `cloudflared_tunnel_request_errors` / `cloudflared_tunnel_response_by_code{status_code=5xx}` / `quic_client_smoothed_rtt{conn_index}` / `quic_client_lost_packets{conn_index}` / `cloudflared_tunnel_server_locations{...edge_location...}` / `build_info{version}` / `go_goroutines` / `process_resident_memory_bytes`）。

### 4.3 `GET /api/v1/configs/{id}/projection`（新）

返回 cfdflags 把当前 YAML 配置投影出的真实运行参数。

```jsonc
{
  "env": { "TUNNEL_PROTOCOL": "auto", "TUNNEL_METRICS": "127.0.0.1:20xxx",
           "TUNNEL_TOKEN": "eyJh……AB12" },   // TUNNEL_TOKEN 脱敏：首4…末4；从不回明文
  "argv": ["tunnel", "--no-autoupdate", "run"],
  "binary_version": "2026.5.2",               // 解析后的实际版本（空 binaryVersion → store active）
  "binary_path": "/var/lib/cfdmgrd/bin/cloudflared/2026.5.2/cloudflared"
}
```

实现复用 [instance.startLocked](../../../internal/manager/instance.go) 里已有的 `cfdflags.Options→ToTunnelEnv` + `LabelArgv` 投影逻辑（抽成可复用函数，避免与 start 路径漂移）。

### 4.4 填充 `Snapshot.binary_version`（修缺陷）

现状恒空。在 `Manager.Get/List` 组装 Snapshot 时解析：`config.binaryVersion` 非空取之；为空则取 `binStore` 当前 active 版本（无 store 时留空）。

## 5. 结构化实时日志前端组件 `InstanceLogPanel`

- 连接 §4.1 的 WS；缓冲封顶 **5000 条**（FIFO）。
- 交互（less +F 模型）：
  - 默认钉底跟随；用户上滚 → 自动暂停跟随 + 右下"N 条新日志 / 回到底部"浮标。
  - 按 `level` 字段着色（复用现有 `.log-warn/.log-error/...` 终端样式，按 level 字段而非正则猜测）。
  - 关键字过滤（隐藏非匹配）+ 命中高亮；级别下拉（客户端过滤，瞬时）。
  - 工具条：暂停/继续、清屏（清前端缓冲，可选调 `DELETE /logs` 置水位）、下载 .txt、自动换行开关、时间戳显隐开关、WS 连接状态徽章。
- 虚拟滚动：**暂不引入 react-window**（KISS）；5000 条直接渲染（现有代码已渲 1000，现代浏览器可承受），留注释说明未来可换虚拟列表。
- **复用**：[Logs.tsx](../../../web/src/pages/Logs.tsx) 独立页改为渲染 `<InstanceLogPanel id={selectedId} />` + 保留其实例下拉，消灭两套日志 UI。

## 6. 后端改动清单

| # | 改动 | 文件 |
|---|---|---|
| B1 | `Manager.Tailer(id) (*logtail.ProcessTailer, bool)` 访问器 | `internal/manager/manager.go` |
| B2 | 抽 `metrics.Scrape(addr) ([]Sample, error)`（sampler 复用）+ `LiveStatus` 结构与折叠函数 | `internal/metrics/{sampler.go,live.go}` |
| B3 | WS `/logs/stream` handler（snapshot+live+filter+ping） | `internal/api/logs.go` |
| B4 | `GET /configs/{id}/live`（调 MetricsAddr + Scrape + 折叠） | `internal/api/live.go`（新） |
| B5 | `GET /configs/{id}/projection`（抽 `manager.ProjectRun(id)` 复用 start 投影 + token 脱敏） | `internal/api/projection.go`（新）+ `internal/manager` |
| B6 | 填充 `Snapshot.binary_version` | `internal/manager/{manager.go,instance.go}` |
| B7 | 注册路由 + 同步 `openapi.yaml` + `docs/API.zh-CN.md` | `internal/api/server.go` 等 |

## 7. 前端改动清单

| # | 改动 | 文件 |
|---|---|---|
| F1 | 扩展 `LogEntry`（补 seq/conn_index/event/tunnel_id/fields）；新增 `LiveStatus/EdgeConnection/Projection` | `web/src/api/types.ts` |
| F2 | `instanceApi.live/projection`、`logsApi.streamUrl(id, filter)` | `web/src/api/client.ts` |
| F3 | `EventType` 补 `'alert'`（修现有遗漏） | `web/src/events/types.ts` |
| F4 | 新组件目录 `web/src/components/instance/`：`InstanceDetailPanel / InstanceOverview / InstanceLogPanel / InstanceMetrics / InstanceConnections / InstanceConfigView / InstanceEvents` | 新 |
| F5 | Configs.tsx 右栏替换为 `<InstanceDetailPanel/>`（操作条/编辑/克隆/删除回调上提） | `web/src/pages/Configs.tsx` |
| F6 | Logs.tsx 改用 `<InstanceLogPanel/>` | `web/src/pages/Logs.tsx` |

## 8. 测试与验收

- 后端单测：
  - `logtail` 已有；新增 `/logs/stream` handler 帧格式（snapshot+live）测试。
  - `metrics.FoldLive` 表驱动：喂样例 Prometheus 文本 → 断言 LiveStatus 字段（含 per-conn、protocol 判定）。
  - `projection` token 脱敏断言（绝不回明文）。
- 关口（与 CI 一致）：`web` build → `go vet ./...` → `go test -race ./...` → `tsc -b` → `npm run lint` → `npm run gen:api`（openapi 改后）。
- 发版：合并 main 触发 Release（自动 patch bump v0.0.7→v0.0.8 + goreleaser + GHCR）。监控直至 green，失败即修。

## 9. 取舍与边界（YAGNI）

- `/live` 按需抓取、不改 SQLite schema：colo/协议/版本是 `/metrics` 现成字段，零存储成本。
- 不做 react-window（封顶 5000 行直渲）。
- 事件 Tab 仅会话级时间线（订阅起点之后），不补历史回放端点。
- 不新增 per-子进程 CPU/内存采样（sysinfo 是守护进程级；cloudflared 自身内存走 `/live` 的 resident_memory）。
- 旧 `/logs/tail` 保留，不迁移。
