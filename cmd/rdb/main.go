package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"
	// Embed the IANA time zone database so TZ works even in images
	// without /usr/share/zoneinfo.
	_ "time/tzdata"

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
		if err := rc.InitRepo(context.Background()); err != nil {
			log.Fatal().Err(err).Msg("failed to initialise restic repository")
		}
	}

	policy := retentionPolicy(cfg)

	jobs := []scheduler.Job{
		{
			Name:     "backup",
			Schedule: cfg.CronSchedule,
			Timeout:  cfg.BackupTimeout,
			Fn: reported(cfg.StateFile, "backup", health.NewPinger(cfg.HealthcheckURL), func(ctx context.Context) error {
				if err := orch.Run(ctx); err != nil {
					return err
				}
				log.Info().Msg("applying retention policy")
				return rc.Forget(ctx, policy)
			}),
		},
	}
	scheduled := []string{"backup"}

	if cfg.MaintenanceEnabled() {
		jobs = append(jobs, scheduler.Job{
			Name:     "maintenance",
			Schedule: cfg.MaintenanceCron,
			Timeout:  cfg.BackupTimeout,
			Fn: reported(cfg.StateFile, "maintenance", health.NewPinger(cfg.MaintenanceHealthcheckURL), func(ctx context.Context) error {
				if err := rc.Prune(ctx); err != nil {
					return err
				}
				return rc.Check(ctx)
			}),
		})
		scheduled = append(scheduled, "maintenance")
	} else {
		log.Warn().Msg("scheduled maintenance disabled (RDB_MAINTENANCE_CRON is empty or 'off') — run 'rdb maintenance' manually to prune and check the repository")
	}

	if err := health.MarkPending(cfg.StateFile, time.Now(), scheduled...); err != nil {
		log.Warn().Err(err).Str("path", cfg.StateFile).Msg("failed to write health state file")
	}

	if err := scheduler.Run(jobs, scheduler.Options{ShutdownGrace: cfg.ShutdownTimeout, Location: cfg.Location}); err != nil {
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

	// Ctrl-C / SIGTERM cancels the run so hung dumps unblock and the
	// partial-snapshot cleanup still gets a chance to run.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if cfg.BackupTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.BackupTimeout)
		defer cancel()
	}

	if !cfg.SkipInit {
		if err := rc.InitRepo(ctx); err != nil {
			log.Fatal().Err(err).Msg("failed to initialise restic repository")
		}
	}

	runErr := orch.Run(ctx)
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
	snaps, err := rc.SnapshotsAll(context.Background())
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
			timeStr = t.In(cfg.Location).Format("2006-01-02 15:04")
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

const restoreUsage = "Usage: rdb restore <snapshot-id> [--output <path>] [--stop] [--force]"

func runRestore(cfg *config.Config) {
	opts, err := parseRestoreArgs(os.Args[2:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n%s\n", err, restoreUsage)
		os.Exit(1)
	}

	dc, err := docker.New()
	if err != nil {
		log.Fatal().Err(err).Msg("connecting to Docker")
	}
	defer dc.Close()

	rc := restic.New(cfg.ResticHostname)
	restorer := restore.New(dc, rc)

	// Ctrl-C / SIGTERM cancels the restore instead of killing the process
	// outright, so restic releases its locks and a container stopped via
	// --stop is still restarted.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if err := restorer.Restore(ctx, opts); err != nil {
		log.Fatal().Err(err).Msg("restore failed")
	}
}

// parseRestoreArgs parses the arguments after "restore". It is strict on
// purpose: a database restore overwrites the live database, so a mistyped or
// unrecognised flag — "--output=dump.sql", "--ouput", a missing path — must
// fail instead of being ignored and falling through to an import.
func parseRestoreArgs(args []string) (restore.Options, error) {
	var opts restore.Options
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--output":
			if i+1 >= len(args) || args[i+1] == "" || strings.HasPrefix(args[i+1], "-") {
				return restore.Options{}, fmt.Errorf("--output requires a file path")
			}
			opts.OutputPath = args[i+1]
			i++
		case strings.HasPrefix(arg, "--output="):
			opts.OutputPath = strings.TrimPrefix(arg, "--output=")
			if opts.OutputPath == "" {
				return restore.Options{}, fmt.Errorf("--output requires a file path")
			}
		case arg == "--force":
			opts.Force = true
		case arg == "--stop":
			opts.Stop = true
		case strings.HasPrefix(arg, "-"):
			return restore.Options{}, fmt.Errorf("unknown restore option %q", arg)
		case opts.SnapshotID == "":
			opts.SnapshotID = arg
		default:
			return restore.Options{}, fmt.Errorf("unexpected argument %q: restore takes a single snapshot ID", arg)
		}
	}
	if opts.SnapshotID == "" {
		return restore.Options{}, fmt.Errorf("missing snapshot ID")
	}
	return opts, nil
}

func runMaintenance(cfg *config.Config) {
	rc := restic.New(cfg.ResticHostname)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	runErr := maintain(ctx, rc, cfg)
	report(cfg.StateFile, "maintenance", health.NewPinger(cfg.MaintenanceHealthcheckURL), runErr)
	if runErr != nil {
		log.Fatal().Err(runErr).Msg("maintenance failed")
	}
	log.Info().Msg("maintenance complete")
}

func maintain(ctx context.Context, rc *restic.Runner, cfg *config.Config) error {
	if err := rc.Forget(ctx, retentionPolicy(cfg)); err != nil {
		return fmt.Errorf("forget failed: %w", err)
	}
	if err := rc.Prune(ctx); err != nil {
		return fmt.Errorf("prune failed: %w", err)
	}
	if err := rc.Check(ctx); err != nil {
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

	if err := health.Evaluate(state, schedules, cfg.HealthGrace, time.Now().In(cfg.Location)); err != nil {
		fmt.Fprintf(os.Stderr, "unhealthy: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("healthy")
}
