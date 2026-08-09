package backup

import (
	"reflect"
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/mount"
	"github.com/scootec/rdb/internal/docker"
)

func mountPoint(mType, name, source, dest string) types.MountPoint {
	return types.MountPoint{
		Type:        mount.Type(mType),
		Name:        name,
		Source:      source,
		Destination: dest,
	}
}

func destinations(mounts []types.MountPoint) []string {
	var out []string
	for _, m := range mounts {
		out = append(out, m.Destination)
	}
	return out
}

func TestFilterMounts(t *testing.T) {
	dataVol := mountPoint("volume", "app_data", "", "/data")
	configVol := mountPoint("volume", "app_config", "", "/config")
	bind := mountPoint("bind", "", "/host/logs", "/logs")

	tests := []struct {
		name              string
		ctr               docker.ContainerInfo
		excludeBindMounts bool
		want              []string
	}{
		{
			name: "no filters returns all mounts",
			ctr:  docker.ContainerInfo{Mounts: []types.MountPoint{dataVol, configVol, bind}},
			want: []string{"/data", "/config", "/logs"},
		},
		{
			name:              "excludeBindMounts drops bind mounts",
			ctr:               docker.ContainerInfo{Mounts: []types.MountPoint{dataVol, bind}},
			excludeBindMounts: true,
			want:              []string{"/data"},
		},
		{
			name: "include filter keeps only listed destinations",
			ctr: docker.ContainerInfo{
				Mounts:         []types.MountPoint{dataVol, configVol, bind},
				VolumesInclude: []string{"/data"},
			},
			want: []string{"/data"},
		},
		{
			name: "exclude filter drops listed destinations",
			ctr: docker.ContainerInfo{
				Mounts:         []types.MountPoint{dataVol, configVol},
				VolumesExclude: []string{"/config"},
			},
			want: []string{"/data"},
		},
		{
			name: "exclude wins over include",
			ctr: docker.ContainerInfo{
				Mounts:         []types.MountPoint{dataVol, configVol},
				VolumesInclude: []string{"/data", "/config"},
				VolumesExclude: []string{"/config"},
			},
			want: []string{"/data"},
		},
		{
			// Documents current behaviour: filters match case-insensitively
			// because contains uses strings.EqualFold (see issue #16).
			name: "include filter matches case-insensitively",
			ctr: docker.ContainerInfo{
				Mounts:         []types.MountPoint{dataVol},
				VolumesInclude: []string{"/DATA"},
			},
			want: []string{"/data"},
		},
		{
			name: "no mounts yields nil",
			ctr:  docker.ContainerInfo{},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := destinations(filterMounts(tt.ctr, tt.excludeBindMounts))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("filterMounts() destinations = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolveHostPath(t *testing.T) {
	tests := []struct {
		name  string
		mount types.MountPoint
		want  string
	}{
		{
			name:  "named volume maps to /var/lib/docker/volumes",
			mount: mountPoint("volume", "app_data", "/var/lib/docker/volumes/app_data/_data", "/data"),
			want:  "/var/lib/docker/volumes/app_data/_data",
		},
		{
			name:  "volume without a name yields empty string",
			mount: mountPoint("volume", "", "/somewhere", "/data"),
			want:  "",
		},
		{
			name:  "bind mount uses its source path",
			mount: mountPoint("bind", "", "/host/logs", "/logs"),
			want:  "/host/logs",
		},
		{
			name:  "unknown mount type falls back to source",
			mount: mountPoint("tmpfs", "", "/tmpfs-src", "/tmp"),
			want:  "/tmpfs-src",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveHostPath(tt.mount); got != tt.want {
				t.Errorf("resolveHostPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestContains(t *testing.T) {
	tests := []struct {
		name  string
		slice []string
		s     string
		want  bool
	}{
		{"exact match", []string{"/data", "/config"}, "/data", true},
		{"case-insensitive match", []string{"/Data"}, "/data", true},
		{"no match", []string{"/data"}, "/logs", false},
		{"empty slice", nil, "/data", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := contains(tt.slice, tt.s); got != tt.want {
				t.Errorf("contains(%v, %q) = %v, want %v", tt.slice, tt.s, got, tt.want)
			}
		})
	}
}
