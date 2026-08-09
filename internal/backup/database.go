package backup

import (
	"context"
	"fmt"
	"io"
	"path"

	"github.com/rs/zerolog/log"
	"github.com/scootec/rdb/internal/dbcmd"
	"github.com/scootec/rdb/internal/docker"
	"github.com/scootec/rdb/internal/restic"
)

// resticBackend is the subset of restic.Runner used by database dumps,
// extracted so the failure path can be unit-tested with a fake.
type resticBackend interface {
	BackupFromStdin(filename string, reader io.Reader, tags []string) (snapshotID string, err error)
	ForgetSnapshot(snapshotID string) error
	TagSnapshot(snapshotID string, tags []string) error
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

	snapshotID, err := rc.BackupFromStdin(stdinFilename, reader, tags)
	if err != nil {
		if stderr := reader.Stderr(); stderr != "" {
			log.Error().Str("container", ctr.Name).Str("db", dbType).Str("stderr", stderr).Msg("database dump stderr")
		}
		if snapshotID != "" {
			discardPartialSnapshot(rc, snapshotID, ctr.Name, dbType)
		}
		return fmt.Errorf("restic backup stdin (%s/%s): %w", ctr.Name, dbType, err)
	}

	// Check if the dump command itself failed
	exitCode, inspectErr := execExitCode(ctx, reader.ExecID())
	if inspectErr != nil {
		log.Warn().Err(inspectErr).Str("container", ctr.Name).Str("db", dbType).Msg("could not inspect exec exit code")
	} else if exitCode != 0 {
		if snapshotID == "" {
			log.Warn().Str("container", ctr.Name).Str("db", dbType).
				Msg("dump failed but its snapshot could not be identified; a truncated snapshot may remain in the repository")
		} else {
			discardPartialSnapshot(rc, snapshotID, ctr.Name, dbType)
		}
		return fmt.Errorf("dump command exited with code %d (%s/%s): %s", exitCode, ctr.Name, dbType, reader.Stderr())
	}

	log.Info().
		Str("container", ctr.Name).
		Str("db", dbType).
		Msg("database dump complete")
	return nil
}

// discardPartialSnapshot removes the snapshot created from a failed dump. If
// deletion fails it falls back to tagging the snapshot partial, which
// excludes it from `rdb snapshots` and restore.
func discardPartialSnapshot(rc resticBackend, snapshotID, ctrName, dbType string) {
	forgetErr := rc.ForgetSnapshot(snapshotID)
	if forgetErr == nil {
		log.Info().Str("container", ctrName).Str("db", dbType).Str("snapshot", snapshotID).
			Msg("deleted snapshot of failed dump")
		return
	}
	log.Warn().Err(forgetErr).Str("container", ctrName).Str("db", dbType).Str("snapshot", snapshotID).
		Msg("could not delete snapshot of failed dump, tagging it partial")
	if err := rc.TagSnapshot(snapshotID, []string{restic.TagPartial}); err != nil {
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
