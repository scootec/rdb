package restore

import (
	"testing"

	"github.com/scootec/rdb/internal/restic"
)

func snapWithTags(tags ...string) restic.Snapshot {
	return restic.Snapshot{ShortID: "abcd1234", ID: "abcd1234full", Tags: tags}
}

func TestSnapshotDBType(t *testing.T) {
	tests := []struct {
		name string
		snap restic.Snapshot
		want string
	}{
		{"postgres tag", snapWithTags("rdb", "postgres", "service:db"), "postgres"},
		{"mysql tag", snapWithTags("rdb", "mysql"), "mysql"},
		{"mariadb tag", snapWithTags("rdb", "mariadb"), "mariadb"},
		{"volume snapshot has no db type", snapWithTags("rdb", "volume", "service:app"), ""},
		{"no tags", snapWithTags(), ""},
		{"first db tag wins", snapWithTags("mysql", "postgres"), "mysql"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := snapshotDBType(tt.snap); got != tt.want {
				t.Errorf("snapshotDBType(%v) = %q, want %q", tt.snap.Tags, got, tt.want)
			}
		})
	}
}

func TestHasTag(t *testing.T) {
	tests := []struct {
		name string
		snap restic.Snapshot
		tag  string
		want bool
	}{
		{"tag present", snapWithTags("rdb", "volume"), "volume", true},
		{"tag absent", snapWithTags("rdb", "postgres"), "volume", false},
		{"no tags", snapWithTags(), "volume", false},
		{"exact match only", snapWithTags("volumes"), "volume", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasTag(tt.snap, tt.tag); got != tt.want {
				t.Errorf("hasTag(%v, %q) = %v, want %v", tt.snap.Tags, tt.tag, got, tt.want)
			}
		})
	}
}

func TestParseTagValue(t *testing.T) {
	tests := []struct {
		name   string
		snap   restic.Snapshot
		prefix string
		want   string
	}{
		{"project tag", snapWithTags("rdb", "project:myapp", "service:db"), "project", "myapp"},
		{"service tag", snapWithTags("rdb", "project:myapp", "service:db"), "service", "db"},
		{"missing prefix", snapWithTags("rdb", "volume"), "project", ""},
		{"value containing colon is kept whole", snapWithTags("service:db:primary"), "service", "db:primary"},
		{"empty value", snapWithTags("project:"), "project", ""},
		{"prefix without colon does not match", snapWithTags("project"), "project", ""},
		{"first matching tag wins", snapWithTags("service:a", "service:b"), "service", "a"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseTagValue(tt.snap, tt.prefix); got != tt.want {
				t.Errorf("parseTagValue(%v, %q) = %q, want %q", tt.snap.Tags, tt.prefix, got, tt.want)
			}
		})
	}
}
