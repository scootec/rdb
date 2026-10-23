package backup

import (
	"context"
	"fmt"
	"io"
	"path"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/scootec/rdb/internal/dbcmd"
	"github.com/scootec/rdb/internal/docker"
	"github.com/scootec/rdb/internal/restic"
)

// resticBackend is the subset of restic.Runner used by database dumps,
// extracted so the failure path can be unit-tested with a fake.
type resticBackend interface {
	BackupFromStdin(ctx context.Context, filename string, reader io.Reader, tags []string) (snapshotID string, err error)
	ForgetSnapshot(ctx context.Context, snapshotID string) error
	TagSnapshot(ctx context.Context, snapshotID string, tags []string) error
}

// dumpStream is the subset of docker.ExecDumpReader that streamDumpToRestic
// consumes.
type dumpStream interface {
	io.Reader
	Stderr() string
	ExecID() string
}

// dumpDatabase runs a database dump inside the container and pipes it to restic.
func dumpDatabase(ctx context.Context, dc *docker.Client, rc resticBackend, ctr docker.ContainerInfo, dbType string) error {
	cmd, extraEnv, err := dbcmd.DumpCmd(ctr.Env, dbType)
	if err != nil {
		return err
	}

	log.Info().
		Str("container", ctr.Name).
		Str("db", dbType).
		Msg("starting database dump")

	reader, _, err := dc.ExecDump(ctx, ctr.ID, cmd, extraEnv)
	if err != nil {
		return fmt.Errorf("exec dump %s on %s: %w", dbType, ctr.Name, err)
	}
	defer reader.Close()

	return streamDumpToRestic(ctx, reader, dc.ExecExitCode, rc, ctr, dbType)
}

// streamDumpToRestic streams the dump command's stdout into restic, then
// verifies the command's exit code. Restic commits its snapshot before the
// exit code is knowable, so a dump that dies mid-stream leaves a truncated
// snapshot in the repository; on failure the snapshot is deleted — or, if
// deletion fails, tagged partial — so it can never masquerade as a good
// backup.
func streamDumpToRestic(ctx context.Context, reader dumpStream, execExitCode func(context.Context, string) (int, error), rc resticBackend, ctr docker.ContainerInfo, dbType string) error {
	stdinFilename := buildDBFilename(ctr, dbType)
	tags := buildTags(ctr, dbType)

	snapshotID, err := rc.BackupFromStdin(ctx, stdinFilename, reader, tags)
	if err != nil {
		if stderr := reader.Stderr(); stderr != "" {
			log.Error().Str("container", ctr.Name).Str("db", dbType).Str("stderr", stderr).Msg("database dump stderr")
		}
		if snapshotID != "" {
			discardPartialSnapshot(ctx, rc, snapshotID, ctr.Name, dbType)
		}
		return fmt.Errorf("restic backup stdin (%s/%s): %w", ctr.Name, dbType, err)
	}

	// Check if the dump command itself failed. A dump whose exit code cannot
	// be determined is treated as failed: a truncated dump must never be kept
	// as a good backup. The check runs detached from the run's cancellation
	// so a timeout firing just after restic finished does not discard a good
	// dump unchecked.
	inspectCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), exitCodeTimeout)
	defer cancel()
	exitCode, inspectErr := execExitCode(inspectCtx, reader.ExecID())
	if inspectErr != nil || exitCode != 0 {
		if snapshotID == "" {
			log.Warn().Str("container", ctr.Name).Str("db", dbType).
				Msg("dump failed but its snapshot could not be identified; a truncated snapshot may remain in the repository")
		} else {
			discardPartialSnapshot(ctx, rc, snapshotID, ctr.Name, dbType)
		}
		if inspectErr != nil {
			return fmt.Errorf("could not determine dump exit code (%s/%s): %w", ctr.Name, dbType, inspectErr)
		}
		return fmt.Errorf("dump command exited with code %d (%s/%s): %s", exitCode, ctr.Name, dbType, reader.Stderr())
	}

	log.Info().
		Str("container", ctr.Name).
		Str("db", dbType).
		Msg("database dump complete")
	return nil
}

// exitCodeTimeout bounds the detached lookup of a finished dump's exit code.
const exitCodeTimeout = time.Minute

// discardCleanupTimeout bounds the detached cleanup of a failed dump's snapshot.
const discardCleanupTimeout = 5 * time.Minute

// discardPartialSnapshot removes the snapshot created from a failed dump. If
// deletion fails it falls back to tagging the snapshot partial, which
// excludes it from `rdb snapshots` and restore. Cleanup must still happen
// when the dump failed because the run was cancelled or timed out, so it
// runs on a context detached from the run's cancellation.
func discardPartialSnapshot(ctx context.Context, rc resticBackend, snapshotID, ctrName, dbType string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), discardCleanupTimeout)
	defer cancel()

	forgetErr := rc.ForgetSnapshot(ctx, snapshotID)
	if forgetErr == nil {
		log.Info().Str("container", ctrName).Str("db", dbType).Str("snapshot", snapshotID).
			Msg("deleted snapshot of failed dump")
		return
	}
	log.Warn().Err(forgetErr).Str("container", ctrName).Str("db", dbType).Str("snapshot", snapshotID).
		Msg("could not delete snapshot of failed dump, tagging it partial")
	if err := rc.TagSnapshot(ctx, snapshotID, []string{restic.TagPartial}); err != nil {
		log.Error().Err(err).Str("container", ctrName).Str("db", dbType).Str("snapshot", snapshotID).
			Msg("could not tag snapshot of failed dump; it may appear as a valid backup")
	}
}

func buildDBFilename(ctr docker.ContainerInfo, dbType string) string {
	segments := []string{"databases"}
	if ctr.Project != "" {
		segments = append(segments, ctr.Project)
	}
	service := ctr.Service
	if service == "" {
		service = ctr.Name
	}
	segments = append(segments, service, dbType, "all_databases.sql")
	return path.Join(segments...)
}

func buildTags(ctr docker.ContainerInfo, component string) []string {
	tags := []string{"rdb", component}
	if ctr.Project != "" {
		tags = append(tags, "project:"+ctr.Project)
	}
	service := ctr.Service
	if service == "" {
		service = ctr.Name
	}
	tags = append(tags, "service:"+service)
	return tags
}
