package backup

import (
	"context"
	"errors"
	"reflect"
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

// recordSteps returns fake dump/volume steps that record their order and
// fail on the named step.
func recordSteps(order *[]string, failOn string) (func(context.Context, string) error, func(context.Context) error) {
	dump := func(_ context.Context, dbType string) error {
		*order = append(*order, dbType)
		if dbType == failOn {
			return errors.New(dbType + " failed")
		}
		return nil
	}
	volumes := func(context.Context) error {
		*order = append(*order, "volumes")
		if failOn == "volumes" {
			return errors.New("volumes failed")
		}
		return nil
	}
	return dump, volumes
}

func TestRunBackupStepsDumpsBeforeVolumes(t *testing.T) {
	// A container may be stopped for its volume backup; its databases must
	// be dumped while it is still up, not right after it restarts.
	ctr := docker.ContainerInfo{
		VolumesEnabled: true, VolumeStopDuringBackup: true,
		PostgresEnabled: true, MySQLEnabled: true, MariaDBEnabled: true,
	}
	var order []string
	dump, volumes := recordSteps(&order, "")

	if err := runBackupSteps(context.Background(), ctr, dump, volumes); err != nil {
		t.Fatalf("runBackupSteps() error = %v", err)
	}
	want := []string{"postgres", "mysql", "mariadb", "volumes"}
	if !reflect.DeepEqual(order, want) {
		t.Errorf("step order = %v, want %v", order, want)
	}
}

func TestRunBackupStepsSkipsDisabledSteps(t *testing.T) {
	var order []string
	dump, volumes := recordSteps(&order, "")

	if err := runBackupSteps(context.Background(), docker.ContainerInfo{MySQLEnabled: true}, dump, volumes); err != nil {
		t.Fatalf("runBackupSteps() error = %v", err)
	}
	if !reflect.DeepEqual(order, []string{"mysql"}) {
		t.Errorf("step order = %v, want only mysql", order)
	}
}

func TestRunBackupStepsStopsAtFirstFailure(t *testing.T) {
	ctr := docker.ContainerInfo{VolumesEnabled: true, PostgresEnabled: true}
	var order []string
	dump, volumes := recordSteps(&order, "postgres")

	if err := runBackupSteps(context.Background(), ctr, dump, volumes); err == nil {
		t.Fatal("runBackupSteps() error = nil, want the dump failure")
	}
	if !reflect.DeepEqual(order, []string{"postgres"}) {
		t.Errorf("step order = %v, want volumes skipped after the failed dump", order)
	}
}
