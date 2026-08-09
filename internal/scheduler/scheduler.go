package scheduler

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"

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
}

// Run starts the cron scheduler with the given jobs and blocks until SIGTERM
// or SIGINT is received. All jobs share one mutex: restic maintenance takes
// exclusive repository locks, so a backup and a maintenance run must never
// overlap. A job whose tick fires while another job (or a previous run of
// itself) is still running skips that tick instead of queueing behind it.
func Run(jobs []Job) error {
	c := cron.New()
	var mu sync.Mutex

	for _, job := range jobs {
		if _, err := c.AddFunc(job.Schedule, guarded(&mu, job)); err != nil {
			return fmt.Errorf("invalid schedule %q for job %s: %w", job.Schedule, job.Name, err)
		}
		log.Info().Str("job", job.Name).Str("schedule", job.Schedule).Msg("job scheduled")
	}

	c.Start()
	log.Info().Msg("scheduler started")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit

	log.Info().Msg("shutting down scheduler")
	c.Stop()
	return nil
}

// guarded wraps a job so that it skips its tick when another guarded job
// still holds the mutex.
func guarded(mu *sync.Mutex, job Job) func() {
	return func() {
		if !mu.TryLock() {
			log.Warn().Str("job", job.Name).Msg("another job is still running, skipping this tick")
			return
		}
		defer mu.Unlock()

		log.Info().Str("job", job.Name).Msg("job started")
		if err := job.Fn(context.Background()); err != nil {
			log.Error().Err(err).Str("job", job.Name).Msg("job failed")
		} else {
			log.Info().Str("job", job.Name).Msg("job complete")
		}
	}
}
