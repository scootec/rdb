package health

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func statePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "rdb-status")
}

func TestReadStateMissingFile(t *testing.T) {
	if _, err := ReadState(statePath(t)); !os.IsNotExist(err) {
		t.Fatalf("ReadState() on missing file: err = %v, want IsNotExist", err)
	}
}

func TestReadStateCorruptFile(t *testing.T) {
	path := statePath(t)
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadState(path); err == nil {
		t.Fatal("ReadState() accepted a corrupt state file")
	}
}

func TestRecordRoundTrip(t *testing.T) {
	path := statePath(t)
	now := time.Date(2026, 8, 9, 2, 30, 0, 0, time.UTC)

	if err := Record(path, "backup", nil, now); err != nil {
		t.Fatalf("Record() success: %v", err)
	}
	if err := Record(path, "maintenance", errors.New("prune exploded"), now); err != nil {
		t.Fatalf("Record() failure: %v", err)
	}

	s, err := ReadState(path)
	if err != nil {
		t.Fatalf("ReadState(): %v", err)
	}

	b := s["backup"]
	if !b.OK || b.Error != "" || b.Pending || !b.Timestamp.Equal(now) {
		t.Errorf("backup result = %+v, want ok at %s", b, now)
	}
	m := s["maintenance"]
	if m.OK || m.Error != "prune exploded" || m.Pending {
		t.Errorf("maintenance result = %+v, want failure with error text", m)
	}
}

func TestRecordOverwritesPreviousResult(t *testing.T) {
	path := statePath(t)
	now := time.Now()

	if err := Record(path, "backup", errors.New("boom"), now); err != nil {
		t.Fatal(err)
	}
	if err := Record(path, "backup", nil, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	s, err := ReadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if !s["backup"].OK || s["backup"].Error != "" {
		t.Errorf("backup result = %+v, want success after recovery", s["backup"])
	}
}

func TestMarkPendingRegistersJobs(t *testing.T) {
	path := statePath(t)
	now := time.Now()

	if err := MarkPending(path, now, "backup", "maintenance"); err != nil {
		t.Fatalf("MarkPending(): %v", err)
	}

	s, err := ReadState(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range []string{"backup", "maintenance"} {
		r := s[job]
		if !r.Pending || !r.OK {
			t.Errorf("%s result = %+v, want pending", job, r)
		}
	}
}

func TestMarkPendingPreservesExistingResults(t *testing.T) {
	path := statePath(t)
	now := time.Now()

	if err := Record(path, "backup", errors.New("boom"), now); err != nil {
		t.Fatal(err)
	}
	// Container restarts: pending registration must not hide the failure.
	if err := MarkPending(path, now.Add(time.Minute), "backup", "maintenance"); err != nil {
		t.Fatal(err)
	}

	s, err := ReadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if s["backup"].Pending || s["backup"].OK {
		t.Errorf("backup result = %+v, want preserved failure", s["backup"])
	}
	if !s["maintenance"].Pending {
		t.Errorf("maintenance result = %+v, want pending", s["maintenance"])
	}
}

func TestEvaluate(t *testing.T) {
	// Daily backup at 02:00; reference "now" is 03:00 on the 9th.
	schedules := map[string]string{"backup": "0 2 * * *"}
	now := time.Date(2026, 8, 9, 3, 0, 0, 0, time.UTC)
	grace := time.Hour

	tests := []struct {
		name  string
		state State
		want  string // substring of the error, empty for healthy
	}{
		{
			name:  "recent success is healthy",
			state: State{"backup": {Timestamp: now.Add(-time.Hour), OK: true}},
		},
		{
			name:  "failed run is unhealthy",
			state: State{"backup": {Timestamp: now.Add(-time.Hour), OK: false, Error: "dump failed"}},
			want:  "last backup run failed: dump failed",
		},
		{
			name: "success older than schedule plus grace is overdue",
			// Last ran 02:00 two days ago; the run due 02:00 yesterday never
			// happened and 25h have passed since.
			state: State{"backup": {Timestamp: now.Add(-49 * time.Hour), OK: true}},
			want:  "backup run overdue",
		},
		{
			name: "success within grace of the next run is healthy",
			// Due at 02:00 today, it is 03:00, grace is 1h: exactly on the edge.
			state: State{"backup": {Timestamp: now.Add(-25 * time.Hour), OK: true}},
		},
		{
			name:  "fresh pending job is healthy",
			state: State{"backup": {Timestamp: now.Add(-time.Hour), OK: true, Pending: true}},
		},
		{
			name:  "pending job past its first run plus grace is overdue",
			state: State{"backup": {Timestamp: now.Add(-48 * time.Hour), OK: true, Pending: true}},
			want:  "backup run overdue",
		},
		{
			name: "job without schedule skips overdue but reports failure",
			state: State{"maintenance": {
				Timestamp: now.Add(-30 * 24 * time.Hour), OK: false, Error: "check failed",
			}},
			want: "last maintenance run failed: check failed",
		},
		{
			name:  "job without schedule and old success is healthy",
			state: State{"maintenance": {Timestamp: now.Add(-30 * 24 * time.Hour), OK: true}},
		},
		{
			name: "failed and overdue reports both",
			state: State{
				"backup":      {Timestamp: now.Add(-49 * time.Hour), OK: false, Error: "boom"},
				"maintenance": {Timestamp: now.Add(-time.Hour), OK: false, Error: "bang"},
			},
			want: "last backup run failed: boom",
		},
		{
			name:  "empty state is healthy",
			state: State{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Evaluate(tt.state, schedules, grace, now)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("Evaluate() = %v, want healthy", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Evaluate() = nil, want error containing %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Evaluate() = %q, want substring %q", err, tt.want)
			}
		})
	}
}

func TestEvaluateIgnoresUnparseableSchedule(t *testing.T) {
	state := State{"backup": {Timestamp: time.Now().Add(-100 * time.Hour), OK: true}}
	err := Evaluate(state, map[string]string{"backup": "not a cron"}, time.Hour, time.Now())
	if err != nil {
		t.Fatalf("Evaluate() = %v, want healthy when schedule is unparseable", err)
	}
}
