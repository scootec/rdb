package backup

import (
	"strings"
	"testing"

	"github.com/scootec/rdb/internal/docker"
)

func TestDiscoveryErrors(t *testing.T) {
	tests := []struct {
		name    string
		running int
		idle    []docker.IdleContainer
		want    []string // substrings, one per expected error
	}{
		{"all targets running", 2, nil, nil},
		{"nothing discovered", 0, nil, []string{"no containers with rdb labels found"}},
		{"stopped target alongside running ones", 1,
			[]docker.IdleContainer{{Name: "myapp-db-1", State: "exited"}},
			[]string{"myapp-db-1 has rdb labels but is exited"}},
		{"only stopped targets", 0,
			[]docker.IdleContainer{{Name: "a", State: "exited"}, {Name: "b", State: "restarting"}},
			[]string{"a has rdb labels but is exited", "b has rdb labels but is restarting"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := discoveryErrors(tt.running, tt.idle)
			if len(errs) != len(tt.want) {
				t.Fatalf("discoveryErrors() = %v, want %d error(s)", errs, len(tt.want))
			}
			for i, want := range tt.want {
				if !strings.Contains(errs[i].Error(), want) {
					t.Errorf("error %d = %q, want it to contain %q", i, errs[i], want)
				}
			}
		})
	}
}
