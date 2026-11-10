// Command opencode2api starts the API gateway and its management interface.
package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	adminui "opencode2api/internal/admin"
	"opencode2api/internal/buildinfo"
	"opencode2api/internal/config"
	"opencode2api/internal/gateway"
	"opencode2api/internal/telemetry"
)

// version remains the linker injection point used by release builds.
var version = "dev"

// main loads the configuration, starts the gateway runtime and the API and
// WebUI servers, and shuts them down on SIGINT or SIGTERM.
func main() {
	buildinfo.Version = version
	configPath := flag.String("config", "config.json", "path to config.json")
	listen := flag.String("listen", "", "override the configured API listen address")
	webListen := flag.String("web-listen", "", "override the configured WebUI listen address")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("configuration error", "error", err)
		os.Exit(1)
	}
	if *listen != "" {
		cfg.Listen = *listen
	}
	if *webListen != "" {
		cfg.WebUI.Listen = *webListen
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	level := new(slog.LevelVar)
	telemetry.SetLogLevel(level, cfg.Logging.Level)

	// The telemetry database keeps metrics and log events across restarts. An
	// unusable path degrades to the in-memory behavior instead of stopping the
	// gateway, so the console still works and the reason is in the event log.
	var store *telemetry.Store
	if cfg.Storage.Enabled {
		opened, err := telemetry.OpenStore(cfg.Storage.Path)
		if err != nil {
			store = nil
			slog.Warn("telemetry database unavailable; keeping metrics in memory",
				"component", "telemetry", "event", "storage_unavailable", "path", cfg.Storage.Path, "error", err)
		} else {
			store = opened
			defer func() { _ = store.Close() }()
		}
	}
	hub, err := telemetry.LogHub(cfg.Logging.RingSize, store)
	if err != nil {
		slog.Warn("log history unavailable; starting with an empty buffer",
			"component", "telemetry", "event", "storage_unavailable", "error", err)
		hub, _ = telemetry.LogHub(cfg.Logging.RingSize, nil)
	}
	redactor := config.SecretRedactor()
	redactor.Replace(cfg)
	logger := telemetry.StructuredLogger(level, hub, redactor)
	if store != nil {
		logger.Info("telemetry database ready", "component", "telemetry", "event", "storage_ready",
			"path", store.Path(), "retention_hours", telemetry.RetentionHours)
	}
	monitor, err := telemetry.Monitor(store)
	if err != nil {
		logger.Warn("metrics history unavailable; starting from zero", "component", "telemetry", "event", "storage_unavailable", "error", err)
		monitor, _ = telemetry.Monitor(nil)
	}
	defer monitor.Close()
	manager, err := gateway.RuntimeManager(ctx, *configPath, cfg, logger, monitor, hub, redactor, level)
	if err != nil {
		logger.Error("failed to initialize runtime", "component", "runtime", "event", "runtime_initialization_failed", "error", err)
		os.Exit(1)
	}
	defer manager.Shutdown()

	apiServer := &http.Server{
		Addr: cfg.Listen, Handler: manager.Handler(), ReadHeaderTimeout: 15 * time.Second, IdleTimeout: 120 * time.Second,
	}
	servers := []*http.Server{apiServer}
	go serveHTTP(cancel, logger, apiServer, "api")

	if cfg.WebUI.Enabled {
		admin := adminui.New(manager, monitor, hub, logger)
		webServer := &http.Server{
			Addr: cfg.WebUI.Listen, Handler: admin.Handler(), ReadHeaderTimeout: 15 * time.Second, IdleTimeout: 120 * time.Second,
		}
		servers = append(servers, webServer)
		go serveHTTP(cancel, logger, webServer, "webui")
	}

	<-ctx.Done()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	for _, server := range servers {
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("graceful shutdown failed", "component", "server", "event", "shutdown_failed", "address", server.Addr, "error", err)
		}
	}
}

// serveHTTP serves server until it is shut down. An unexpected stop is logged
// and cancels the root context, which begins the shutdown of every server.
func serveHTTP(cancel context.CancelFunc, logger *slog.Logger, server *http.Server, component string) {
	logger.Info("server listening", "component", component, "event", "server_started", "address", server.Addr, "version", buildinfo.Version)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("server stopped unexpectedly", "component", component, "event", "server_failed", "address", server.Addr, "error", err)
		cancel()
	}
}
