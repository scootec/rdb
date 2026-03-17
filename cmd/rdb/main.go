package main

import (
	"context"
	"fmt"
	"os"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/scootec/rdb/internal/backup"
	"github.com/scootec/rdb/internal/config"
	"github.com/scootec/rdb/internal/docker"
	"github.com/scootec/rdb/internal/restic"
	"github.com/scootec/rdb/internal/scheduler"
)

func main() {
	// Default pretty console logging until we have config
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]

	// "snapshots" and "maintenance" don't need full config validation,
	// but most commands do. Load config for all commands.
	cfg, err := config.Load()
	if err != nil && cmd != "help" {
		log.Fatal().Err(err).Msg("configuration error")
	}

	if cfg != nil {
		setupLogging(cfg.LogLevel)
	}

	switch cmd {
	case "run":
		runScheduler(cfg)
	case "backup":
		runBackup(cfg)
	case "status":
		runStatus(cfg)
	case "snapshots":
		runSnapshots(cfg)
	case "maintenance":
		runMaintenance(cfg)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `Usage: rdb <command>

Commands:
  run          Start the cron scheduler (default container entrypoint)
  backup       Run a backup immediately
  status       Show discovered containers and their backup configuration
  snapshots    List restic snapshots
  maintenance  Run forget + prune + check`)
}

func setupLogging(level string) {
	l, err := zerolog.ParseLevel(level)
	if err != nil {
		l = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(l)
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
}

func buildDeps(cfg *config.Config) (*docker.Client, *restic.Runner, *backup.Orchestrator, error) {
	dc, err := docker.New()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("connecting to Docker: %w", err)
	}
	rc := restic.New()
	orch := backup.New(cfg, dc, rc)
	return dc, rc, orch, nil
}

func runScheduler(cfg *config.Config) {
	dc, rc, orch, err := buildDeps(cfg)
	if err != nil {
		log.Fatal().Err(err).Send()
	}
	defer dc.Close()

	if !cfg.SkipInit {
		if err := rc.InitRepo(); err != nil {
			log.Fatal().Err(err).Msg("failed to initialise restic repository")
		}
	}

	err = scheduler.Run(cfg.CronSchedule, func(ctx context.Context) error {
		return orch.Run(ctx)
	})
	if err != nil {
		log.Fatal().Err(err).Msg("scheduler error")
	}
}

func runBackup(cfg *config.Config) {
	dc, rc, orch, err := buildDeps(cfg)
	if err != nil {
		log.Fatal().Err(err).Send()
	}
	defer dc.Close()

	if !cfg.SkipInit {
		if err := rc.InitRepo(); err != nil {
			log.Fatal().Err(err).Msg("failed to initialise restic repository")
		}
	}

	ctx := context.Background()
	if err := orch.Run(ctx); err != nil {
		log.Fatal().Err(err).Msg("backup failed")
	}
	log.Info().Msg("backup complete")
}

func runStatus(cfg *config.Config) {
	dc, err := docker.New()
	if err != nil {
		log.Fatal().Err(err).Send()
	}
	defer dc.Close()

	rc := restic.New()
	orch := backup.New(cfg, dc, rc)

	ctx := context.Background()
	if err := orch.Status(ctx); err != nil {
		log.Fatal().Err(err).Send()
	}
}

func runSnapshots(cfg *config.Config) {
	rc := restic.New()
	if err := rc.Snapshots(); err != nil {
		log.Fatal().Err(err).Send()
	}
}

func runMaintenance(cfg *config.Config) {
	rc := restic.New()

	policy := restic.RetentionPolicy{
		Daily:   cfg.KeepDaily,
		Weekly:  cfg.KeepWeekly,
		Monthly: cfg.KeepMonthly,
		Yearly:  cfg.KeepYearly,
		Last:    cfg.KeepLast,
		Hourly:  cfg.KeepHourly,
		Within:  cfg.KeepWithin,
	}

	if err := rc.Forget(policy); err != nil {
		log.Fatal().Err(err).Msg("forget failed")
	}
	if err := rc.Prune(); err != nil {
		log.Fatal().Err(err).Msg("prune failed")
	}
	if err := rc.Check(); err != nil {
		log.Fatal().Err(err).Msg("check failed")
	}
	log.Info().Msg("maintenance complete")
}
