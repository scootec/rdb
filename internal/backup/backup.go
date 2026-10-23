package backup

import (
	"context"
	"errors"
	"fmt"

	"github.com/rs/zerolog/log"
	"github.com/scootec/rdb/internal/config"
	"github.com/scootec/rdb/internal/docker"
	"github.com/scootec/rdb/internal/restic"
)

// Orchestrator discovers containers and runs backups.
type Orchestrator struct {
	cfg *config.Config
	dc  *docker.Client
	rc  *restic.Runner
}

// New creates a new Orchestrator.
func New(cfg *config.Config, dc *docker.Client, rc *restic.Runner) *Orchestrator {
	return &Orchestrator{cfg: cfg, dc: dc, rc: rc}
}

// Run discovers containers and backs them up.
func (o *Orchestrator) Run(ctx context.Context) error {
	containers, err := o.dc.DiscoverContainers(ctx)
	if err != nil {
		return fmt.Errorf("discovering containers: %w", err)
	}

	idle, err := o.dc.DiscoverIdleContainers(ctx)
	if err != nil {
		return fmt.Errorf("discovering containers: %w", err)
	}

	errs := discoveryErrors(len(containers), idle)
	for _, err := range errs {
		log.Error().Err(err).Msg("backup target not available")
	}
	if len(containers) == 0 {
		return errors.Join(errs...)
	}

	log.Info().Int("count", len(containers)).Msg("discovered containers for backup")

	for _, ctr := range containers {
		if err := ctx.Err(); err != nil {
			log.Warn().Err(err).Msg("backup run cancelled, skipping remaining containers")
			return fmt.Errorf("backup run cancelled: %w", err)
		}
		if err := o.backupContainer(ctx, ctr); err != nil {
			log.Error().Err(err).Str("container", ctr.Name).Msg("backup failed")
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("%d container backup(s) failed", len(errs))
	}
	return nil
}

// discoveryErrors reports backup targets that silently would not be backed
// up: no labelled containers at all (wrong Docker socket, labels lost on
// recreation, stack down), or labelled containers that are not running. Each
// counts as a failed backup so the run — and health checks — show it.
func discoveryErrors(running int, idle []docker.IdleContainer) []error {
	var errs []error
	if running == 0 && len(idle) == 0 {
		errs = append(errs, fmt.Errorf("no containers with rdb labels found — nothing was backed up"))
	}
	for _, ctr := range idle {
		errs = append(errs, fmt.Errorf("container %s has rdb labels but is %s — it was not backed up (start it, or remove the container if it is retired)", ctr.Name, ctr.State))
	}
	return errs
}

// Status prints discovered containers and their backup configuration.
func (o *Orchestrator) Status(ctx context.Context) error {
	containers, err := o.dc.DiscoverContainers(ctx)
	if err != nil {
		return fmt.Errorf("discovering containers: %w", err)
	}

	idle, err := o.dc.DiscoverIdleContainers(ctx)
	if err != nil {
		return fmt.Errorf("discovering containers: %w", err)
	}

	if len(containers) == 0 && len(idle) == 0 {
		log.Info().Msg("no containers with rdb labels found")
		return nil
	}

	for _, ctr := range idle {
		log.Warn().Str("container", ctr.Name).Str("state", ctr.State).
			Msg("container has rdb labels but is not running — it will not be backed up")
	}

	for _, ctr := range containers {
		log.Info().
			Str("container", ctr.Name).
			Str("project", ctr.Project).
			Str("service", ctr.Service).
			Bool("volumes", ctr.VolumesEnabled).
			Bool("postgres", ctr.PostgresEnabled).
			Bool("mysql", ctr.MySQLEnabled).
			Bool("mariadb", ctr.MariaDBEnabled).
			Bool("pre-backup", ctr.PreBackupCmd != "").
			Bool("post-backup", ctr.PostBackupCmd != "").
			Msg("container")
	}
	return nil
}

func (o *Orchestrator) backupContainer(ctx context.Context, ctr docker.ContainerInfo) error {
	log.Info().Str("container", ctr.Name).Msg("backing up container")

	return runWithHooks(ctx, o.dc, ctr, func(ctx context.Context) error {
		return runBackupSteps(ctx, ctr,
			func(ctx context.Context, dbType string) error {
				return dumpDatabase(ctx, o.dc, o.rc, ctr, dbType)
			},
			func(ctx context.Context) error {
				return backupVolumes(ctx, o.dc, o.rc, ctr, o.cfg.ExcludeBindMounts)
			})
	})
}

// runBackupSteps runs a container's enabled database dumps, then its volume
// backup, stopping at the first failure. Dumps go first because they need
// the database up and accepting connections, while the volume backup may
// stop and restart the container (rdb.volumes.stop-during-backup) — a dump
// started right after that restart would race the database's startup.
func runBackupSteps(ctx context.Context, ctr docker.ContainerInfo, dump func(context.Context, string) error, volumes func(context.Context) error) error {
	dbs := []struct {
		enabled bool
		dbType  string
	}{
		{ctr.PostgresEnabled, "postgres"},
		{ctr.MySQLEnabled, "mysql"},
		{ctr.MariaDBEnabled, "mariadb"},
	}
	for _, db := range dbs {
		if !db.enabled {
			continue
		}
		if err := dump(ctx, db.dbType); err != nil {
			return err
		}
	}

	if ctr.VolumesEnabled {
		return volumes(ctx)
	}
	return nil
}
