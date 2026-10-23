package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all configuration parsed from environment variables.
type Config struct {
	// Required restic settings
	ResticRepository string
	ResticPassword   string

	// Scheduler
	CronSchedule string
	// Location is the time zone cron schedules are evaluated in, taken from
	// the standard TZ variable. Unset TZ falls back to the system zone.
	Location *time.Location
	// MaintenanceCron schedules prune + check. Empty or "off" disables it.
	MaintenanceCron string
	// BackupTimeout bounds a single scheduled run (backup or maintenance):
	// when it expires the run's context is cancelled, aborting hung dumps
	// and restic invocations. Zero disables the timeout.
	BackupTimeout time.Duration
	// ShutdownTimeout is how long, after SIGTERM/SIGINT, an in-flight run may
	// keep going before it is aborted. Zero aborts in-flight runs immediately.
	ShutdownTimeout time.Duration

	// Logging
	LogLevel string

	// Backup behaviour
	IncludeProjectName bool
	ExcludeBindMounts  bool
	SkipInit           bool
	// ResticHostname is recorded as the snapshot hostname on backups so that
	// parent-snapshot selection survives container recreations.
	ResticHostname string

	// Retention policy
	KeepDaily   int
	KeepWeekly  int
	KeepMonthly int
	KeepYearly  int
	KeepLast    int
	KeepHourly  int
	KeepWithin  string

	// Health reporting. HealthcheckURL is pinged after each backup run and
	// MaintenanceHealthcheckURL after each maintenance run, both with
	// healthchecks.io semantics (success to the URL, failures to <url>/fail).
	HealthcheckURL            string
	MaintenanceHealthcheckURL string
	// StateFile records the outcome of the last runs for `rdb health`.
	StateFile string
	// HealthGrace is how far past its next scheduled run a job may be before
	// `rdb health` reports it overdue; it must cover the run duration itself,
	// since state is written when a run completes.
	HealthGrace time.Duration
}

// Load reads configuration from environment variables and returns a validated Config.
func Load() (*Config, error) {
	cfg := &Config{
		ResticRepository:   os.Getenv("RESTIC_REPOSITORY"),
		ResticPassword:     os.Getenv("RESTIC_PASSWORD"),
		CronSchedule:       envOrDefault("RDB_CRON_SCHEDULE", "0 2 * * *"),
		MaintenanceCron:    envAllowEmpty("RDB_MAINTENANCE_CRON", "0 4 * * 0"),
		BackupTimeout:      envDuration("RDB_BACKUP_TIMEOUT", 2*time.Hour),
		ShutdownTimeout:    envDuration("RDB_SHUTDOWN_TIMEOUT", 5*time.Minute),
		ResticHostname:     envOrDefault("RDB_RESTIC_HOSTNAME", "rdb"),
		LogLevel:           envOrDefault("RDB_LOG_LEVEL", "info"),
		IncludeProjectName: envBool("RDB_INCLUDE_PROJECT_NAME", false),
		ExcludeBindMounts:  envBool("RDB_EXCLUDE_BIND_MOUNTS", false),
		SkipInit:           envBool("RDB_SKIP_INIT", false),
		KeepDaily:          envInt("RESTIC_KEEP_DAILY", 7),
		KeepWeekly:         envInt("RESTIC_KEEP_WEEKLY", 4),
		KeepMonthly:        envInt("RESTIC_KEEP_MONTHLY", 12),
		KeepYearly:         envInt("RESTIC_KEEP_YEARLY", 3),
		KeepLast:           envInt("RESTIC_KEEP_LAST", 0),
		KeepHourly:         envInt("RESTIC_KEEP_HOURLY", 0),
		KeepWithin:         envOrDefault("RESTIC_KEEP_WITHIN", ""),

		HealthcheckURL:            os.Getenv("RDB_HEALTHCHECK_URL"),
		MaintenanceHealthcheckURL: os.Getenv("RDB_MAINTENANCE_HEALTHCHECK_URL"),
		StateFile:                 envOrDefault("RDB_STATE_FILE", "/tmp/rdb-status"),
		HealthGrace:               envDuration("RDB_HEALTH_GRACE", time.Hour),
	}

	loc, err := loadLocation()
	if err != nil {
		return nil, err
	}
	cfg.Location = loc

	if cfg.ResticRepository == "" {
		return nil, fmt.Errorf("RESTIC_REPOSITORY is required")
	}
	if cfg.ResticPassword == "" {
		return nil, fmt.Errorf("RESTIC_PASSWORD is required")
	}

	return cfg, nil
}

// MaintenanceEnabled reports whether the scheduled prune + check job is enabled.
func (c *Config) MaintenanceEnabled() bool {
	return c.MaintenanceCron != "" && !strings.EqualFold(c.MaintenanceCron, "off")
}

// loadLocation resolves the TZ variable the way the Go runtime does for
// time.Local, but fails loudly on an unknown zone instead of silently
// falling back to UTC, which would shift every scheduled run.
func loadLocation() (*time.Location, error) {
	tz, ok := os.LookupEnv("TZ")
	if !ok {
		return time.Local, nil
	}
	tz = strings.TrimPrefix(tz, ":")
	if strings.HasPrefix(tz, "/") {
		data, err := os.ReadFile(tz)
		if err != nil {
			return nil, fmt.Errorf("TZ: %w", err)
		}
		loc, err := time.LoadLocationFromTZData(tz, data)
		if err != nil {
			return nil, fmt.Errorf("TZ: invalid time zone file %s: %w", tz, err)
		}
		return loc, nil
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("TZ: unknown time zone %q (use an IANA name such as Europe/London)", tz)
	}
	return loc, nil
}

// envAllowEmpty returns the default only when key is unset. Unlike
// envOrDefault, a variable explicitly set to the empty string is preserved,
// so operators can use an empty value to disable a feature.
func envAllowEmpty(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func envDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
