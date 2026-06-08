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

	"github.com/mia-clark/cloudflared-manager/internal/api"
	"github.com/mia-clark/cloudflared-manager/internal/appcfg"
	"github.com/mia-clark/cloudflared-manager/internal/cfdbin"
	"github.com/mia-clark/cloudflared-manager/internal/eventbus"
	"github.com/mia-clark/cloudflared-manager/internal/manager"
	"github.com/mia-clark/cloudflared-manager/internal/metrics"
	"github.com/mia-clark/cloudflared-manager/internal/process"
	"github.com/mia-clark/cloudflared-manager/pkg/version"
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
	mgr.AutoStart()
	defer mgr.Shutdown()

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

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		logger.Info("shutdown signal received", slog.String("signal", sig.String()))
	case err := <-errCh:
		logger.Error("http server crashed", slog.Any("err", err))
		return 1
	}

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
