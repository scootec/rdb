package backup

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/scootec/rdb/internal/docker"
)

// hookExecutor is the subset of docker.Client used to run pre/post-backup
// scripts, extracted so hook semantics can be unit-tested with a fake.
type hookExecutor interface {
	ExecCommand(ctx context.Context, containerID string, cmd []string) error
}

// runWithHooks wraps a container's backups with its pre/post-backup scripts:
//
//   - The pre-backup script runs first; if it fails the backups are skipped,
//     because backing up a stale application export would masquerade as a
//     good backup.
//   - The post-backup script always runs when defined — even when the
//     pre-backup script or the backups failed — so cleanup of export
//     artifacts is never skipped. It runs on a context detached from the
//     run's cancellation (bounded by its own timeout), matching how stopped
//     containers are restarted after a cancelled volume backup.
//   - A failing script marks the container's backup as failed.
func runWithHooks(ctx context.Context, hx hookExecutor, ctr docker.ContainerInfo, backups func(context.Context) error) error {
	var errs []error

	if ctr.PreBackupCmd != "" {
		if err := runHookScript(ctx, hx, ctr, "pre-backup", ctr.PreBackupCmd, ctr.PreBackupTimeout); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) == 0 {
		if err := backups(ctx); err != nil {
			errs = append(errs, err)
		}
	}

	if ctr.PostBackupCmd != "" {
		if err := runHookScript(context.WithoutCancel(ctx), hx, ctr, "post-backup", ctr.PostBackupCmd, ctr.PostBackupTimeout); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// runHookScript runs a single hook script inside the container via `sh -c`,
// bounded by the hook's timeout.
func runHookScript(ctx context.Context, hx hookExecutor, ctr docker.ContainerInfo, phase, script string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = docker.DefaultHookTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	log.Info().Str("container", ctr.Name).Str("phase", phase).Msg("running backup script")
	if err := hx.ExecCommand(ctx, ctr.ID, []string{"sh", "-c", script}); err != nil {
		return fmt.Errorf("%s script on %s: %w", phase, ctr.Name, err)
	}
	log.Info().Str("container", ctr.Name).Str("phase", phase).Msg("backup script complete")
	return nil
}
