package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds all configuration parsed from environment variables.
type Config struct {
	// Required restic settings
	ResticRepository string
	ResticPassword   string

	// Scheduler
	CronSchedule string

	// Logging
	LogLevel string

	// Backup behaviour
	IncludeProjectName bool
	ExcludeBindMounts  bool
	SkipInit           bool

	// Retention policy
	KeepDaily   int
	KeepWeekly  int
	KeepMonthly int
	KeepYearly  int
	KeepLast    int
	KeepHourly  int
	KeepWithin  string
}

// Load reads configuration from environment variables and returns a validated Config.
func Load() (*Config, error) {
	cfg := &Config{
		ResticRepository:   os.Getenv("RESTIC_REPOSITORY"),
		ResticPassword:     os.Getenv("RESTIC_PASSWORD"),
		CronSchedule:       envOrDefault("RDB_CRON_SCHEDULE", "0 2 * * *"),
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
	}

	if cfg.ResticRepository == "" {
		return nil, fmt.Errorf("RESTIC_REPOSITORY is required")
	}
	if cfg.ResticPassword == "" {
		return nil, fmt.Errorf("RESTIC_PASSWORD is required")
	}

	return cfg, nil
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
