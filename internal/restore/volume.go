package restore

import (
	"context"
	"errors"
	"fmt"

	"github.com/rs/zerolog/log"
	"github.com/scootec/rdb/internal/docker"
	"github.com/scootec/rdb/internal/restic"
)

// runningAction is what to do when the owning container of a volume snapshot
// is running at restore time.
type runningAction int

const (
	actionRefuse runningAction = iota
	actionStopRestart
	actionProceed
)

// decideRunningAction maps the --stop/--force flags to an action for a running
// owning container. --stop wins over --force; with neither, refuse.
func decideRunningAction(force, stop bool) runningAction {
	switch {
	case stop:
		return actionStopRestart
	case force:
		return actionProceed
	default:
		return actionRefuse
	}
}

// restoreVolume restores a volume snapshot to its original paths.
// The restore is scoped to the snapshot's backed-up paths, and the owning
// container (located via the snapshot's project/service tags) must not be
// running unless --force or --stop is given.
func (r *Restorer) restoreVolume(ctx context.Context, snap restic.Snapshot, opts Options) error {
	if len(snap.Paths) == 0 {
		return fmt.Errorf("snapshot %s has no recorded paths — cannot scope restore", snap.ShortID)
	}

	restart, err := r.guardOwningContainer(ctx, snap, opts)
	if err != nil {
		return err
	}

	log.Warn().
		Strs("paths", snap.Paths).
		Msg("restoring volume snapshot — files will be overwritten at their original paths")

	restoreErr := r.rc.Restore(snap.ID, "/", snap.Paths)

	// Restart a container stopped via --stop even if the restore failed,
	// so the stack is not left down.
	if restart != nil {
		restart()
	}

	if restoreErr != nil {
		return fmt.Errorf("restic restore: %w", restoreErr)
	}

	log.Info().Str("snapshot", snap.ShortID).Msg("volume restore complete")
	return nil
}

// guardOwningContainer refuses to restore over a running container unless
// --force or --stop is given. With --stop it stops the container and returns a
// restart function the caller must invoke after the restore.
func (r *Restorer) guardOwningContainer(ctx context.Context, snap restic.Snapshot, opts Options) (restart func(), err error) {
	project := parseTagValue(snap, "project")
	service := parseTagValue(snap, "service")
	if service == "" {
		log.Warn().
			Str("snapshot", snap.ShortID).
			Msg("snapshot has no service tag — cannot check whether the owning container is running")
		return nil, nil
	}

	ctr, err := r.dc.FindContainer(ctx, project, service)
	if errors.Is(err, docker.ErrContainerNotFound) {
		log.Debug().Str("service", service).Msg("owning container is not running — safe to restore")
		return nil, nil
	}
	if err != nil {
		if opts.Force {
			log.Warn().Err(err).Msg("could not determine owning container state — proceeding due to --force")
			return nil, nil
		}
		return nil, fmt.Errorf("checking owning container: %w (pass --force to restore anyway)", err)
	}

	switch decideRunningAction(opts.Force, opts.Stop) {
	case actionStopRestart:
		log.Info().Str("container", ctr.Name).Msg("stopping container before volume restore")
		if err := r.dc.StopContainer(ctx, ctr.ID); err != nil {
			return nil, fmt.Errorf("stopping container %s: %w", ctr.Name, err)
		}
		return func() {
			log.Info().Str("container", ctr.Name).Msg("restarting container after volume restore")
			if startErr := r.dc.StartContainer(ctx, ctr.ID); startErr != nil {
				log.Error().Err(startErr).Str("container", ctr.Name).Msg("failed to restart container")
			}
		}, nil
	case actionProceed:
		log.Warn().Str("container", ctr.Name).Msg("owning container is running — restoring anyway due to --force")
		return nil, nil
	default:
		return nil, fmt.Errorf(
			"container %s (project=%q service=%q) is running — stop it first, pass --stop to stop and restart it around the restore, or --force to restore anyway",
			ctr.Name, project, service)
	}
}
