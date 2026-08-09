package main

import (
	"context"
	"fmt"
	"os"

	"strings"
	"text/tabwriter"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/scootec/rdb/internal/backup"
	"github.com/scootec/rdb/internal/config"
	"github.com/scootec/rdb/internal/docker"
	"github.com/scootec/rdb/internal/health"
	"github.com/scootec/rdb/internal/restic"
	"github.com/scootec/rdb/internal/restore"
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
	case "restore":
		runRestore(cfg)
	case "maintenance":
		runMaintenance(cfg)
	case "health":
		runHealth(cfg)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `Usage: rdb <command>

Commands:
  run          Start the cron scheduler: backups, retention, maintenance (default container entrypoint)
  backup       Run a backup immediately
  status       Show discovered containers and their backup configuration
  snapshots    List restic snapshots
  restore      Restore a snapshot (use 'rdb snapshots' to find IDs)
  maintenance  Run forget + prune + check immediately
  health       Exit non-zero if the last run failed or is overdue (used by Docker HEALTHCHECK)`)
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
	rc := restic.New(cfg.ResticHostname)
	orch := backup.New(cfg, dc, rc)
	return dc, rc, orch, nil
}

func retentionPolicy(cfg *config.Config) restic.RetentionPolicy {
	return restic.RetentionPolicy{
		Daily:   cfg.KeepDaily,
		Weekly:  cfg.KeepWeekly,
		Monthly: cfg.KeepMonthly,
		Yearly:  cfg.KeepYearly,
		Last:    cfg.KeepLast,
		Hourly:  cfg.KeepHourly,
		Within:  cfg.KeepWithin,
	}
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

	policy := retentionPolicy(cfg)

	jobs := []scheduler.Job{
		{
			Name:     "backup",
			Schedule: cfg.CronSchedule,
			Fn: reported(cfg.StateFile, "backup", health.NewPinger(cfg.HealthcheckURL), func(ctx context.Context) error {
				if err := orch.Run(ctx); err != nil {
					return err
				}
				log.Info().Msg("applying retention policy")
				return rc.Forget(policy)
			}),
		},
	}
	scheduled := []string{"backup"}

	if cfg.MaintenanceEnabled() {
		jobs = append(jobs, scheduler.Job{
			Name:     "maintenance",
			Schedule: cfg.MaintenanceCron,
			Fn: reported(cfg.StateFile, "maintenance", health.NewPinger(cfg.MaintenanceHealthcheckURL), func(ctx context.Context) error {
				if err := rc.Prune(); err != nil {
					return err
				}
				return rc.Check()
			}),
		})
		scheduled = append(scheduled, "maintenance")
	} else {
		log.Warn().Msg("scheduled maintenance disabled (RDB_MAINTENANCE_CRON is empty or 'off') — run 'rdb maintenance' manually to prune and check the repository")
	}

	if err := health.MarkPending(cfg.StateFile, time.Now(), scheduled...); err != nil {
		log.Warn().Err(err).Str("path", cfg.StateFile).Msg("failed to write health state file")
	}

	if err := scheduler.Run(jobs); err != nil {
		log.Fatal().Err(err).Msg("scheduler error")
	}
}

// reported wraps a job function so that its outcome is written to the health
// state file and pinged to the configured healthcheck URL after every run.
func reported(stateFile, job string, pinger *health.Pinger, fn scheduler.RunFunc) scheduler.RunFunc {
	return func(ctx context.Context) error {
		err := fn(ctx)
		report(stateFile, job, pinger, err)
		return err
	}
}

// report records a run's outcome and pings the healthcheck URL. Reporting
// problems are logged but never affect the run result.
func report(stateFile, job string, pinger *health.Pinger, runErr error) {
	if err := health.Record(stateFile, job, runErr, time.Now()); err != nil {
		log.Warn().Err(err).Str("path", stateFile).Msg("failed to write health state file")
	}
	pinger.Ping(job, runErr)
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

	runErr := orch.Run(context.Background())
	report(cfg.StateFile, "backup", health.NewPinger(cfg.HealthcheckURL), runErr)
	if runErr != nil {
		log.Fatal().Err(runErr).Msg("backup failed")
	}
	log.Info().Msg("backup complete")
}

func runStatus(cfg *config.Config) {
	dc, err := docker.New()
	if err != nil {
		log.Fatal().Err(err).Send()
	}
	defer dc.Close()

	rc := restic.New(cfg.ResticHostname)
	orch := backup.New(cfg, dc, rc)

	ctx := context.Background()
	if err := orch.Status(ctx); err != nil {
		log.Fatal().Err(err).Send()
	}
}

func runSnapshots(cfg *config.Config) {
	rc := restic.New(cfg.ResticHostname)
	snaps, err := rc.SnapshotsAll()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to list snapshots")
	}

	if len(snaps) == 0 {
		fmt.Println("No snapshots found.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tTime\tType\tProject\tService\tPath")
	fmt.Fprintln(w, "--\t----\t----\t-------\t-------\t----")
	for _, snap := range snaps {
		snapType := snapshotType(snap)
		project := tagValue(snap, "project")
		service := tagValue(snap, "service")
		path := ""
		if len(snap.Paths) > 0 {
			path = snap.Paths[0]
		}
		t, err := time.Parse(time.RFC3339Nano, snap.Time)
		timeStr := snap.Time
		if err == nil {
			timeStr = t.Local().Format("2006-01-02 15:04")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", snap.ShortID, timeStr, snapType, project, service, path)
	}
	w.Flush()
}

func snapshotType(snap restic.Snapshot) string {
	for _, tag := range snap.Tags {
		switch tag {
		case "volume", "postgres", "mysql", "mariadb":
			return tag
		}
	}
	return "unknown"
}

func tagValue(snap restic.Snapshot, prefix string) string {
	for _, t := range snap.Tags {
		if strings.HasPrefix(t, prefix+":") {
			return strings.TrimPrefix(t, prefix+":")
		}
	}
	return ""
}

func runRestore(cfg *config.Config) {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "Usage: rdb restore <snapshot-id> [--output <path>] [--stop] [--force]")
		os.Exit(1)
	}

	snapshotID := os.Args[2]
	var outputPath string
	var force, stop bool
	for i := 3; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--output":
			if i+1 < len(os.Args) {
				outputPath = os.Args[i+1]
				i++
			}
		case "--force":
			force = true
		case "--stop":
			stop = true
		}
	}

	dc, err := docker.New()
	if err != nil {
		log.Fatal().Err(err).Msg("connecting to Docker")
	}
	defer dc.Close()

	rc := restic.New(cfg.ResticHostname)
	restorer := restore.New(dc, rc)

	ctx := context.Background()
	if err := restorer.Restore(ctx, restore.Options{
		SnapshotID: snapshotID,
		OutputPath: outputPath,
		Force:      force,
		Stop:       stop,
	}); err != nil {
		log.Fatal().Err(err).Msg("restore failed")
	}
}

func runMaintenance(cfg *config.Config) {
	rc := restic.New(cfg.ResticHostname)

	runErr := maintain(rc, cfg)
	report(cfg.StateFile, "maintenance", health.NewPinger(cfg.MaintenanceHealthcheckURL), runErr)
	if runErr != nil {
		log.Fatal().Err(runErr).Msg("maintenance failed")
	}
	log.Info().Msg("maintenance complete")
}

func maintain(rc *restic.Runner, cfg *config.Config) error {
	if err := rc.Forget(retentionPolicy(cfg)); err != nil {
		return fmt.Errorf("forget failed: %w", err)
	}
	if err := rc.Prune(); err != nil {
		return fmt.Errorf("prune failed: %w", err)
	}
	if err := rc.Check(); err != nil {
		return fmt.Errorf("check failed: %w", err)
	}
	return nil
}

// runHealth backs the Docker HEALTHCHECK: exit 0 when the last runs succeeded
// and are not overdue, exit 1 otherwise.
func runHealth(cfg *config.Config) {
	state, err := health.ReadState(cfg.StateFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "unhealthy: no run state at %s (scheduler not running?): %v\n", cfg.StateFile, err)
		os.Exit(1)
	}

	schedules := map[string]string{"backup": cfg.CronSchedule}
	if cfg.MaintenanceEnabled() {
		schedules["maintenance"] = cfg.MaintenanceCron
	}

	if err := health.Evaluate(state, schedules, cfg.HealthGrace, time.Now()); err != nil {
		fmt.Fprintf(os.Stderr, "unhealthy: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("healthy")
}
