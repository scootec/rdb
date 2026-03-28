package restore

import (
	"context"
	"fmt"

	"github.com/rs/zerolog/log"
	"github.com/scootec/rdb/internal/restic"
)

// restoreVolume restores a volume snapshot to its original paths.
func (r *Restorer) restoreVolume(ctx context.Context, snap restic.Snapshot) error {
	log.Warn().Msg("restoring volume snapshot — files will be overwritten at their original paths")

	if err := r.rc.Restore(snap.ID, "/"); err != nil {
		return fmt.Errorf("restic restore: %w", err)
	}

	log.Info().Str("snapshot", snap.ShortID).Msg("volume restore complete")
	return nil
}
