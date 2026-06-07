# E2E Tests

基于 Playwright 的端到端测试，覆盖 cloudflared-manager（守护进程 `cfdmgrd`）Web 面板的关键回归路径。

## 前置

- Node 20+
- 项目已构建出 daemon 二进制：

```bash
# 推荐（同时重建内嵌的 web/dist）
make build-host

# 或手动构建 daemon
#   Linux / macOS
go build -o bin/cfdmgrd ./cmd/cfdmgrd
#   Windows
go build -o bin/cfdmgrd.exe ./cmd/cfdmgrd
```

> globalSetup 按顺序找 `bin/cfdmgrd-dev[.exe]` → `bin/cfdmgrd[.exe]`（`.exe` 仅 Windows）。

## 首次安装浏览器

```bash
cd web
npm run test:e2e:install      # 安装 Chromium
```

## 跑测试

```bash
cd web
npm run test:e2e              # 无头运行
npm run test:e2e:ui           # Playwright UI 模式（调试）
npm run test:e2e:report       # 跑完后查看 HTML 报告
```

只跑某一个 spec：

```bash
npm run test:e2e -- 01-cloudflared-lifecycle.spec.ts
```

## 架构

- 每个 worker 用 `fixtures/daemon.ts` 拿到独立 daemon fixture：
  - 启独立 `cfdmgrd[-dev][.exe]` 子进程
  - 监听 `127.0.0.1:18080 + workerIndex`（端口逐 worker 偏移）
  - 注入环境变量：`CFDM_API_TOKEN` / `CFDM_HTTP_ADDR` / `CFDM_DATA_DIR` / `CFDM_LOG_LEVEL`
    （**无 token 时 cfdmgrd 直接拒绝启动**）
  - 独立 `e2e-tmp/<workerN>-<rand>/` 数据目录
  - 子进程 stdout/stderr 落到 `daemon.log`
  - 轮询**无需鉴权**的 `GET /api/v1/health` 直到 200（max 5s）
- 串行 (`workers: 1`) 避免端口冲突
- 测试结束：
  - 成功：自动 kill daemon + 删 TempDir
  - 失败：kill daemon，**保留** TempDir 供事后查
- Trace / screenshot / video 仅在失败时保留，在 `playwright-report/` 和 `test-results/`

## 加新测试

1. `e2e/` 下新建 `NN-name.spec.ts`
2. 从 `./fixtures/daemon` import `test, expect`
3. **选择器集中加在 `helpers/selectors.ts`**，不要在 spec 内写裸 CSS/XPath
4. 复杂 setup 走 `helpers/api.ts` 直接调 REST API（绕过 UI 加速）
5. 跑 `npm run test:e2e -- NN-name.spec.ts` 调试

### 找不到选择器时

1. 用 `npm run test:e2e:ui -- NN-name.spec.ts` 在浏览器里交互定位
2. 或 `npx playwright codegen http://127.0.0.1:18080` 录制后复制选择器到 `selectors.ts`
3. 仍不行可在 React 组件加 `data-testid`，并在 commit message 中标注

### 创建配置

`helpers/api.ts` 的 `api(daemon).createConfig(id, name?, config?, manualStart?)` 通过 REST API 直接创建一份 cloudflared 隧道配置：

- payload 形如 `{ id, config: TunnelConfigV1, cfdmgr: { name, manualStart } }`
  —— 信封键是 **`cfdmgr`**（不是旧的 `frpsmgr`）
- 默认 `manualStart: true`，避免 fixture daemon 启动时被 `AutoStart` 自动拉起
- `config` 默认用 `helpers/config.ts` 的 `minimalTunnelConfig()`（含一个合法的 120 字符 dummy token）
- `TunnelConfigV1` 字段全部 **camelCase**（`edge`/`reliability`/`logging`/`identity`/`advancedEnvOverrides`）；
  后端 `decodeJSON` 开启 `DisallowUnknownFields`，多发未知键会 400

### Token 约束

- `token` 由独立字段管理（信封响应里**永远脱敏**，`has_token` 表示是否已设置）。
- 要让实例真正 `start`，token 长度需落在 `[100,1500]` 且仅含 base64 字符
  (`[A-Za-z0-9_\-+/=]`)。`helpers/config.ts` 的 `dummyToken()` 产出 120 个 `'A'`，满足校验。
- **CI 里没有真实 cloudflared 二进制**，所以 `start` 只断言「返回 200 Snapshot」，
  实例随后落到 `stopped` 并带 `last_error`（缺二进制 / 连不上 edge）是预期内的。
  测试**不**假定隧道真正达到 `started`。

## 已覆盖场景

| Spec | 验证目标 |
|---|---|
| `01-cloudflared-lifecycle.spec.ts` | 登录 → API 建配置（断言 `has_token` 且响应不回明文 token）→ UI 看到卡片 → API `start` 返回 Snapshot（不假定真起来）→ stop 幂等 → PUT 改名（token 留空保留原值）→ UI 反映新名 → delete → 列表/卡片消失 |
| `02-no-frp-residue.spec.ts` | 侧栏品牌 "Cloudflared Manager" / 菜单含 "cloudflared 实例"、"二进制管理"、"告警" / 侧栏导航无 frps·frpc·NAT 残留 / 已删 `/runtime/*`、`/nathole/*`、`/configs/{id}/proxies` 返 404 / 现存 `/metrics/{id}/traffic`、`/alerts`、`/health` 可达 |

## 已知约束

- **必须先 build daemon** 才能跑 e2e（globalSetup 校验 `bin/cfdmgrd[-dev][.exe]` 存在）。
- **Windows 杀软**可能拦截 daemon 子进程启动；出现 EPERM/ACCESS_DENIED 把 `bin/` 加入白名单。
- 负向文案检查只针对登录后的应用外壳（侧栏导航），不扫整页 body：登录页 Hero
  目前仍残留 "FRPS Manager" 字样（属 `web/src` 范畴，需在源码侧单独清理）。

## 未来扩展

- **CI 集成**：GitHub Actions 加：
  ```yaml
  - run: cd web && npm ci --legacy-peer-deps
  - run: cd web && npx playwright install chromium
  - run: make build-host           # 或 go build -o bin/cfdmgrd ./cmd/cfdmgrd
  - run: cd web && npm run test:e2e
  - uses: actions/upload-artifact@v4
    if: failure()
    with: { name: playwright-report, path: web/playwright-report }
  ```
- **多浏览器**：`playwright.config.ts` 的 `projects` 段加 firefox / webkit。
- **更多场景**：
  - 通过 UI Modal（"新建" → 填 ID/名称/Cloudflared Token + YAML 编辑器）端到端建配置
  - Binaries 页：安装 / 激活 / 删除某个 cloudflared 版本
  - Traffic 页：选实例 → 时间范围 → 曲线（即便是 0）能渲染
  - Alerts 页：建规则 → 触发后事件历史出现
  - YAML 原始编辑（GET/PUT `/configs/{id}/raw`）与可视化配置的一致性

## 故障排查

| 现象 | 原因 / 解决 |
|---|---|
| globalSetup 抛错 "cfdmgrd binary not found" | 先 `make build-host` 或 `go build -o bin/cfdmgrd[.exe] ./cmd/cfdmgrd` |
| globalSetup 找到的是**旧版** frps 二进制 | 删掉残留的 `bin/frpsmgrd*` / `bin/frpmgrd*`，只留新构建的 `bin/cfdmgrd*` |
| Daemon 起不来（5s 超时） | 看 `e2e-tmp/<worker>/daemon.log` 末尾：可能端口被占 / 杀软拦截 / `CFDM_API_TOKEN` 未注入（缺它直接退出）|
| 选择器找不到（Locator not found） | 用 `npm run test:e2e:ui` 实地探测 + 改 `selectors.ts` |
| `createConfig 400 unknown field` | payload 含 `TunnelConfigV1` 不认的 key（`DisallowUnknownFields`）；核 `helpers/config.ts` 的 `minimalTunnelConfig` |
| `start` 后状态一直不是 `started` | 这是 CI 预期：无真实 cloudflared 二进制，实例会回落 `stopped` 并带 `last_error`；测试只断言返回 Snapshot |
