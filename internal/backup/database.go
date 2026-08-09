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

// dumpDatabase runs a database dump inside the container and pipes it to restic.
func dumpDatabase(ctx context.Context, dc *docker.Client, rc *restic.Runner, ctr docker.ContainerInfo, dbType string) error {
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

	stdinFilename := buildDBFilename(ctr, dbType)
	tags := buildTags(ctr, dbType)

	pr, pw := io.Pipe()

	// Copy exec stdout → pipe writer in background
	errCh := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(pw, reader)
		pw.CloseWithError(copyErr)
		errCh <- copyErr
	}()

	if err := rc.BackupFromStdin(stdinFilename, pr, tags); err != nil {
		if stderr := reader.Stderr(); stderr != "" {
			log.Error().Str("container", ctr.Name).Str("db", dbType).Str("stderr", stderr).Msg("database dump stderr")
		}
		return fmt.Errorf("restic backup stdin (%s/%s): %w", ctr.Name, dbType, err)
	}

	if copyErr := <-errCh; copyErr != nil {
		return fmt.Errorf("reading dump output (%s/%s): %w", ctr.Name, dbType, copyErr)
	}

	// Check if the dump command itself failed
	exitCode, inspectErr := dc.ExecExitCode(ctx, reader.ExecID())
	if inspectErr != nil {
		log.Warn().Err(inspectErr).Str("container", ctr.Name).Str("db", dbType).Msg("could not inspect exec exit code")
	} else if exitCode != 0 {
		stderr := reader.Stderr()
		return fmt.Errorf("dump command exited with code %d (%s/%s): %s", exitCode, ctr.Name, dbType, stderr)
	}

	log.Info().
		Str("container", ctr.Name).
		Str("db", dbType).
		Msg("database dump complete")
	return nil
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
