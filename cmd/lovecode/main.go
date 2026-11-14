// Command lovecode runs the LoveCode backend.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lovecode/lovecode/internal/agents"
	"github.com/lovecode/lovecode/internal/api"
	"github.com/lovecode/lovecode/internal/audit"
	"github.com/lovecode/lovecode/internal/config"
	"github.com/lovecode/lovecode/internal/core"
	"github.com/lovecode/lovecode/internal/providers"
	"github.com/lovecode/lovecode/internal/routing"
	"github.com/lovecode/lovecode/internal/security"
	"github.com/lovecode/lovecode/internal/storage"
	"github.com/lovecode/lovecode/internal/tools"
)

// version is overridden at build time: -ldflags "-X main.version=1.2.3".
var version = "0.1.0-dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "lovecode:", err)
		os.Exit(1)
	}
}

func newLogger(c config.Logging) *slog.Logger {
	var lvl slog.Level
	_ = lvl.UnmarshalText([]byte(c.Level))
	opts := &slog.HandlerOptions{Level: lvl}
	if c.Format == "json" {
		return slog.New(slog.NewJSONHandler(os.Stderr, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stderr, opts))
}

func run() error {
	cfgPath := flag.String("config", "", "path to TOML config file (defaults are used if empty)")
	showVersion := flag.Bool("version", false, "print version and exit")
	backupTo := flag.String("backup", "", "write a database backup to this path and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("lovecode", version)
		return nil
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	log := newLogger(cfg.Logging)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := storage.Open(ctx, cfg.DBPath())
	if err != nil {
		return err
	}
	applied, err := db.Migrate(ctx)
	if err != nil {
		db.Close()
		return err
	}
	if len(applied) > 0 {
		log.Info("migrations applied", "files", applied)
	}
	if *backupTo != "" {
		defer db.Close()
		if err := db.Backup(ctx, *backupTo); err != nil {
			return err
		}
		fmt.Println("backup written to", *backupTo)
		return nil
	}

	tokens := security.NewTokenStore(db.DB)
	auditLog := audit.New(db.DB)

	tokenPath, created, err := security.BootstrapOwner(ctx, tokens, cfg.DataDir)
	if err != nil {
		db.Close()
		return err
	}
	if created {
		// Log the path only, never the secret.
		log.Warn("created owner token", "file", tokenPath)
		_ = auditLog.Append(ctx, audit.Event{Actor: "system", Action: "token.bootstrap", Target: "owner"})
	}

	providerManager := providers.NewManager(providers.NewRegistry(), nil, log)
	router := routing.NewWithOptions(providerManager, routing.RouterOptions{
		DB:             db.DB,
		HealthInterval: 60 * time.Second,
	})

	toolsStore := tools.NewStore(db.DB)
	toolsService := tools.NewService(toolsStore, auditLog, log)

	agentStore := agents.NewStore(db.DB)
	agentService := agents.NewService(cfg.Agents, agentStore, router, auditLog, log)
	agentService.SetToolExecutor(toolsService.Executor())
	agentService.SetToolRegistry(toolsService.Registry())

	rt := core.New(log)
	rt.Register(core.FuncModule{
		ModName: "storage",
		OnStop:  func(context.Context) error { return db.Close() },
		OnHealth: func(c context.Context) core.Health {
			if err := db.Ping(c); err != nil {
				return core.Health{Status: core.StatusDown, Detail: "database unreachable"}
			}
			return core.Health{Status: core.StatusOK}
		},
	})
	rt.Register(core.FuncModule{
		ModName: "providers",
		OnStart: func(startCtx context.Context) error {
			if err := providerManager.Load(startCtx, cfg.Providers); err != nil {
				_ = auditLog.Append(startCtx, audit.Event{
					Actor: "system", Action: "provider.load_failed",
					Detail: map[string]any{"error": providers.SanitizeText(err.Error())},
				})
				_ = providerManager.Close(context.Background())
				return err
			}
			for _, st := range providerManager.ProviderStatuses() {
				if st.LastError == "" {
					continue
				}
				_ = auditLog.Append(startCtx, audit.Event{
					Actor: "system", Action: "provider.init_failed", Target: st.ID,
					Detail: map[string]any{"kind": st.LastErrorKind, "error": st.LastError},
				})
			}
			log.Info("providers loaded", "configured", len(cfg.Providers), "registered", providerManager.Registry().Count())
			return nil
		},
		OnStop: func(stopCtx context.Context) error { return providerManager.Close(stopCtx) },
		OnHealth: func(context.Context) core.Health {
			failed := 0
			for _, st := range providerManager.ProviderStatuses() {
				if st.LastError != "" {
					failed++
				}
			}
			if failed > 0 {
				return core.Health{Status: core.StatusDegraded, Detail: "one or more configured providers failed to initialize"}
			}
			return core.Health{Status: core.StatusOK}
		},
	})
	rt.Register(core.FuncModule{
		ModName: "router",
		OnStart: func(context.Context) error {
			if hm := router.HealthMonitor(); hm != nil {
				hm.Start()
			}
			return nil
		},
		OnStop: func(context.Context) error {
			if hm := router.HealthMonitor(); hm != nil {
				hm.Stop()
			}
			return nil
		},
		OnHealth: func(context.Context) core.Health {
			return core.Health{Status: core.StatusOK}
		},
	})
	rt.Register(toolsService)
	rt.Register(agentService)
	rt.Register(api.New(api.Deps{
		Config: cfg.Server, Log: log, Tokens: tokens, Audit: auditLog, Providers: providerManager, Router: router,
		Agents: agentService, Tools: toolsService, Health: rt.Health, Version: version,
	}))

	if err := rt.Start(ctx); err != nil {
		return err
	}
	_ = auditLog.Append(ctx, audit.Event{Actor: "system", Action: "system.start", Detail: map[string]any{"version": version}})
	log.Info("lovecode ready", "version", version, "data_dir", cfg.DataDir)

	<-ctx.Done()
	log.Info("shutting down")

	sctx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout.Std())
	defer cancel()
	_ = auditLog.Append(sctx, audit.Event{Actor: "system", Action: "system.stop"})
	return rt.Stop(sctx)
}
