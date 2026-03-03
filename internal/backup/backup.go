package backup

import (
	"context"
	"fmt"

	"github.com/rs/zerolog/log"
	"github.com/shanescott/rdb/internal/config"
	"github.com/shanescott/rdb/internal/docker"
	"github.com/shanescott/rdb/internal/restic"
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

	if len(containers) == 0 {
		log.Warn().Msg("no containers with rdb labels found")
		return nil
	}

	log.Info().Int("count", len(containers)).Msg("discovered containers for backup")

	var errs []error
	for _, ctr := range containers {
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

// Status prints discovered containers and their backup configuration.
func (o *Orchestrator) Status(ctx context.Context) error {
	containers, err := o.dc.DiscoverContainers(ctx)
	if err != nil {
		return fmt.Errorf("discovering containers: %w", err)
	}

	if len(containers) == 0 {
		log.Info().Msg("no containers with rdb labels found")
		return nil
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
			Msg("container")
	}
	return nil
}

func (o *Orchestrator) backupContainer(ctx context.Context, ctr docker.ContainerInfo) error {
	log.Info().Str("container", ctr.Name).Msg("backing up container")

	if ctr.VolumesEnabled {
		if err := backupVolumes(ctx, o.dc, o.rc, ctr, o.cfg.ExcludeBindMounts); err != nil {
			return err
		}
	}

	if ctr.PostgresEnabled {
		if err := dumpDatabase(ctx, o.dc, o.rc, ctr, "postgres"); err != nil {
			return err
		}
	}

	if ctr.MySQLEnabled {
		if err := dumpDatabase(ctx, o.dc, o.rc, ctr, "mysql"); err != nil {
			return err
		}
	}

	if ctr.MariaDBEnabled {
		if err := dumpDatabase(ctx, o.dc, o.rc, ctr, "mariadb"); err != nil {
			return err
		}
	}

	return nil
}
