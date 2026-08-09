package scheduler

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/rs/zerolog/log"
)

// RunFunc is the function executed on each scheduled tick.
type RunFunc func(ctx context.Context) error

// Job is a named function on a cron schedule.
type Job struct {
	Name     string
	Schedule string
	Fn       RunFunc
	// Timeout bounds a single run of Fn; the context passed to Fn is
	// cancelled when it expires. Zero means no per-run timeout.
	Timeout time.Duration
}

// Options configures scheduler lifecycle behaviour.
type Options struct {
	// ShutdownGrace is how long to wait for an in-flight job to finish after
	// SIGTERM/SIGINT before its context is cancelled and it is aborted.
	ShutdownGrace time.Duration
}

// abortDrain is how long aborted jobs get to unwind after their context is
// cancelled before Run gives up and returns an error.
const abortDrain = 30 * time.Second

// Run starts the cron scheduler with the given jobs and blocks until SIGTERM
// or SIGINT is received. All jobs share one mutex: restic maintenance takes
// exclusive repository locks, so a backup and a maintenance run must never
// overlap. A job whose tick fires while another job (or a previous run of
// itself) is still running skips that tick instead of queueing behind it.
//
// On SIGTERM/SIGINT no new ticks fire and Run waits up to opts.ShutdownGrace
// for an in-flight job to finish. If it does not — or a second signal
// arrives — the run context is cancelled, aborting the job, and Run waits a
// short drain period for it to unwind before returning.
func Run(jobs []Job, opts Options) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := cron.New(cron.WithChain(cron.SkipIfStillRunning(cronLogger{})))
	var mu sync.Mutex

	for _, job := range jobs {
		if _, err := c.AddFunc(job.Schedule, guarded(ctx, &mu, job)); err != nil {
			return fmt.Errorf("invalid schedule %q for job %s: %w", job.Schedule, job.Name, err)
		}
		log.Info().Str("job", job.Name).Str("schedule", job.Schedule).Dur("timeout", job.Timeout).Msg("job scheduled")
	}

	c.Start()
	log.Info().Msg("scheduler started")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit

	log.Info().Dur("grace", opts.ShutdownGrace).Msg("shutting down scheduler, waiting for in-flight jobs")
	return awaitShutdown(c.Stop(), cancel, opts.ShutdownGrace, abortDrain, quit)
}

// awaitShutdown waits for running jobs (tracked by stopCtx, the context
// returned by cron.Stop) to finish. If they do not finish within grace — or
// another signal arrives on quit — cancelRuns is called to abort them, and
// they get drain time to unwind. An error is returned only if jobs are still
// stuck after being aborted.
func awaitShutdown(stopCtx context.Context, cancelRuns context.CancelFunc, grace, drain time.Duration, quit <-chan os.Signal) error {
	graceTimer := time.NewTimer(grace)
	defer graceTimer.Stop()

	select {
	case <-stopCtx.Done():
		log.Info().Msg("all jobs finished, exiting")
		return nil
	case <-quit:
		log.Warn().Msg("second signal received, aborting in-flight jobs")
	case <-graceTimer.C:
		log.Warn().Dur("grace", grace).Msg("shutdown grace period expired, aborting in-flight jobs")
	}

	cancelRuns()

	drainTimer := time.NewTimer(drain)
	defer drainTimer.Stop()

	select {
	case <-stopCtx.Done():
		log.Info().Msg("aborted jobs exited")
		return nil
	case <-drainTimer.C:
		return fmt.Errorf("in-flight jobs still running %s after cancellation", drain)
	}
}

// guarded wraps a job so that it skips its tick when another guarded job
// still holds the mutex, and bounds each run with the job's timeout.
func guarded(ctx context.Context, mu *sync.Mutex, job Job) func() {
	return func() {
		if !mu.TryLock() {
			log.Warn().Str("job", job.Name).Msg("another job is still running, skipping this tick")
			return
		}
		defer mu.Unlock()

		runCtx := ctx
		if job.Timeout > 0 {
			var cancel context.CancelFunc
			runCtx, cancel = context.WithTimeout(ctx, job.Timeout)
			defer cancel()
		}

		log.Info().Str("job", job.Name).Msg("job started")
		err := job.Fn(runCtx)
		switch {
		case err == nil:
			log.Info().Str("job", job.Name).Msg("job complete")
		case runCtx.Err() == context.DeadlineExceeded:
			log.Error().Err(err).Str("job", job.Name).Dur("timeout", job.Timeout).Msg("job timed out")
		default:
			log.Error().Err(err).Str("job", job.Name).Msg("job failed")
		}
	}
}

// cronLogger adapts zerolog to cron's Logger interface, so cron can report
// skipped ticks and internal errors through the application log.
type cronLogger struct{}

func (cronLogger) Info(msg string, keysAndValues ...interface{}) {
	log.Info().Fields(keysAndValues).Msg("cron: " + msg)
}

func (cronLogger) Error(err error, msg string, keysAndValues ...interface{}) {
	log.Error().Err(err).Fields(keysAndValues).Msg("cron: " + msg)
}
