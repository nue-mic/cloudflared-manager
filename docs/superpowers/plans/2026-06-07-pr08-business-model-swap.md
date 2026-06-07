# PR-08 业务模型替换：TunnelConfigV1 + cfdflags 注入 + ProcessTailer + 端口分配

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development.

**Goal:** 把 manager / instance / sampler / api handlers 从旧 frps `ServerConfigV1` 切换到 `pkg/cfdconfig.TunnelConfigV1`；instance.start() 真正注入 `TUNNEL_TOKEN` + `TUNNEL_*` env + `--metrics 127.0.0.1:<port>`；接入 `internal/logtail.ProcessTailer`；端口分配按 instance id 哈希到 `20241-20999`；删除 `/api/v1/runtime/*` 路由。完成后："创建一个含真实 token 的 instance + 启动 → cloudflared 子进程跑起来 + sampler 拉到指标"端到端可用。

**Out-of-scope（留 PR-08b 或 PR-10）：** AES-GCM 加密导出 / token 单独 endpoint / 错误码细化 / `openapi.yaml` 全文重写 / WS 事件类型清理。

**Architecture:**
- manager 的 `Get/Create/Update/WriteRaw/ReadRaw` 全部以 `*cfdconfig.TunnelConfigV1` 作为参数 / 返回类型；`LoadAll` 扫 `*.yaml`
- instance 持有当前 yaml path → start 前先 `os.ReadFile + cfdconfig.ParseYAML` → 投影到 `cfdflags.Options` → `cfdflags.ToTunnelEnv()` 得 `map[string]string` → 合并 `os.Environ()` 后追加强制 env：`TUNNEL_TOKEN` / `NO_AUTOUPDATE=true` / `AUTOUPDATE_FREQ=87600h` / `TUNNEL_METRICS=127.0.0.1:<port>` / `TUNNEL_OUTPUT=json`
- 端口分配：`port := crc32(instanceID) % 758 + 20241`；持久化到 instance 字段（重启进程时按 instance id hash 重算，所以同一 id 一定拿同一端口）
- ProcessTailer：instance 实例化时 `logtail.NewProcessTailer(id, 8000)`；Attach(stdout, stderr) 在 spawn 后；OnExit 在子进程退出时；instance 暴露 `Tailer()` 方法供 api logs handler 订阅（PR-08b 时接 WS）
- manager.MetricsAddr 真实返回 `127.0.0.1:<port>`

**Tech Stack:** std lib + `hash/crc32`；现有 cfdconfig / cfdflags / logtail / process / cfdbin 包。

---

## 文件清单（净改 8 文件）

| 路径 | 动作 | 关键点 |
|---|---|---|
| `internal/manager/manager.go` | Modify | LoadAll 扫 yaml；Get/Create/Update/WriteRaw 改 TunnelConfigV1；MetricsAddr 实装；import `cfdconfig` 删 `config` |
| `internal/manager/instance.go` | Modify | start() 整体重写 spawn 流程（读 yaml → cfdflags.Options → ToTunnelEnv → 强制 env → process.Spawn）；持有 ProcessTailer + metricsPort 字段；Snapshot.BinaryVersion / PID / MetricsPort 填充 |
| `internal/manager/manager_test.go` | Modify | 删 pkg/config 引用，用 cfdconfig 重新 fixture（最小化） |
| `internal/metrics/sampler.go` | Modify | 删 var blank、tick 调 evalRules 与 publishAlert（恢复告警路径） |
| `internal/api/configs.go` | Modify | envelope.Config 改 *TunnelConfigV1；Frpsmgr → Cfdmgr；Patch 用 cfdconfig JSON 编解码；GetRaw/PutRaw 用 YAML（Content-Type application/yaml）；Duplicate 强制清 token |
| `internal/api/validate.go` | Modify | 删 fvalidation + pkg/config；改用 cfdconfig.ParseYAML/ParseJSON + Validate + cfdflags cross-field（postQuantum 强制 protocol=quic）|
| `internal/api/importexport.go` | Modify | 把 pkg/config 调用换 cfdconfig；只支持 YAML（删 TOML/INI 分支）；不做加密（PR-10） |
| `internal/api/runtime.go` | **DELETE** | 同时删 server.go 中 4 个路由注册 |
| `internal/api/server.go` | Modify | 删 runtime handler / 路由 |
| `internal/api/logs_test.go` | Modify | 删 pkg/config 引用（如有） |

**不动**：pkg/config（PR-11 删）、services/frps.go（PR-11 删）、cmd/cfdmgrd、cfdbin / cfdflags / cfdconfig / cfdstate / process / logtail / sysinfo / selfupdate / eventbus / appcfg / web / docs / yml 等。

---

## Task 1：基线
```bash
cd /d/Github_Codes_mia-clark/cloudflared-manager
git status && go vet ./... && go test ./... && go build -o /tmp/x ./cmd/cfdmgrd && rm -f /tmp/x
```

---

## Task 2 (Batch I)：manager.go + manager_test.go 业务模型替换

**改造点**：

`internal/manager/manager.go`：

```go
import (
    "github.com/mia-clark/cloudflared-manager/pkg/cfdconfig"  // <- 替换 pkg/config
    // 其它 import 不变
)

// LoadAll 改扫 *.yaml
func (m *Manager) LoadAll() error {
    files, err := filepath.Glob(filepath.Join(m.opts.ProfilesDir, "*.yaml"))
    if err != nil { return err }
    for _, f := range files {
        b, rerr := os.ReadFile(f)
        if rerr != nil { /* log skip */ continue }
        if _, perr := cfdconfig.ParseYAML(b); perr != nil { /* log skip */ continue }
        id := idFromPath(f)
        m.register(id, f)
    }
    return nil
}

// Get 返回 *cfdconfig.TunnelConfigV1
func (m *Manager) Get(id string) (Snapshot, *cfdconfig.TunnelConfigV1, MgrMeta, error) {
    inst := m.get(id)
    if inst == nil { return Snapshot{}, nil, MgrMeta{}, ErrNotFound }
    b, err := os.ReadFile(inst.Path())
    if err != nil { return Snapshot{}, nil, MgrMeta{}, err }
    sc, err := cfdconfig.ParseYAML(b)
    if err != nil { return Snapshot{}, nil, MgrMeta{}, err }
    snap := inst.Snapshot()
    snap.Name = m.nameOf(id)
    snap.LogPath = m.LogPath(id)
    mm := MgrMeta{Name: m.nameOf(id), ManualStart: m.meta.manualStart(id)}
    return snap, sc, mm, nil
}

// Create / Update / WriteRaw 同理：接收 *cfdconfig.TunnelConfigV1；用 cfdconfig.MarshalYAML 写
// pathFor: id+".toml" → id+".yaml"
func (m *Manager) pathFor(id string) string {
    return filepath.Join(m.opts.ProfilesDir, id+".yaml")
}

// MetricsAddr 实装：返回 instance 的真实端口
func (m *Manager) MetricsAddr(id string) (string, bool) {
    inst := m.get(id)
    if inst == nil { return "", false }
    if inst.State() != cfdstate.ConfigStateStarted { return "", false }
    return inst.MetricsAddr(), inst.MetricsAddr() != ""
}
```

`internal/manager/manager_test.go`：删测试中所有 `config.ServerConfigV1` 引用，改用 `cfdconfig.TunnelConfigV1`。如某测试依赖 frp 特有字段，**整测试 git rm**（写测试覆盖率不是 PR-08 目标）。

**验证**（Batch I 结束时）：
```bash
go vet ./internal/manager/... 2>&1 | head -10
```
预期：vet 报 api 包错（manager 公开签名变了 api 还没改）— 是预期，Batch II 修。manager 包内 vet 应 PASS。

---

## Task 3 (Batch I 续)：instance.go 接入

`internal/manager/instance.go`：

```go
import (
    "hash/crc32"
    "os"
    "strconv"
    "github.com/mia-clark/cloudflared-manager/internal/logtail"
    "github.com/mia-clark/cloudflared-manager/pkg/cfdconfig"
    "github.com/mia-clark/cloudflared-manager/pkg/cfdflags"
    // 其它不变
)

// instance struct 加字段：
type instance struct {
    // ... 原有 ...
    metricsPort int
    tailer      *logtail.ProcessTailer
}

// newInstance 加 tailer 初始化（容量从 env 取，0 默认 8000）：
func newInstance(id, path string, logger *slog.Logger, bus *eventbus.Bus, logSink io.Writer, binStore *cfdbin.Store) *instance {
    return &instance{
        // ... 原有 ...
        metricsPort: allocMetricsPort(id),
        tailer:      logtail.NewProcessTailer(id, 0),
    }
}

// allocMetricsPort 按 id hash 到 20241-20999
func allocMetricsPort(id string) int {
    return int(crc32.ChecksumIEEE([]byte(id)))%758 + 20241
}

// MetricsAddr 返回 "127.0.0.1:<port>"
func (i *instance) MetricsAddr() string {
    return "127.0.0.1:" + strconv.Itoa(i.metricsPort)
}

// Tailer 暴露 ProcessTailer 供 logs handler 订阅
func (i *instance) Tailer() *logtail.ProcessTailer { return i.tailer }

// start 整体重写：
func (i *instance) start(ctx context.Context) error {
    i.mu.Lock()
    if i.state == cfdstate.ConfigStateStarted || i.state == cfdstate.ConfigStateStarting {
        i.mu.Unlock()
        return errors.New("already running")
    }
    i.state = cfdstate.ConfigStateStarting
    i.lastErr = ""
    i.mu.Unlock()

    // 1. 读 yaml 解析 token + 所有可注入 flag
    raw, err := os.ReadFile(i.path)
    if err != nil { i.recordError(err); i.setState(cfdstate.ConfigStateStopped); return err }
    cfg, err := cfdconfig.ParseYAML(raw)
    if err != nil { i.recordError(err); i.setState(cfdstate.ConfigStateStopped); return err }
    if err := cfg.Validate(); err != nil { i.recordError(err); i.setState(cfdstate.ConfigStateStopped); return err }
    if cfg.Token == "" {
        err := errors.New("token is required to start cloudflared")
        i.recordError(err); i.setState(cfdstate.ConfigStateStopped); return err
    }

    // 2. 投影 → cfdflags.Options → env
    opts := cfdflags.Options{
        Protocol: cfg.Edge.Protocol, EdgeIPVersion: cfg.Edge.EdgeIPVersion,
        EdgeBindAddress: cfg.Edge.EdgeBindAddress, Region: cfg.Edge.Region,
        PostQuantum: cfg.Edge.PostQuantum,
        Retries: cfg.Reliability.Retries, GracePeriod: cfg.Reliability.GracePeriod,
        LogLevel: cfg.Logging.LogLevel, TransportLogLevel: cfg.Logging.TransportLogLevel,
        Tags: cfg.Identity.Tags, Label: cfg.Identity.Label,
        AdvancedEnvOverrides: cfg.AdvancedEnvOverrides,
    }
    userEnv := cfdflags.ToTunnelEnv(opts)

    // 3. 解析二进制路径（cfdbin 优先；fallback PATH "cloudflared"）
    binPath := "cloudflared"
    if i.binStore != nil {
        if p, err := i.binStore.Resolve(cfg.BinaryVersion); err == nil {
            binPath = p
        }
    }

    // 4. 构造 env：os.Environ() + userEnv + 强制 env（强制 last 胜出）
    env := append([]string{}, os.Environ()...)
    for k, v := range userEnv { env = append(env, k+"="+v) }
    env = append(env,
        "TUNNEL_TOKEN="+cfg.Token,
        "NO_AUTOUPDATE=true",
        "AUTOUPDATE_FREQ=87600h",
        "TUNNEL_METRICS="+i.MetricsAddr(),
        "TUNNEL_OUTPUT=json",
    )

    // 5. argv：tunnel + --no-autoupdate + 可选 --label + run
    args := []string{"tunnel", "--no-autoupdate"}
    args = append(args, cfdflags.LabelArgv(cfg.Identity.Label)...)
    args = append(args, "run")

    // 6. spawn — log sink 同时写文件（instanceLog）+ tailer
    sink := io.MultiWriter(i.logSink, &tailerWriter{t: i.tailer})

    runCtx, cancel := context.WithCancel(ctx)
    w, err := process.Spawn(runCtx, process.SpawnParams{
        BinaryPath: binPath, Args: args, Env: env,
        LogSink: sink,
        StartupGrace: 5 * time.Second, StopGrace: 5 * time.Second,
    })
    if err != nil {
        cancel(); i.recordError(err); i.setState(cfdstate.ConfigStateStopped)
        return fmt.Errorf("spawn cloudflared: %w", err)
    }
    i.mu.Lock(); i.w = w; i.cancel = cancel; i.mu.Unlock()

    // exit watcher（保留原逻辑 + OnExit 注入 tailer）
    go func() {
        <-w.Done()
        i.tailer.OnExit(w.Cmd().ProcessState)
        i.mu.Lock()
        stopping := i.state == cfdstate.ConfigStateStopping
        i.w = nil; i.cancel = nil
        i.mu.Unlock(); cancel()
        if !stopping {
            if exitErr := w.ExitErr(); exitErr != nil {
                i.recordError(fmt.Errorf("cloudflared exited: %w", exitErr))
            }
            i.setState(cfdstate.ConfigStateStopped)
        }
    }()

    i.setState(cfdstate.ConfigStateStarted)
    i.logger.Info("cloudflared instance started", slog.Int("pid", w.PID()), slog.String("metrics", i.MetricsAddr()))
    return nil
}

// tailerWriter 是把 ProcessTailer 当 io.Writer 用的适配器；
// Attach 对应的是 io.Reader，所以这里走一个特殊路径：
// 把 bytes 分行喂入 tailer.Attach 的等价（用 strings.Split + parseLine 走包私有 — 直接给 tailer 加公开 Write 方法）
```

**关键技术细节**：ProcessTailer 当前只有 `Attach(stdout, stderr io.Reader)` —— 适合给 process.Spawn 直接传两个 pipe，但 process.SpawnParams 只接受 一个 LogSink io.Writer。

**调整方案**（更直接）：

instance 持有 ProcessTailer；spawn 时 LogSink 只用 instanceLog 文件 sink；额外用 `tailer.Attach(io.NopCloser of nil, ...)` 不行 — 需要在 process 包加 stdout/stderr pipe 暴露 OR 在 instance 这里改成不用 SpawnParams.LogSink，而是直接拿子进程 pipe。

**最小改动方案**：给 process 包加暴露 stdout/stderr pipe 的方式；OR 让 ProcessTailer 也实现 io.Writer 接口（追加新方法 Write(p) 解析每行）。

**选 ProcessTailer.Write** 实现 io.Writer（更小改动）：
- 在 internal/logtail/process_tailer.go 加 `Write(p []byte) (n int, err error)` 方法：按 `\n` split → 每段调内部 `append(parseLine(seg, "process"))`
- 这样 instance 可以用 `io.MultiWriter(fileSink, tailer)`

**这是对 PR-06 的小补丁**，需要 implementer 改 process_tailer.go：

```go
// 加在 process_tailer.go 末尾（PR-08 范围内做）
func (p *ProcessTailer) Write(b []byte) (int, error) {
    if p.stopped.Load() {
        return len(b), nil
    }
    // 简化：按行 split，每行走 parseLine（source 标 "stream"）
    rem := p.tail
    rem = append(rem, b...)
    for {
        i := bytes.IndexByte(rem, '\n')
        if i < 0 { p.tail = rem; return len(b), nil }
        line := rem[:i]
        rem = rem[i+1:]
        p.append(parseLine(strings.TrimRight(string(line), "\r"), "stream"))
    }
}
```

需要在 struct 加 `tail []byte` 字段保留跨 Write 的残行。需要 import bytes。

---

## Task 4 (Batch I 续)：sampler 接通 evalRules

`internal/metrics/sampler.go`：tick 接 evalRules（PR-07 已写好框架，删 var blank）：

```go
func (s *Sampler) tick() {
    now := time.Now().Unix()
    stepSec := int64(s.interval / time.Second)
    if stepSec <= 0 { stepSec = 1 }
    rules, _ := s.store.ListRules()
    points := make([]TrafficPoint, 0, 16)

    for _, id := range s.src.RunningIDs() {
        addr, ok := s.src.MetricsAddr(id)
        if !ok { continue }
        samples, err := s.scrape(addr)
        if err != nil { s.log.Debug(...); continue }
        instPoints := s.toPoints(id, samples, now)
        points = append(points, instPoints...)
        // 评估告警（用第一个 point 的数据作为 server-scope）
        if len(instPoints) > 0 {
            sp := instPoints[0]
            s.evalRules(rules, id, "", sp.Conns, sp, stepSec, now)
        }
    }

    if len(points) > 0 {
        if err := s.store.InsertTraffic(points); err != nil {
            s.log.Warn("insert traffic failed", slog.Any("err", err))
        }
    }
}

// 删尾部 var blank：
// var (_ = (*Sampler).evalRules; _ = (*Sampler).applyRule)
```

---

## Task 5 (Batch II)：api 包跟进

`internal/api/configs.go`：

```go
import (
    "github.com/mia-clark/cloudflared-manager/pkg/cfdconfig"  // 替 pkg/config
    "gopkg.in/yaml.v3"  // 新增
)

// envelope
type configEnvelope struct {
    manager.Snapshot
    Config *cfdconfig.TunnelConfigV1 `json:"config"`
    Cfdmgr manager.MgrMeta            `json:"cfdmgr"`  // Frpsmgr → Cfdmgr
}

type createReq struct {
    ID     string                     `json:"id"`
    Config *cfdconfig.TunnelConfigV1  `json:"config"`
    Cfdmgr manager.MgrMeta            `json:"cfdmgr"`
}

// Patch：用 cfdconfig JSON 编解码
func (h *ConfigsHandler) Patch(w http.ResponseWriter, r *http.Request) {
    id := pathID(r)
    _, sc, mm, err := h.m.Get(id)
    if writeManagerError(w, err) { return }
    curBytes, _ := cfdconfig.MarshalJSON(sc)
    patch, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
    merged, err := mergeJSON(curBytes, patch)
    if err != nil { /* 400 */ return }
    next, err := cfdconfig.ParseJSON(merged)
    if err != nil { /* 400 */ return }
    if err := h.m.Update(id, next, mm); writeManagerError(w, err) { return }
    snap, fresh, freshMM, _ := h.m.Get(id)
    WriteJSON(w, http.StatusOK, configEnvelope{Snapshot: snap, Config: fresh, Cfdmgr: freshMM})
}

// GetRaw / PutRaw 改 YAML
func (h *ConfigsHandler) GetRaw(w http.ResponseWriter, r *http.Request) {
    // ... 不变
    w.Header().Set("Content-Type", "application/yaml")
    _, _ = w.Write(b)
}
// PutRaw 不变（manager.WriteRaw 会处理）

// Duplicate 清 token
func (h *ConfigsHandler) Duplicate(...) {
    _, sc, mm, err := h.m.Get(src)
    // ...
    if sc != nil { sc.Token = "" } // PR-08 强制清 token
    if err := h.m.Create(body.NewID, sc, mm); ...
}
```

注：yaml 直接借用现有 pkg/cfdconfig.MarshalYAML/ParseYAML 即可，**configs.go 不需要新增 yaml.v3 import**。撤销上面的 `gopkg.in/yaml.v3`。

`internal/api/validate.go`：整体重写：

```go
package api

import (
    "encoding/json"
    "io"
    "net/http"
    "strings"

    "github.com/mia-clark/cloudflared-manager/pkg/cfdconfig"
    "github.com/mia-clark/cloudflared-manager/pkg/cfdflags"
)

type ValidateHandler struct{}
func NewValidateHandler() *ValidateHandler { return &ValidateHandler{} }

type validateResp struct {
    Valid    bool     `json:"valid"`
    Errors   []string `json:"errors,omitempty"`
    Warnings []string `json:"warnings,omitempty"`
}

func (h *ValidateHandler) Validate(w http.ResponseWriter, r *http.Request) {
    ct := r.Header.Get("Content-Type")
    body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
    if err != nil { WriteError(w, http.StatusBadRequest, CodeBadRequest, "read body: "+err.Error(), nil); return }

    var cfg *cfdconfig.TunnelConfigV1
    if strings.Contains(ct, "application/json") {
        cfg, err = cfdconfig.ParseJSON(body)
    } else {
        cfg, err = cfdconfig.ParseYAML(body)
    }
    if err != nil { WriteJSON(w, http.StatusOK, validateResp{Valid: false, Errors: []string{err.Error()}}); return }

    if err := cfg.Validate(); err != nil {
        WriteJSON(w, http.StatusOK, validateResp{Valid: false, Errors: []string{err.Error()}})
        return
    }
    // cross-field: postQuantum 需 protocol=quic
    if cfg.Edge.PostQuantum && cfg.Edge.Protocol != "quic" {
        WriteJSON(w, http.StatusOK, validateResp{Valid: false, Errors: []string{"postQuantum requires protocol=quic"}})
        return
    }
    // 检查 advanced env 白名单
    var warnings []string
    for k := range cfg.AdvancedEnvOverrides {
        if !cfdflags.AllowEnvOverride(k) {
            warnings = append(warnings, "env "+k+" not in whitelist")
        }
    }
    WriteJSON(w, http.StatusOK, validateResp{Valid: true, Warnings: warnings})
}
```

`internal/api/importexport.go`：把 `config.ParseServerTOML` → `cfdconfig.ParseYAML`，`MarshalTOML` → `cfdconfig.MarshalYAML`；ImportFile 接受 .yaml/.yml 扩展名；TOML/INI/CONF 分支删除或改为 "unsupported format"。**重点**：persistRaw 函数把 raw 字节当 YAML 解析 + 校验后 manager.Create。其它逻辑不变。

`internal/api/runtime.go`：**整文件删除** (`git rm`)。

`internal/api/server.go`：删除 4 个 runtime 路由 + `runtime := NewRuntimeHandler(...)` 行。

`internal/api/logs_test.go`：如果含 pkg/config 引用，删测试 OR 改 cfdconfig.TunnelConfigV1 fixture。

`internal/manager/manager_test.go`：同上。

---

## Task 6：全量验证 + smoke
```bash
go vet ./...
go test ./... -count=1 2>&1 | tail -15
go build -o bin/cfdmgrd ./cmd/cfdmgrd && ./bin/cfdmgrd version
rm -rf ./tmp/data; mkdir -p ./tmp/data/profiles
# 写一个 fake 实例 yaml
cat > ./tmp/data/profiles/test.yaml <<'YAML'
token: ""
edge:
  protocol: auto
YAML
CFDM_API_TOKEN=dev CFDM_DATA_DIR=./tmp/data ./bin/cfdmgrd serve > /tmp/cfdmgrd-pr08.log 2>&1 &
SERVE_PID=$!
sleep 2
curl -fsS http://127.0.0.1:8080/api/v1/health
echo
curl -fsS -H "Authorization: Bearer dev" http://127.0.0.1:8080/api/v1/configs
echo
kill $SERVE_PID 2>/dev/null; sleep 1
rm -rf ./tmp/data /tmp/cfdmgrd-pr08.log
```
预期：全绿；configs 返回包含 test 实例。

```bash
gofmt -l internal/manager internal/metrics internal/api internal/logtail
```
预期：无输出（或只剩 baseline pre-existing）。

---

## Task 7：commit（controller）

---

## Self-Review

✅ manager / instance / sampler 业务模型整体替换（PR-04..07 累积能力终于在 PR-08 联调）
✅ 真实端口分配 + Token env 注入 + ProcessTailer 集成
✅ runtime route 整体删除
⏸ AES 加密导出 / token 单独 endpoint / openapi.yaml 重写 / WS 事件类型清理 → PR-08b
⏸ pkg/config / services/frps.go 仍存在 → PR-11 删 fatedier/frp 时一并清理

---

## Execution Handoff

2 batch：
- Batch I = Task 2-4（manager + instance + sampler + ProcessTailer.Write）
- Batch II = Task 5（api handlers 跟进 + 删 runtime）

Task 6 + commit 由 controller。
