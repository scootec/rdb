package restore

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/rs/zerolog/log"
	"github.com/scootec/rdb/internal/docker"
	"github.com/scootec/rdb/internal/restic"
)

// restoreDatabase restores a database snapshot by piping the SQL dump into the
// target container, or writing it to a file if OutputPath is set.
func (r *Restorer) restoreDatabase(ctx context.Context, snap restic.Snapshot, dbType string, opts Options) error {
	dumpPath := snap.Paths[0]

	reader, err := r.rc.Dump(snap.ID, dumpPath)
	if err != nil {
		return fmt.Errorf("restic dump: %w", err)
	}
	defer reader.Close()

	// Dump to file mode
	if opts.OutputPath != "" {
		return dumpToFile(reader, opts.OutputPath)
	}

	// Import into running container
	project := parseTagValue(snap, "project")
	service := parseTagValue(snap, "service")
	if service == "" {
		return fmt.Errorf("snapshot %s has no service tag — cannot locate target container", snap.ShortID)
	}

	ctr, err := r.dc.FindContainer(ctx, project, service)
	if err != nil {
		return fmt.Errorf("finding target container: %w (use --output to extract the dump to a file instead)", err)
	}

	cmd, extraEnv := buildImportCmd(ctr, dbType)

	log.Warn().
		Str("container", ctr.Name).
		Str("db", dbType).
		Msg("restoring database — this will overwrite existing data")

	if err := r.dc.ExecImport(ctx, ctr.ID, cmd, extraEnv, reader); err != nil {
		return fmt.Errorf("importing %s dump into %s: %w", dbType, ctr.Name, err)
	}

	log.Info().
		Str("container", ctr.Name).
		Str("db", dbType).
		Msg("database restore complete")
	return nil
}

// buildImportCmd returns the command and environment variables for importing
// a SQL dump into a container, matching the credential logic in backup/database.go.
func buildImportCmd(ctr *docker.ContainerInfo, dbType string) (cmd []string, extraEnv []string) {
	switch dbType {
	case "postgres":
		user := ctr.Env["POSTGRES_USER"]
		if user == "" {
			user = "postgres"
		}
		password := ctr.Env["POSTGRES_PASSWORD"]
		cmd = []string{"psql", "-U", user}
		if password != "" {
			extraEnv = []string{"PGPASSWORD=" + password}
		}

	case "mysql":
		password := ctr.Env["MYSQL_ROOT_PASSWORD"]
		user := "root"
		if password == "" {
			user = ctr.Env["MYSQL_USER"]
			password = ctr.Env["MYSQL_PASSWORD"]
		}
		cmd = []string{"mysql", "--user=" + user}
		if password != "" {
			extraEnv = []string{"MYSQL_PWD=" + password}
		}

	case "mariadb":
		password := ctr.Env["MARIADB_ROOT_PASSWORD"]
		user := "root"
		if password == "" {
			user = ctr.Env["MARIADB_USER"]
			password = ctr.Env["MARIADB_PASSWORD"]
		}
		cmd = []string{"mariadb", "--user=" + user}
		if password != "" {
			extraEnv = []string{"MYSQL_PWD=" + password}
		}
	}
	return cmd, extraEnv
}

// dumpToFile writes the reader contents to the given file path.
func dumpToFile(reader io.Reader, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating output file: %w", err)
	}
	defer f.Close()

	n, err := io.Copy(f, reader)
	if err != nil {
		return fmt.Errorf("writing dump to file: %w", err)
	}

	log.Info().Str("path", path).Int64("bytes", n).Msg("database dump written to file")
	return nil
}
