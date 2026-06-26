package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/nue-mic/cloudflared-manager/internal/api"
	"github.com/nue-mic/cloudflared-manager/internal/appcfg"
	"github.com/nue-mic/cloudflared-manager/internal/cfaccount"
	"github.com/nue-mic/cloudflared-manager/internal/cfdbin"
	"github.com/nue-mic/cloudflared-manager/internal/cfdupdate"
	"github.com/nue-mic/cloudflared-manager/internal/eventbus"
	"github.com/nue-mic/cloudflared-manager/internal/manager"
	"github.com/nue-mic/cloudflared-manager/internal/metrics"
	"github.com/nue-mic/cloudflared-manager/internal/process"
	"github.com/nue-mic/cloudflared-manager/pkg/version"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		os.Exit(runServe(os.Args[2:]))
	case "health":
		os.Exit(runHealth(os.Args[2:]))
	case "version", "-v", "--version":
		fmt.Printf("cfdmgrd %s (built %s)\n", version.Number, version.BuildDate)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `cfdmgrd — headless cloudflared multi-instance manager daemon

USAGE
  cfdmgrd <command> [flags]

COMMANDS
  serve     Run the HTTP API server (default for containers)
  health    Probe /api/v1/health and exit non-zero on failure
  version   Print version information
  help      Show this help

ENV
  CFDM_API_TOKEN       Required. Bearer token for API auth.
  CFDM_HTTP_ADDR       Listen address (default ":8080")
  CFDM_DATA_DIR        Data root (default "/var/lib/cfdmgrd")
  CFDM_CORS_ORIGINS    Comma-separated origins or "*" (default "*")
  CFDM_LOG_LEVEL       trace|debug|info|warn|error (default "info")
  CFDM_DOCS_ENABLED    Expose /api/docs Scalar UI (default "true")`)
}

func runServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	_ = fs.Parse(args)

	cfg, err := appcfg.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		return 1
	}
	if err := cfg.EnsureDirs(); err != nil {
		fmt.Fprintf(os.Stderr, "cannot create data dirs: %v\n", err)
		return 1
	}

	logger := newLogger(cfg.LogLevel)
	// CFDM_HTTP_ADDR 归一化（只填端口→:端口）若遇到无法识别的值，会把原值原样保留
	// 交给 net.Listen 报错——这里把告警显式打出来（appcfg.Load 在 logger 之前运行，
	// 只能先暂存文本），避免运营者对着打不开的面板一头雾水。
	if cfg.HTTPAddrWarn != "" {
		logger.Warn("listen addr normalize", slog.String("detail", cfg.HTTPAddrWarn))
	}
	logger.Info("starting cfdmgrd",
		slog.String("addr", cfg.HTTPAddr),
		slog.String("data_dir", cfg.DataDir),
		slog.String("version", version.Number),
	)

	binStore := cfdbin.New(cfg.BinariesDir)
	binDl := &cfdbin.Downloader{
		BaseURLs: cfg.ReleaseProxyBases,
		Key:      cfg.ReleaseProxyKey,
	}

	// Arm the Windows Job Object (no-op on POSIX) so any cloudflared we
	// spawn dies with us, even on taskkill /F / IDE debug-stop / panic.
	// fail-open: orphan scanner below catches anything Job missed.
	if err := process.InitParentJob(); err != nil {
		logger.Warn("process: parent job init failed (kernel-level kill-on-exit not armed); relying on orphan scan",
			slog.Any("err", err),
		)
	} else if process.ParentJobReady() {
		logger.Info("process: parent job armed (kill-on-exit safety net active)")
	}

	// Sweep any cloudflared children left behind by a previous cfdmgrd
	// run (panic, taskkill /F, BSOD). Restricted to our cfdbin store
	// subtree so a user's hand-installed cloudflared cannot be touched.
	if killed, err := process.ScanAndKillOrphans(binStore.Root(), logger); err != nil {
		logger.Warn("process: orphan scan failed", slog.Any("err", err))
	} else if len(killed) > 0 {
		logger.Info("process: orphan scan terminated leftovers",
			slog.Int("count", len(killed)),
			slog.Any("pids", killed),
		)
	}

	bus := eventbus.New(1024)
	mgr, err := manager.New(manager.Options{
		ProfilesDir: cfg.ProfilesDir,
		LogsDir:     cfg.LogsDir,
		StoresDir:   cfg.StoresDir,
		MetaPath:    cfg.MetaFile,
		Logger:      logger,
		Bus:         bus,
		BinaryStore: binStore,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "init manager: %v\n", err)
		return 1
	}
	if err := mgr.LoadAll(); err != nil {
		fmt.Fprintf(os.Stderr, "load configs: %v\n", err)
		return 1
	}

	// cloudflared 二进制自动更新引擎：启动自举（无二进制则下载）+ 定时检查
	// 下载激活并滚动重启跟随实例，失败自动回滚。设置存 meta.json（UI 覆盖
	// env 默认）。Controller=mgr 负责重启实例与版本钉用查询。
	autoUpd := cfdupdate.New(cfdupdate.Config{
		Store:      cfdupdate.NewStoreAdapter(binStore, binDl),
		Release:    binDl,
		Controller: mgr,
		Bus:        bus,
		Logger:     logger,
		Load: func() (cfdupdate.Settings, bool) {
			m, ok := mgr.AutoUpdate()
			if !ok {
				return cfdupdate.Settings{}, false
			}
			return cfdupdate.Settings{
				Enabled:            m.Enabled,
				Mode:               m.Mode,
				IntervalHours:      m.IntervalHours,
				IncludePrerelease:  m.IncludePrerelease,
				AutoRollback:       m.AutoRollback,
				KeepVersions:       m.KeepVersions,
				HealthGraceSeconds: m.HealthGraceSeconds,
			}, true
		},
		Save: func(s cfdupdate.Settings) error {
			return mgr.SetAutoUpdate(manager.AutoUpdateMeta{
				Enabled:            s.Enabled,
				Mode:               s.Mode,
				IntervalHours:      s.IntervalHours,
				IncludePrerelease:  s.IncludePrerelease,
				AutoRollback:       s.AutoRollback,
				KeepVersions:       s.KeepVersions,
				HealthGraceSeconds: s.HealthGraceSeconds,
			})
		},
	})

	defer mgr.Shutdown()
	// 注意：二进制启动自举 + 实例自启动 + 定时调度被移到 HTTP 监听启动「之后」
	// 的后台 goroutine 执行（见下文 startup goroutine）。这样首启即便正在下载
	// 二进制，/health 与面板也立即可达，不会拖垮容器就绪/存活探针。

	// Cloudflare 账号 + 实例绑定存储（密钥 AES-GCM 落盘）。失败不致命：
	// 仅 CF 集成端点降级（store 为 nil 时 handler 自身不会被命中——路由仍注册，
	// 但任何 store 操作会 panic，故这里失败则禁用整段集成）。
	cfStore, err := cfaccount.New(cfg.CFStoreFile, cfg.SecretKeyFile)
	if err != nil {
		logger.Warn("cf account store disabled", slog.Any("err", err))
		cfStore = nil
	} else {
		// 实例被删除时清理其 Cloudflare 绑定，避免孤儿绑定被新同名实例继承。
		sub := bus.Subscribe(&eventbus.Filter{Types: []eventbus.EventType{eventbus.TypeConfigDeleted}}, 16)
		go func() {
			for ev := range sub.C() {
				if ev.ConfigID != "" {
					_ = cfStore.DeleteBinding(ev.ConfigID)
				}
			}
		}()
		defer sub.Unsubscribe()
	}

	// 时序指标存储 + 采样器：纯 Go SQLite，落 $DataDir/metrics.db。
	// 采样器每 interval 拉取每个运行中实例的 cloudflared --metrics 端点，
	// 解析 Prometheus 文本，写入 TrafficPoint，并评估告警规则。
	mstore, err := metrics.Open(filepath.Join(cfg.DataDir, "metrics.db"))
	if err != nil {
		logger.Warn("metrics store disabled", slog.Any("err", err))
		mstore = nil
	} else {
		defer mstore.Close()
		sampler := metrics.NewSampler(mstore, mgr, bus, logger, 10*time.Second, 7*24*time.Hour)
		samplerCtx, cancelSampler := context.WithCancel(context.Background())
		defer cancelSampler()
		go sampler.Run(samplerCtx)
	}

	handler := api.NewRouter(api.Deps{
		Cfg:              cfg,
		Logger:           logger,
		Manager:          mgr,
		Metrics:          mstore,
		BinaryStore:      binStore,
		BinaryDownloader: binDl,
		BinaryUpdater:    autoUpd,
		CFAccounts:       cfStore,
	})
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	// 信号在「后台预置」之前注册，使 bootstrap/下载期间收到的 SIGTERM/SIGINT
	// 也能触发优雅关闭（取消 appCtx → 中断下载与调度），而非被 Go 默认处置硬杀。
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	appCtx, appCancel := context.WithCancel(context.Background())
	defer appCancel()

	// 后台预置：与 HTTP 监听并行，绝不阻塞 /health。
	// ① 首启无二进制 → 同步下载激活最新版（内部按 enabled/已有二进制自动跳过；
	//   best-effort，失败仅告警，实例可回退 PATH），挂在 appCtx 上可被信号中断；
	// ② 自启动实例（此时二进制已就绪，确保用上新版）；
	// ③ 进入定时自动更新循环（首轮延迟 ~30s，之后每 interval 一次）。
	go func() {
		bctx, bcancel := context.WithTimeout(appCtx, 3*time.Minute)
		if err := autoUpd.BootstrapIfMissing(bctx); err != nil {
			logger.Warn("cloudflared bootstrap skipped/failed", slog.Any("err", err))
		}
		bcancel()
		if appCtx.Err() != nil {
			return // 关停中：不再自启动实例
		}
		mgr.AutoStart()
		autoUpd.Run(appCtx) // 阻塞至 appCtx 取消
	}()

	select {
	case sig := <-sigCh:
		logger.Info("shutdown signal received", slog.String("signal", sig.String()))
	case err := <-errCh:
		logger.Error("http server crashed", slog.Any("err", err))
		return 1
	}
	appCancel() // 取消后台预置/调度（中断在途下载、停止定时循环）

	ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownWait)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("graceful shutdown failed", slog.Any("err", err))
		return 1
	}
	logger.Info("bye")
	return 0
}

func runHealth(args []string) int {
	fs := flag.NewFlagSet("health", flag.ExitOnError)
	addr := fs.String("addr", "http://127.0.0.1:8080", "daemon base URL")
	_ = fs.Parse(args)

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(*addr + "/api/v1/health")
	if err != nil {
		fmt.Fprintf(os.Stderr, "health check failed: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "unhealthy: status=%d\n", resp.StatusCode)
		return 1
	}
	return 0
}

func newLogger(level string) *slog.Logger {
	var lv slog.Level
	switch strings.ToLower(level) {
	case "trace", "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lv}))
}
