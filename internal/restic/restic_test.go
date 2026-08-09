package restic

import (
	"reflect"
	"testing"
)

func TestRestoreArgs(t *testing.T) {
	tests := []struct {
		name       string
		snapshotID string
		target     string
		includes   []string
		want       []string
	}{
		{
			"no includes",
			"abc123", "/", nil,
			[]string{"restore", "abc123", "--target", "/", "--verify"},
		},
		{
			"single include scopes the restore",
			"abc123", "/", []string{"/var/lib/docker/volumes/myapp_data/_data"},
			[]string{"restore", "abc123", "--target", "/", "--verify", "--include", "/var/lib/docker/volumes/myapp_data/_data"},
		},
		{
			"multiple includes",
			"abc123", "/", []string{"/data/a", "/data/b"},
			[]string{"restore", "abc123", "--target", "/", "--verify", "--include", "/data/a", "--include", "/data/b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := restoreArgs(tt.snapshotID, tt.target, tt.includes); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("restoreArgs(%q, %q, %v) = %v, want %v", tt.snapshotID, tt.target, tt.includes, got, tt.want)
			}
		})
	}
}
