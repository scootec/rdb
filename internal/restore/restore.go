package restore

import (
	"context"
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/scootec/rdb/internal/docker"
	"github.com/scootec/rdb/internal/restic"
)

// Options configures a restore operation.
type Options struct {
	SnapshotID string
	OutputPath string // non-empty = dump SQL to file instead of importing
	Force      bool   // volume restore: proceed even if the owning container is running
	Stop       bool   // volume restore: stop the owning container, restore, then restart it
}

// Restorer handles restoring data from restic snapshots.
type Restorer struct {
	dc *docker.Client
	rc *restic.Runner
}

// New creates a new Restorer.
func New(dc *docker.Client, rc *restic.Runner) *Restorer {
	return &Restorer{dc: dc, rc: rc}
}

// Restore restores data from the given snapshot.
func (r *Restorer) Restore(ctx context.Context, opts Options) error {
	snaps, err := r.rc.SnapshotsByID(opts.SnapshotID)
	if err != nil {
		return fmt.Errorf("looking up snapshot %s: %w", opts.SnapshotID, err)
	}
	if len(snaps) == 0 {
		return fmt.Errorf("snapshot %s not found", opts.SnapshotID)
	}

	snap := snaps[0]
	dbType := snapshotDBType(snap)

	if dbType != "" {
		log.Info().
			Str("snapshot", snap.ShortID).
			Str("db", dbType).
			Msg("restoring database snapshot")
		return r.restoreDatabase(ctx, snap, dbType, opts)
	}

	if hasTag(snap, "volume") {
		log.Info().
			Str("snapshot", snap.ShortID).
			Msg("restoring volume snapshot")
		return r.restoreVolume(ctx, snap, opts)
	}

	return fmt.Errorf("snapshot %s has no recognized rdb type tags", snap.ShortID)
}

// snapshotDBType returns the database type from snapshot tags, or "" if not a database snapshot.
func snapshotDBType(snap restic.Snapshot) string {
	for _, tag := range snap.Tags {
		switch tag {
		case "postgres", "mysql", "mariadb":
			return tag
		}
	}
	return ""
}

// hasTag checks if a snapshot has a specific tag.
func hasTag(snap restic.Snapshot, tag string) bool {
	for _, t := range snap.Tags {
		if t == tag {
			return true
		}
	}
	return false
}

// parseTagValue extracts the value from a "key:value" tag. Returns "" if not found.
func parseTagValue(snap restic.Snapshot, prefix string) string {
	for _, t := range snap.Tags {
		if strings.HasPrefix(t, prefix+":") {
			return strings.TrimPrefix(t, prefix+":")
		}
	}
	return ""
}
