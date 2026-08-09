package config

import (
	"os"
	"testing"
	"time"
)

func TestEnvOrDefault(t *testing.T) {
	t.Run("set value wins", func(t *testing.T) {
		t.Setenv("RDB_TEST_STR", "custom")
		if got := envOrDefault("RDB_TEST_STR", "def"); got != "custom" {
			t.Errorf("envOrDefault() = %q, want %q", got, "custom")
		}
	})

	t.Run("unset falls back to default", func(t *testing.T) {
		if got := envOrDefault("RDB_TEST_UNSET", "def"); got != "def" {
			t.Errorf("envOrDefault() = %q, want %q", got, "def")
		}
	})

	t.Run("empty value falls back to default", func(t *testing.T) {
		t.Setenv("RDB_TEST_EMPTY", "")
		if got := envOrDefault("RDB_TEST_EMPTY", "def"); got != "def" {
			t.Errorf("envOrDefault() = %q, want %q", got, "def")
		}
	})
}

func TestEnvBool(t *testing.T) {
	tests := []struct {
		name  string
		value string
		set   bool
		def   bool
		want  bool
	}{
		{"unset returns default true", "", false, true, true},
		{"unset returns default false", "", false, false, false},
		{"true", "true", true, false, true},
		{"1", "1", true, false, true},
		{"TRUE", "TRUE", true, false, true},
		{"false", "false", true, true, false},
		{"0", "0", true, true, false},
		// strconv.ParseBool rejects "yes"; invalid values fall back to default.
		{"yes is invalid and falls back to default", "yes", true, false, false},
		{"garbage falls back to default true", "banana", true, true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv("RDB_TEST_BOOL", tt.value)
			}
			if got := envBool("RDB_TEST_BOOL", tt.def); got != tt.want {
				t.Errorf("envBool(%q, %v) = %v, want %v", tt.value, tt.def, got, tt.want)
			}
		})
	}
}

func TestEnvInt(t *testing.T) {
	tests := []struct {
		name  string
		value string
		set   bool
		def   int
		want  int
	}{
		{"unset returns default", "", false, 7, 7},
		{"valid int", "42", true, 7, 42},
		{"zero", "0", true, 7, 0},
		{"negative", "-3", true, 7, -3},
		{"garbage falls back to default", "many", true, 7, 7},
		{"float falls back to default", "1.5", true, 7, 7},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv("RDB_TEST_INT", tt.value)
			}
			if got := envInt("RDB_TEST_INT", tt.def); got != tt.want {
				t.Errorf("envInt(%q, %d) = %d, want %d", tt.value, tt.def, got, tt.want)
			}
		})
	}
}

func TestEnvDuration(t *testing.T) {
	tests := []struct {
		name  string
		value string
		set   bool
		def   time.Duration
		want  time.Duration
	}{
		{"unset returns default", "", false, time.Hour, time.Hour},
		{"valid duration", "90m", true, time.Hour, 90 * time.Minute},
		{"seconds", "30s", true, time.Hour, 30 * time.Second},
		{"garbage falls back to default", "soon", true, time.Hour, time.Hour},
		{"bare number falls back to default", "5", true, time.Hour, time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv("RDB_TEST_DURATION", tt.value)
			}
			if got := envDuration("RDB_TEST_DURATION", tt.def); got != tt.want {
				t.Errorf("envDuration(%q, %v) = %v, want %v", tt.value, tt.def, got, tt.want)
			}
		})
	}
}

func TestLoad(t *testing.T) {
	// Clear every variable Load reads so ambient environment cannot leak in.
	// t.Setenv registers restoration of the original value; the subsequent
	// Unsetenv makes the variable truly unset (set-but-empty is meaningful
	// for RDB_MAINTENANCE_CRON).
	clearEnv := func(t *testing.T) {
		for _, key := range []string{
			"RESTIC_REPOSITORY", "RESTIC_PASSWORD",
			"RDB_CRON_SCHEDULE", "RDB_MAINTENANCE_CRON", "RDB_LOG_LEVEL",
			"RDB_INCLUDE_PROJECT_NAME", "RDB_EXCLUDE_BIND_MOUNTS", "RDB_SKIP_INIT",
			"RDB_RESTIC_HOSTNAME",
			"RESTIC_KEEP_DAILY", "RESTIC_KEEP_WEEKLY", "RESTIC_KEEP_MONTHLY",
			"RESTIC_KEEP_YEARLY", "RESTIC_KEEP_LAST", "RESTIC_KEEP_HOURLY",
			"RESTIC_KEEP_WITHIN",
			"RDB_HEALTHCHECK_URL", "RDB_MAINTENANCE_HEALTHCHECK_URL",
			"RDB_STATE_FILE", "RDB_HEALTH_GRACE",
			"RDB_BACKUP_TIMEOUT", "RDB_SHUTDOWN_TIMEOUT",
		} {
			t.Setenv(key, "")
			os.Unsetenv(key)
		}
	}

	t.Run("missing RESTIC_REPOSITORY errors", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("RESTIC_PASSWORD", "pw")
		if _, err := Load(); err == nil {
			t.Fatal("Load() expected error for missing RESTIC_REPOSITORY, got nil")
		}
	})

	t.Run("missing RESTIC_PASSWORD errors", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("RESTIC_REPOSITORY", "/repo")
		if _, err := Load(); err == nil {
			t.Fatal("Load() expected error for missing RESTIC_PASSWORD, got nil")
		}
	})

	t.Run("defaults applied when only required vars set", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("RESTIC_REPOSITORY", "/repo")
		t.Setenv("RESTIC_PASSWORD", "pw")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() unexpected error: %v", err)
		}
		if cfg.CronSchedule != "0 2 * * *" {
			t.Errorf("CronSchedule = %q, want %q", cfg.CronSchedule, "0 2 * * *")
		}
		if cfg.MaintenanceCron != "0 4 * * 0" {
			t.Errorf("MaintenanceCron = %q, want %q", cfg.MaintenanceCron, "0 4 * * 0")
		}
		if !cfg.MaintenanceEnabled() {
			t.Error("MaintenanceEnabled() = false, want true by default")
		}
		if cfg.ResticHostname != "rdb" {
			t.Errorf("ResticHostname = %q, want %q", cfg.ResticHostname, "rdb")
		}
		if cfg.LogLevel != "info" {
			t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "info")
		}
		if cfg.IncludeProjectName || cfg.ExcludeBindMounts || cfg.SkipInit {
			t.Errorf("bool defaults = %v/%v/%v, want all false",
				cfg.IncludeProjectName, cfg.ExcludeBindMounts, cfg.SkipInit)
		}
		if cfg.KeepDaily != 7 || cfg.KeepWeekly != 4 || cfg.KeepMonthly != 12 || cfg.KeepYearly != 3 {
			t.Errorf("retention defaults = %d/%d/%d/%d, want 7/4/12/3",
				cfg.KeepDaily, cfg.KeepWeekly, cfg.KeepMonthly, cfg.KeepYearly)
		}
		if cfg.KeepLast != 0 || cfg.KeepHourly != 0 || cfg.KeepWithin != "" {
			t.Errorf("KeepLast/KeepHourly/KeepWithin = %d/%d/%q, want 0/0/\"\"",
				cfg.KeepLast, cfg.KeepHourly, cfg.KeepWithin)
		}
		if cfg.HealthcheckURL != "" || cfg.MaintenanceHealthcheckURL != "" {
			t.Errorf("healthcheck URLs = %q/%q, want both empty by default",
				cfg.HealthcheckURL, cfg.MaintenanceHealthcheckURL)
		}
		if cfg.StateFile != "/tmp/rdb-status" {
			t.Errorf("StateFile = %q, want %q", cfg.StateFile, "/tmp/rdb-status")
		}
		if cfg.HealthGrace != time.Hour {
			t.Errorf("HealthGrace = %v, want %v", cfg.HealthGrace, time.Hour)
		}
		if cfg.BackupTimeout != 2*time.Hour {
			t.Errorf("BackupTimeout = %v, want %v", cfg.BackupTimeout, 2*time.Hour)
		}
		if cfg.ShutdownTimeout != 5*time.Minute {
			t.Errorf("ShutdownTimeout = %v, want %v", cfg.ShutdownTimeout, 5*time.Minute)
		}
	})

	t.Run("explicit values override defaults", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("RESTIC_REPOSITORY", "s3:s3.example.com/bucket")
		t.Setenv("RESTIC_PASSWORD", "pw")
		t.Setenv("RDB_CRON_SCHEDULE", "*/15 * * * *")
		t.Setenv("RDB_MAINTENANCE_CRON", "30 3 * * 6")
		t.Setenv("RDB_RESTIC_HOSTNAME", "my-backup-host")
		t.Setenv("RDB_LOG_LEVEL", "debug")
		t.Setenv("RDB_INCLUDE_PROJECT_NAME", "true")
		t.Setenv("RDB_EXCLUDE_BIND_MOUNTS", "1")
		t.Setenv("RDB_SKIP_INIT", "true")
		t.Setenv("RESTIC_KEEP_DAILY", "14")
		t.Setenv("RESTIC_KEEP_LAST", "5")
		t.Setenv("RESTIC_KEEP_WITHIN", "30d")
		t.Setenv("RDB_HEALTHCHECK_URL", "https://hc.example.com/ping/uuid-1")
		t.Setenv("RDB_MAINTENANCE_HEALTHCHECK_URL", "https://hc.example.com/ping/uuid-2")
		t.Setenv("RDB_STATE_FILE", "/var/lib/rdb/status")
		t.Setenv("RDB_HEALTH_GRACE", "2h")
		t.Setenv("RDB_BACKUP_TIMEOUT", "45m")
		t.Setenv("RDB_SHUTDOWN_TIMEOUT", "90s")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() unexpected error: %v", err)
		}
		if cfg.ResticRepository != "s3:s3.example.com/bucket" {
			t.Errorf("ResticRepository = %q", cfg.ResticRepository)
		}
		if cfg.CronSchedule != "*/15 * * * *" {
			t.Errorf("CronSchedule = %q", cfg.CronSchedule)
		}
		if cfg.LogLevel != "debug" {
			t.Errorf("LogLevel = %q", cfg.LogLevel)
		}
		if !cfg.IncludeProjectName || !cfg.ExcludeBindMounts || !cfg.SkipInit {
			t.Errorf("bools = %v/%v/%v, want all true",
				cfg.IncludeProjectName, cfg.ExcludeBindMounts, cfg.SkipInit)
		}
		if cfg.KeepDaily != 14 || cfg.KeepLast != 5 || cfg.KeepWithin != "30d" {
			t.Errorf("KeepDaily/KeepLast/KeepWithin = %d/%d/%q, want 14/5/30d",
				cfg.KeepDaily, cfg.KeepLast, cfg.KeepWithin)
		}
		if cfg.MaintenanceCron != "30 3 * * 6" {
			t.Errorf("MaintenanceCron = %q, want %q", cfg.MaintenanceCron, "30 3 * * 6")
		}
		if cfg.ResticHostname != "my-backup-host" {
			t.Errorf("ResticHostname = %q, want %q", cfg.ResticHostname, "my-backup-host")
		}
		if cfg.HealthcheckURL != "https://hc.example.com/ping/uuid-1" {
			t.Errorf("HealthcheckURL = %q", cfg.HealthcheckURL)
		}
		if cfg.MaintenanceHealthcheckURL != "https://hc.example.com/ping/uuid-2" {
			t.Errorf("MaintenanceHealthcheckURL = %q", cfg.MaintenanceHealthcheckURL)
		}
		if cfg.StateFile != "/var/lib/rdb/status" {
			t.Errorf("StateFile = %q", cfg.StateFile)
		}
		if cfg.HealthGrace != 2*time.Hour {
			t.Errorf("HealthGrace = %v, want 2h", cfg.HealthGrace)
		}
		if cfg.BackupTimeout != 45*time.Minute {
			t.Errorf("BackupTimeout = %v, want 45m", cfg.BackupTimeout)
		}
		if cfg.ShutdownTimeout != 90*time.Second {
			t.Errorf("ShutdownTimeout = %v, want 90s", cfg.ShutdownTimeout)
		}
	})

	t.Run("empty RDB_MAINTENANCE_CRON disables maintenance", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("RESTIC_REPOSITORY", "/repo")
		t.Setenv("RESTIC_PASSWORD", "pw")
		t.Setenv("RDB_MAINTENANCE_CRON", "")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() unexpected error: %v", err)
		}
		if cfg.MaintenanceEnabled() {
			t.Error("MaintenanceEnabled() = true, want false for empty value")
		}
	})

	t.Run("RDB_MAINTENANCE_CRON=off disables maintenance", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("RESTIC_REPOSITORY", "/repo")
		t.Setenv("RESTIC_PASSWORD", "pw")
		t.Setenv("RDB_MAINTENANCE_CRON", "OFF")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() unexpected error: %v", err)
		}
		if cfg.MaintenanceEnabled() {
			t.Error("MaintenanceEnabled() = true, want false for 'OFF'")
		}
	})
}
