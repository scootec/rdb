package restore

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/rs/zerolog/log"
	"github.com/scootec/rdb/internal/dbcmd"
	"github.com/scootec/rdb/internal/restic"
)

// restoreDatabase restores a database snapshot by piping the SQL dump into the
// target container, or writing it to a file if OutputPath is set.
func (r *Restorer) restoreDatabase(ctx context.Context, snap restic.Snapshot, dbType string, opts Options) error {
	dumpPath := snap.Paths[0]

	reader, err := r.rc.Dump(ctx, snap.ID, dumpPath)
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

	cmd, extraEnv, err := dbcmd.ImportCmd(ctr.Env, dbType)
	if err != nil {
		return fmt.Errorf("building import command for %s: %w", ctr.Name, err)
	}

	log.Warn().
		Str("container", ctr.Name).
		Str("db", dbType).
		Msg("restoring database — objects in the backup are dropped and recreated from the backup; objects created after the backup are left in place")

	// The filter strips statements the dump expects to fail (see
	// dbcmd.ImportFilter); closing it releases the filter goroutine if the
	// import stops before draining the stream.
	filtered := dbcmd.ImportFilter(reader, ctr.Env, dbType)
	if closer, ok := filtered.(io.Closer); ok {
		defer closer.Close()
	}

	if err := r.dc.ExecImport(ctx, ctr.ID, cmd, extraEnv, filtered); err != nil {
		return fmt.Errorf("importing %s dump into %s: %w", dbType, ctr.Name, err)
	}

	log.Info().
		Str("container", ctr.Name).
		Str("db", dbType).
		Msg("database restore complete")
	return nil
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
