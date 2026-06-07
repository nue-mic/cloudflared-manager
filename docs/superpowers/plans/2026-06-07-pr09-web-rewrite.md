# PR-09 web/ 前端重写 — frps UI → cloudflared-manager UI

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development.

**Goal:** 把 frps-manager 前端整体改造为 cloudflared-manager 前端。删除 frps 强相关页面与配置 schema；types.ts 用 PR-08 的新 envelope（`config: TunnelConfigV1` + `cfdmgr: MgrMeta`）；Configs 改为 cfdflags 五大分组表单 + token 字段 + YAML 双向编辑；新增 Binaries 页面；其它页面（Logs/Traffic/Alerts/Dashboard/ImportExport/ToolsValidate/About）做适配性改造让 `tsc -b && vite build` 通过且不调用已 404 的旧端点。

**Acceptance:**
- `cd web && npm run build` 成功，`web/dist/` 产物嵌入 `cfdmgrd` 二进制
- 浏览器访问 daemon 端口能看到 Login → Dashboard → Configs/Binaries/Logs/... 全部页面渲染
- Network panel 无 `/api/v1/runtime/*` 调用（路由已删）
- Configs 页面能新建实例（含 token + edge + reliability + logging + identity + 高级覆盖 + binaryVersion）

**Out-of-scope（接受当前体验残缺）：**
- 历史流量 Traffic 曲线只渲染基础（PR-09b 详细化）
- Alerts 12 条模板"一键启用"按钮可有可无
- AES 加密导出 UI（PR-10 / 后续）
- 完整 i18n / 主题切换不动

**Tech Stack:** React 19 + TypeScript + Antd 6 + Vite 8 + axios + CodeMirror 6（YAML mode）。无新增依赖。

---

## 文件清单（约 15 文件改动）

| 路径 | 动作 | 关键点 |
|---|---|---|
| `web/src/api/types.ts` | **REWRITE** | 移除 ServerConfig / RuntimeOverview / RuntimeProxy 等 frps 类型；新增 TunnelConfigV1 / EdgeConfig / ReliabilityConfig / LoggingConfig / IdentityConfig；envelope `frpsmgr` → `cfdmgr`；新增 BinaryItem / AvailableRelease / BinaryMeta；新增 LogEntry 结构（match ProcessTailer Entry）；保留 Snapshot / TrafficPoint / TrafficSeries / AlertRule / AlertEvent 通用类型 |
| `web/src/api/client.ts` | Modify | 加 `binariesApi`（list/available/install/activate/delete）+ `validateYAML`；删 `runtimeApi`；如果 `frpsmgr` 在请求 body 中出现，统一改 `cfdmgr` |
| `web/src/api/schema.d.ts` | **REGENERATE** | 跑 `npm run gen:api` 重新生成（基于现有 openapi.yaml — 但 openapi.yaml 没在 PR-08 重写，所以可能漂移；如 gen 失败就 git rm + 改 client 不依赖 schema.d.ts） |
| `web/src/api/update.ts` | Modify | 删 `frp` 字段；保留 daemon 自更新逻辑 |
| `web/src/App.tsx` | Modify | 删 Runtime/TomlReference 路由；加 `/binaries` 路由；保留 lazy load |
| `web/src/components/MainLayout.tsx`（如有） | Modify | 侧栏菜单删 Runtime/TomlReference；加 Binaries |
| `web/src/pages/Login.tsx` | REUSE | 0 修改 |
| `web/src/pages/Dashboard.tsx` | Modify | 移除 frp 版本卡片；保留资源摘要 + 实例摘要 + （可选）binaries 摘要；调用 `/api/v1/version` 不再期待 `frp` 字段 |
| `web/src/pages/Configs.tsx` | **REWRITE** | 5 大分组表单（identity / edge / reliability / logging / advanced）+ token 字段（password input 含显示开关）+ YAML CodeMirror 双向（与表单同步）+ 启停按钮 + 删除/复制按钮（复制走 `/duplicate`，PR-08 后端会自动清 token）。表单 schema 可以从 `pkg/cfdflags.registry`（spec §2.4）的 11 个 flag 元数据手工抄 — 不要试图运行时从后端拉，因为没有"列 flags" endpoint |
| `web/src/pages/ServerConfigGroups.tsx` | **DELETE** | frps 强相关 |
| `web/src/pages/Runtime.tsx` | **DELETE** | endpoint 已删 |
| `web/src/pages/TomlReference.tsx` | **DELETE** | cfd 不用 TOML |
| `web/src/pages/serverConfigForm.ts` | **DELETE** | frps form schema |
| `web/src/pages/tomlSnippets.ts` | **DELETE** | TOML 片段 |
| `web/src/pages/Logs.tsx` | Modify | 调 `/configs/{id}/logs` 返回新结构（结构化 Entry：level/event/conn_index/raw/source 等）；列展示 level color + 时间 + message；过滤条加 MinLevel / Keyword |
| `web/src/pages/Traffic.tsx` | Modify | metric 字段语义变更（in/out 现在是 cloudflared 请求/错误 delta，conns 是 HA 连接数）。**最小适配**：图表保留壳子，标签改 "请求/错误/HA 连接"；如太复杂直接 stub 为 "暂未接入"占位（spec §5.2 PR-09b 完整化） |
| `web/src/pages/Alerts.tsx` | Modify | 删 `proxy.status` / `proxy.connections` 引用；保留规则 CRUD；可选加"加载 12 条默认模板"按钮（POST 12 次 `/alerts` 用 spec §5.4 模板） |
| `web/src/pages/System.tsx` | REUSE | 0 修改（已通用） |
| `web/src/pages/ImportExport.tsx` | Modify | 改文件扩展名提示 `.yaml/.yml`；导出文件名变化（`cloudflared-manager-export-*.zip`）；删 TOML 帮助文字 |
| `web/src/pages/Settings.tsx` | Modify | 删 frp 字段；保留 token / CORS / log level / 镜像源 等基础设置 |
| `web/src/pages/About.tsx` | Modify | 删 frp 版本 + frp 上游链接；保留 cfdmgrd 版本 + cloudflared 版本（PR-05 binaries 提供） |
| `web/src/pages/ToolsValidate.tsx` | Modify | POST `/validate` body 改 YAML（用 `application/yaml` Content-Type）OR JSON；UI 文案改 cfd schema |
| **NEW** `web/src/pages/Binaries.tsx` | **CREATE** | 基础列表（已装版本表）+ "检查更新"按钮（拉 `/binaries/available`）+ "安装新版" + "设为当前" + "删除" 按钮 |

---

## Task 1：基线
```bash
cd /d/Github_Codes_mia-clark/cloudflared-manager
git status
cd web
npm run lint 2>&1 | tail -10 || true
npm run build 2>&1 | tail -10  # 看基线能否 build
cd ..
```

如基线已经 build 失败（types.ts 引用旧路径）→ 这是 PR-08 后端改动引起的级联，PR-09 要修。

---

## Task 2 (Batch I)：基础设施 — 类型 + client + 路由 + 删除

### Step 2.1 整体重写 `web/src/api/types.ts`

新结构骨架（不内联所有，让 implementer 按需补全 — 关键字段：`token`/`edge`/`reliability`/`logging`/`identity`/`advancedEnvOverrides`/`binaryVersion`，envelope 包 `cfdmgr` 不是 `frpsmgr`，Snapshot 加 `pid`/`binary_version`/`metrics_port`）：

```ts
export interface Snapshot {
  id: string;
  name: string;
  path: string;
  log_path: string;
  state: 'stopped' | 'starting' | 'started' | 'stopping';
  last_error?: string;
  started_at?: string;
  stopped_at?: string;
  binary_version?: string;
  pid?: number;
  metrics_port?: number;
}

export interface MgrMeta {
  name: string;
  manualStart: boolean;
}

export interface EdgeConfig {
  protocol?: 'auto' | 'http2' | 'quic';
  edgeIpVersion?: 'auto' | '4' | '6';
  edgeBindAddress?: string;
  region?: '' | 'us';
  postQuantum?: boolean;
}
export interface ReliabilityConfig {
  retries?: number;
  gracePeriod?: string;
}
export interface LoggingConfig {
  logLevel?: 'debug' | 'info' | 'warn' | 'error' | 'fatal';
  transportLogLevel?: 'debug' | 'info' | 'warn' | 'error' | 'fatal';
}
export interface IdentityConfig {
  label?: string;
  tags?: Record<string, string>;
}
export interface TunnelConfigV1 {
  token?: string;
  edge?: EdgeConfig;
  reliability?: ReliabilityConfig;
  logging?: LoggingConfig;
  identity?: IdentityConfig;
  advancedEnvOverrides?: Record<string, string>;
  binaryVersion?: string;
}

export interface ConfigEnvelope extends Snapshot {
  config: TunnelConfigV1;
  cfdmgr: MgrMeta;
}
export interface ConfigList {
  items: Snapshot[];
}
export interface ValidateResp {
  valid: boolean;
  errors?: string[];
  warnings?: string[];
}

// Logs (ProcessTailer.Entry, snake_case)
export interface LogEntry {
  seq: number;
  time: string;
  level: 'debug' | 'info' | 'warn' | 'error' | 'fatal' | 'unknown';
  message: string;
  event?: number;
  conn_index?: number;
  tunnel_id?: string;
  raw: string;
  fields?: Record<string, unknown>;
  source: 'stdout' | 'stderr' | 'stream' | 'daemon';
}

// Binaries
export interface BinaryItem {
  version: string;
  path: string;
  sha256?: string;
  source_url?: string;
  mirror?: string;
  downloaded_at?: string;
  size_bytes?: number;
  verified: boolean;
  is_active: boolean;
}
export interface BinaryList {
  items: BinaryItem[];
}
export interface AvailableRelease {
  tag_name: string;
  published_at: string;
  html_url: string;
  asset_url?: string;
  sha256?: string;
}
export interface AvailableReleaseList {
  items: AvailableRelease[];
}
export interface BinaryMeta {
  version: string;
  platform: string;
  arch: string;
  asset_name: string;
  sha256: string;
  source_url: string;
  mirror?: string;
  downloaded_at: string;
  size_bytes: number;
  verified: boolean;
}

// Traffic / Alerts 保留原 snake_case 类型（PR-08 后端字段一致），但删除 RuntimeOverview / RuntimeProxy / RuntimeClient 与 frps ServerConfig / ServerTransport / AuthOIDC / SSHTunnelGateway / WebServerInfo / LogConfigFields / TLSConfigFields / PortsRange / QUICOptions 等。
```

### Step 2.2 改 `web/src/api/client.ts`

新增 `binariesApi`：
```ts
list: () => http.get<{items: BinaryItem[]}>('/api/v1/binaries').then(r => r.data),
available: () => http.get<{items: AvailableRelease[]}>('/api/v1/binaries/available').then(r => r.data),
install: (version: string) => http.post<BinaryMeta>('/api/v1/binaries/install', { version }).then(r => r.data),
activate: (version: string) => http.post('/api/v1/binaries/' + encodeURIComponent(version) + '/activate').then(r => r.data),
delete: (version: string) => http.delete('/api/v1/binaries/' + encodeURIComponent(version)).then(r => r.data),
```

如果 client.ts 当前导出 `runtimeApi` → 整段删除。任何请求 body 含 `frpsmgr` → 改 `cfdmgr`。

### Step 2.3 改 `web/src/App.tsx`

```ts
// 删
const Runtime = lazy(() => import('./pages/Runtime'));
const TomlReference = lazy(() => import('./pages/TomlReference'));
<Route path="runtime" element={<Runtime />} />
<Route path="reference" element={<TomlReference />} />

// 加
const Binaries = lazy(() => import('./pages/Binaries'));
<Route path="binaries" element={<Binaries />} />
```

`tools` 路由 reference 子路由删；如果只剩 validate，简化为 `<Route path="tools" element={<Navigate to="/tools/validate" replace />} />` 或保留 sub-routes 只含 validate。

### Step 2.4 改 MainLayout / Sidebar（如有）

侧栏菜单项删 Runtime / Reference；加 Binaries（图标用 Antd 的 `CodeOutlined` 或 `DownloadOutlined`）。

### Step 2.5 git rm
```bash
git rm web/src/pages/Runtime.tsx web/src/pages/ServerConfigGroups.tsx web/src/pages/TomlReference.tsx web/src/pages/serverConfigForm.ts web/src/pages/tomlSnippets.ts
```

### Step 2.6 编译验证
```bash
cd web
npm run build 2>&1 | tail -30
```
预期：大量错误 — `Configs.tsx` 等仍引用旧类型。是预期，Batch II 修。Batch I 结束时只要"删除/类型/路由"层面的改动落地。

---

## Task 3 (Batch II)：业务页面适配

每个 .tsx 文件按"删 frps 强相关 + 改字段路径 + 让 tsc 通过"思路：

### 3.1 Configs.tsx（最大重写）
- 删除 frps 9 分组表单逻辑
- 新建 5 分组：identity / edge / reliability / logging / advanced
- token 字段单独卡片，含"显示/隐藏"开关
- protocol = http2 时 disable + 强制 false postQuantum
- YAML 编辑器（CodeMirror）双向同步表单（onChange 同步）
- 启停 / 重载 / 删除 / 复制按钮（复制走 `/duplicate` body `{new_id}`）
- 提交时调 `configsApi.create({ id, config: TunnelConfigV1, cfdmgr: { name, manualStart } })` 或 `update`

提示：spec §2.4 + §9.2 给了表单分组示意；可以从 `pkg/cfdflags` registry 抄 11 个字段的元数据；如果工作量超出 token 预算 → **退化为：只渲染 token 输入 + YAML CodeMirror 双向 + 启停按钮**（最小可用版本）

### 3.2 Logs.tsx
- 调 `/api/v1/configs/{id}/logs` 返回 `LogEntry[]`
- 列：time / level（color tag）/ source / message
- 过滤：MinLevel select / Keyword input
- 删除旧 frps 日志解析正则

### 3.3 Traffic.tsx
- metric 字段：sampler 现在写的是 ha_connections（conns）+ requests delta（in）+ errors delta（out）
- 图表标签：HA 连接数 / 请求量 / 错误量
- 如太复杂 → stub "PR-09b 详细化"占位 + 表格列

### 3.4 Alerts.tsx
- 删 proxy.status / proxy.connections 引用
- AlertMetric 枚举改 `'ha_connections' | 'requests_delta' | 'errors_delta'`（虽然后端仍接受 `conns/traffic_in_rate/traffic_out_rate`，这里 UI 标签用新名）
- 可选：加"启用默认 12 条模板"按钮

### 3.5 Dashboard.tsx
- 删 frp 版本卡片
- 保留：实例数（按 state 分组）+ 资源摘要
- 可选：加 Binaries 卡片

### 3.6 ImportExport.tsx
- 文件类型提示 `.yaml/.yml`
- 导出文件名展示 `cloudflared-manager-export-*.zip`

### 3.7 Settings.tsx
- 删 frp 相关字段
- 保留 API token / CORS / 日志级别 / 镜像源（CFDM_DOWNLOAD_MIRRORS 只读展示）

### 3.8 About.tsx
- 删 frp 版本 + frp 上游链接
- 加 cloudflared 版本（GET /binaries 取 active 项的 version）

### 3.9 ToolsValidate.tsx
- 输入：YAML textarea
- 提交：POST `/validate` 用 `application/yaml` Content-Type
- 展示 errors / warnings 列表

### 3.10 NEW: Binaries.tsx
- 已装版本表（columns: version / size / sha256 (short) / downloaded_at / verified / is_active / actions）
- "检查更新" 按钮 → 拉 `/binaries/available` → 弹出模态选择版本 → "安装" 调 `install`
- 每行 actions：设为当前 / 删除（active 行禁用删除）

---

## Task 4：验证
```bash
cd web
npm run lint 2>&1 | tail -20
npm run build 2>&1 | tail -30
cd ..
```
预期：tsc 0 错误；vite build 成功；web/dist 产物 OK。

```bash
go build -o bin/cfdmgrd ./cmd/cfdmgrd
./bin/cfdmgrd version
rm -rf ./tmp/data; mkdir -p ./tmp/data
CFDM_API_TOKEN=dev CFDM_DATA_DIR=./tmp/data ./bin/cfdmgrd serve > /tmp/cfdmgrd-pr09.log 2>&1 &
PID=$!
sleep 2
# 浏览器人工访问 http://127.0.0.1:8080/ 检查 — 自动化只能看返回 200
curl -fsS http://127.0.0.1:8080/ | head -c 200
echo
kill $PID 2>/dev/null; sleep 1
rm -rf ./tmp/data /tmp/cfdmgrd-pr09.log
```

---

## Task 5：commit（controller）

---

## Self-Review

✅ spec §9 页面动作矩阵覆盖
⚠️ Configs.tsx 完整 5 分组实现是 implementer 决策点；token 允许的话做完整版，否则退化为 "token + YAML + 启停" 最小版
⏸ Traffic / Alerts 完整功能 → PR-09b 或后续

---

## Execution Handoff

3 batch（可压缩）：
- Batch I：types + client + 路由 + 删除（Task 1-2）
- Batch II：业务页面适配（Task 3）
- Batch III：验证 + commit（Task 4-5）
