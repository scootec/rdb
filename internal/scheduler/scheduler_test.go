package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestGuardedRunsJobWhenFree(t *testing.T) {
	var mu sync.Mutex
	ran := false

	guarded(&mu, Job{
		Name: "backup",
		Fn: func(ctx context.Context) error {
			ran = true
			return nil
		},
	})()

	if !ran {
		t.Fatal("guarded job did not run while mutex was free")
	}
}

func TestGuardedSkipsWhenAnotherJobHoldsLock(t *testing.T) {
	var mu sync.Mutex
	ran := false

	mu.Lock()
	defer mu.Unlock()

	guarded(&mu, Job{
		Name: "maintenance",
		Fn: func(ctx context.Context) error {
			ran = true
			return nil
		},
	})()

	if ran {
		t.Fatal("guarded job ran while another job held the lock")
	}
}

func TestGuardedJobsAreMutuallyExclusive(t *testing.T) {
	var mu sync.Mutex

	backupRunning := make(chan struct{})
	release := make(chan struct{})
	backup := guarded(&mu, Job{
		Name: "backup",
		Fn: func(ctx context.Context) error {
			close(backupRunning)
			<-release
			return nil
		},
	})

	maintenanceRan := false
	maintenance := guarded(&mu, Job{
		Name: "maintenance",
		Fn: func(ctx context.Context) error {
			maintenanceRan = true
			return nil
		},
	})

	done := make(chan struct{})
	go func() {
		backup()
		close(done)
	}()

	<-backupRunning
	maintenance() // fires while backup is mid-run: must skip, not block or overlap
	if maintenanceRan {
		t.Fatal("maintenance ran concurrently with backup")
	}

	close(release)
	<-done

	maintenance() // lock is free again: must run
	if !maintenanceRan {
		t.Fatal("maintenance did not run after backup finished")
	}
}

func TestGuardedReleasesLockAfterError(t *testing.T) {
	var mu sync.Mutex

	guarded(&mu, Job{
		Name: "backup",
		Fn: func(ctx context.Context) error {
			return context.DeadlineExceeded
		},
	})()

	locked := make(chan struct{})
	go func() {
		mu.Lock()
		defer mu.Unlock()
		close(locked)
	}()

	select {
	case <-locked:
	case <-time.After(time.Second):
		t.Fatal("mutex still held after a failing job")
	}
}

func TestRunRejectsInvalidSchedule(t *testing.T) {
	err := Run([]Job{{
		Name:     "backup",
		Schedule: "not a cron expression",
		Fn:       func(ctx context.Context) error { return nil },
	}})
	if err == nil {
		t.Fatal("Run() accepted an invalid cron expression")
	}
}
