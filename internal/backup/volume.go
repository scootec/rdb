package backup

import (
	"context"
	"fmt"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/rs/zerolog/log"
	"github.com/scootec/rdb/internal/docker"
	"github.com/scootec/rdb/internal/restic"
)

// backupVolumes backs up the volumes mounted to the given container.
// It accesses volumes via /var/lib/docker/volumes (mounted read-only into the rdb container).
func backupVolumes(ctx context.Context, dc *docker.Client, rc *restic.Runner, ctr docker.ContainerInfo, excludeBindMounts bool) error {
	mounts := filterMounts(ctr, excludeBindMounts)
	if len(mounts) == 0 {
		log.Info().Str("container", ctr.Name).Msg("no eligible mounts to back up")
		return nil
	}

	if ctr.VolumeStopDuringBackup {
		log.Info().Str("container", ctr.Name).Msg("stopping container before volume backup")
		if err := dc.StopContainer(ctx, ctr.ID); err != nil {
			return fmt.Errorf("stopping container %s: %w", ctr.Name, err)
		}
		defer func() {
			log.Info().Str("container", ctr.Name).Msg("restarting container after volume backup")
			if startErr := dc.StartContainer(ctx, ctr.ID); startErr != nil {
				log.Error().Err(startErr).Str("container", ctr.Name).Msg("failed to restart container")
			}
		}()
	}

	for _, mount := range mounts {
		hostPath := resolveHostPath(mount)
		if hostPath == "" {
			log.Warn().Str("container", ctr.Name).Str("destination", mount.Destination).Msg("cannot determine host path for mount, skipping")
			continue
		}

		tags := buildTags(ctr, "volume")
		log.Info().
			Str("container", ctr.Name).
			Str("path", hostPath).
			Msg("backing up volume")

		if err := rc.BackupDir(hostPath, tags); err != nil {
			return fmt.Errorf("backup volume %s on %s: %w", mount.Destination, ctr.Name, err)
		}
	}

	return nil
}

// filterMounts returns the mounts that should be backed up, applying include/exclude filters.
func filterMounts(ctr docker.ContainerInfo, excludeBindMounts bool) []types.MountPoint {
	var result []types.MountPoint
	for _, m := range ctr.Mounts {
		// Optionally skip host bind mounts
		if excludeBindMounts && m.Type == "bind" {
			continue
		}

		dest := m.Destination

		// Apply include filter
		if len(ctr.VolumesInclude) > 0 && !contains(ctr.VolumesInclude, dest) {
			continue
		}

		// Apply exclude filter
		if contains(ctr.VolumesExclude, dest) {
			continue
		}

		result = append(result, m)
	}
	return result
}

// resolveHostPath returns the path on the rdb container's filesystem for the given mount.
// Named volumes are accessed via /var/lib/docker/volumes/<name>/_data.
// Bind mounts are accessed directly via their Source path.
func resolveHostPath(mount types.MountPoint) string {
	switch mount.Type {
	case "volume":
		if mount.Name != "" {
			return "/var/lib/docker/volumes/" + mount.Name + "/_data"
		}
		return ""
	case "bind":
		return mount.Source
	default:
		return mount.Source
	}
}

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}
