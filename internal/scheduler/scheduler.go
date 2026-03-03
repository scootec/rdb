package scheduler

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/robfig/cron/v3"
	"github.com/rs/zerolog/log"
)

// RunFunc is the function executed on each scheduled tick.
type RunFunc func(ctx context.Context) error

// Run starts the cron scheduler with the given schedule expression and blocks
// until SIGTERM or SIGINT is received.
func Run(schedule string, fn RunFunc) error {
	c := cron.New()

	_, err := c.AddFunc(schedule, func() {
		ctx := context.Background()
		log.Info().Str("schedule", schedule).Msg("running scheduled backup")
		if err := fn(ctx); err != nil {
			log.Error().Err(err).Msg("scheduled backup failed")
		} else {
			log.Info().Msg("scheduled backup complete")
		}
	})
	if err != nil {
		return err
	}

	c.Start()
	log.Info().Str("schedule", schedule).Msg("scheduler started")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit

	log.Info().Msg("shutting down scheduler")
	c.Stop()
	return nil
}
