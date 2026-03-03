package backup

import (
	"context"
	"fmt"
	"io"
	"path"

	"github.com/rs/zerolog/log"
	"github.com/shanescott/rdb/internal/docker"
	"github.com/shanescott/rdb/internal/restic"
)

// dumpDatabase runs a database dump inside the container and pipes it to restic.
func dumpDatabase(ctx context.Context, dc *docker.Client, rc *restic.Runner, ctr docker.ContainerInfo, dbType string) error {
	var cmd []string
	var extraEnv []string
	var user, password string

	switch dbType {
	case "postgres":
		user = ctr.Env["POSTGRES_USER"]
		if user == "" {
			user = "postgres"
		}
		password = ctr.Env["POSTGRES_PASSWORD"]
		cmd = []string{"pg_dumpall", "-U", user}
		if password != "" {
			extraEnv = []string{"PGPASSWORD=" + password}
		}

	case "mysql":
		password = ctr.Env["MYSQL_ROOT_PASSWORD"]
		user = "root"
		if password == "" {
			user = ctr.Env["MYSQL_USER"]
			password = ctr.Env["MYSQL_PASSWORD"]
		}
		cmd = []string{
			"mysqldump",
			"--user=" + user,
			"--all-databases",
			"--single-transaction",
			"--compact",
			"--force",
		}
		if password != "" {
			extraEnv = []string{"MYSQL_PWD=" + password}
		}

	case "mariadb":
		password = ctr.Env["MARIADB_ROOT_PASSWORD"]
		user = "root"
		if password == "" {
			user = ctr.Env["MARIADB_USER"]
			password = ctr.Env["MARIADB_PASSWORD"]
		}
		cmd = []string{
			"mariadb-dump",
			"--user=" + user,
			"--all-databases",
			"--single-transaction",
			"--compact",
			"--force",
		}
		if password != "" {
			extraEnv = []string{"MYSQL_PWD=" + password}
		}

	default:
		return fmt.Errorf("unknown database type: %s", dbType)
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
		return fmt.Errorf("restic backup stdin (%s/%s): %w", ctr.Name, dbType, err)
	}

	if copyErr := <-errCh; copyErr != nil {
		return fmt.Errorf("reading dump output (%s/%s): %w", ctr.Name, dbType, copyErr)
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
