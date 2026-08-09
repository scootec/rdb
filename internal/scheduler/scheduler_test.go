package scheduler

import (
	"context"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestGuardedRunsJobWhenFree(t *testing.T) {
	var mu sync.Mutex
	ran := false

	guarded(context.Background(), &mu, Job{
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

	guarded(context.Background(), &mu, Job{
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
	backup := guarded(context.Background(), &mu, Job{
		Name: "backup",
		Fn: func(ctx context.Context) error {
			close(backupRunning)
			<-release
			return nil
		},
	})

	maintenanceRan := false
	maintenance := guarded(context.Background(), &mu, Job{
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

	guarded(context.Background(), &mu, Job{
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

func TestGuardedAppliesTimeout(t *testing.T) {
	var mu sync.Mutex

	var gotErr error
	guarded(context.Background(), &mu, Job{
		Name:    "backup",
		Timeout: 10 * time.Millisecond,
		Fn: func(ctx context.Context) error {
			select {
			case <-ctx.Done():
				gotErr = ctx.Err()
				return ctx.Err()
			case <-time.After(5 * time.Second):
				return nil
			}
		},
	})()

	if gotErr != context.DeadlineExceeded {
		t.Fatalf("job context error = %v, want context.DeadlineExceeded", gotErr)
	}
}

func TestGuardedWithoutTimeoutLeavesContextUnbounded(t *testing.T) {
	var mu sync.Mutex

	guarded(context.Background(), &mu, Job{
		Name: "backup",
		Fn: func(ctx context.Context) error {
			if _, ok := ctx.Deadline(); ok {
				t.Error("job context has a deadline, want none when Timeout is zero")
			}
			return nil
		},
	})()
}

func TestGuardedPropagatesParentCancellation(t *testing.T) {
	var mu sync.Mutex
	parent, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go guarded(parent, &mu, Job{
		Name:    "backup",
		Timeout: time.Hour,
		Fn: func(ctx context.Context) error {
			defer close(done)
			<-ctx.Done()
			return ctx.Err()
		},
	})()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelling the parent context did not cancel the job context")
	}
}

func TestAwaitShutdownReturnsWhenJobsFinish(t *testing.T) {
	stopCtx, jobsDone := context.WithCancel(context.Background())
	cancelled := false

	go func() {
		time.Sleep(20 * time.Millisecond)
		jobsDone() // in-flight jobs finish on their own
	}()

	err := awaitShutdown(stopCtx, func() { cancelled = true }, 5*time.Second, 5*time.Second, nil)
	if err != nil {
		t.Fatalf("awaitShutdown() = %v, want nil", err)
	}
	if cancelled {
		t.Error("run context was cancelled although jobs finished within the grace period")
	}
}

func TestAwaitShutdownCancelsAfterGrace(t *testing.T) {
	stopCtx, jobsDone := context.WithCancel(context.Background())

	// The job only exits once its context is cancelled, simulating a hung
	// run that needs the abort to unblock.
	err := awaitShutdown(stopCtx, jobsDone, 10*time.Millisecond, 5*time.Second, nil)
	if err != nil {
		t.Fatalf("awaitShutdown() = %v, want nil when aborted jobs exit within the drain period", err)
	}
}

func TestAwaitShutdownErrorsWhenJobsIgnoreCancellation(t *testing.T) {
	stopCtx, jobsDone := context.WithCancel(context.Background())
	defer jobsDone()

	err := awaitShutdown(stopCtx, func() {}, 10*time.Millisecond, 10*time.Millisecond, nil)
	if err == nil {
		t.Fatal("awaitShutdown() = nil, want error when jobs survive cancellation")
	}
}

func TestAwaitShutdownSecondSignalAborts(t *testing.T) {
	stopCtx, jobsDone := context.WithCancel(context.Background())

	quit := make(chan os.Signal, 1)
	quit <- syscall.SIGTERM // second signal already pending

	start := time.Now()
	// Grace is long: only the second signal can trigger the abort promptly.
	err := awaitShutdown(stopCtx, jobsDone, time.Hour, 5*time.Second, quit)
	if err != nil {
		t.Fatalf("awaitShutdown() = %v, want nil", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("awaitShutdown() took %v, want prompt abort on second signal", elapsed)
	}
}

func TestRunRejectsInvalidSchedule(t *testing.T) {
	err := Run([]Job{{
		Name:     "backup",
		Schedule: "not a cron expression",
		Fn:       func(ctx context.Context) error { return nil },
	}}, Options{})
	if err == nil {
		t.Fatal("Run() accepted an invalid cron expression")
	}
}
