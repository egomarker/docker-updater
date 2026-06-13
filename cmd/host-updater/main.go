package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/egomarker/docker-updater/internal/api"
	"github.com/egomarker/docker-updater/internal/config"
	"github.com/egomarker/docker-updater/internal/jobs"
	"github.com/egomarker/docker-updater/internal/startup"
	"github.com/egomarker/docker-updater/internal/update"
)

const version = "1.0.0"

func main() {
	var configPath string
	var showVersion bool

	flag.StringVar(&configPath, "config", config.DefaultPath(), "path to config.json")
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.Parse()

	if showVersion {
		_, _ = os.Stdout.WriteString(version + "\n")
		return
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load(configPath)
	if err != nil {
		logger.Error("load config failed", "error", err)
		os.Exit(1)
	}

	token, err := config.ReadBearerToken(cfg.Server.BearerTokenFile)
	if err != nil {
		logger.Error("read bearer token failed", "error", err)
		os.Exit(1)
	}

	store, err := jobs.NewStore(cfg.Paths.JobsRoot)
	if err != nil {
		logger.Error("init jobs store failed", "error", err)
		os.Exit(1)
	}

	if err := startup.RecoverRunningJobs(store, cfg.Paths.RuntimeRoot); err != nil {
		logger.Error("startup recovery failed", "error", err)
		os.Exit(1)
	}

	service := update.NewService(cfg, store, logger)
	handler := api.NewHandler(token, version, service, cfg.Limits.MaxTailLines)

	server := &http.Server{
		Addr:              cfg.Server.ListenAddress,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-sigCtx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("http shutdown failed", "error", err)
		}
	}()

	logger.Info("host-updater starting", "addr", cfg.Server.ListenAddress, "config", configPath, "version", version)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("http server failed", "error", err)
		os.Exit(1)
	}
}