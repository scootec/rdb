package restic

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"

	"github.com/rs/zerolog/log"
)

// RetentionPolicy specifies how many snapshots to keep.
type RetentionPolicy struct {
	Daily   int
	Weekly  int
	Monthly int
	Yearly  int
}

// Runner executes restic commands.
type Runner struct{}

// New creates a new Runner. Repository and password are read from environment variables
// (RESTIC_REPOSITORY, RESTIC_PASSWORD) and passed to restic automatically.
func New() *Runner {
	return &Runner{}
}

// InitRepo initialises the restic repository if it does not exist yet.
// It first checks with "restic cat config"; only runs "restic init" if needed.
func (r *Runner) InitRepo() error {
	if err := r.run(nil, "cat", "config"); err == nil {
		log.Debug().Msg("restic repository already initialised")
		return nil
	}
	log.Info().Msg("initialising restic repository")
	return r.run(nil, "init")
}

// BackupDir runs "restic backup <path>" and tags the snapshot with the given tags.
func (r *Runner) BackupDir(path string, tags []string) error {
	args := []string{"backup", path}
	for _, t := range tags {
		args = append(args, "--tag", t)
	}
	return r.run(nil, args...)
}

// BackupFromStdin streams data from reader into restic using --stdin.
func (r *Runner) BackupFromStdin(filename string, reader io.Reader, tags []string) error {
	args := []string{"backup", "--stdin", "--stdin-filename", filename}
	for _, t := range tags {
		args = append(args, "--tag", t)
	}
	return r.run(reader, args...)
}

// Snapshots runs "restic snapshots --latest 1" to verify the repository is accessible.
func (r *Runner) Snapshots() error {
	return r.run(nil, "snapshots", "--latest", "1")
}

// Forget removes old snapshots according to the retention policy.
func (r *Runner) Forget(policy RetentionPolicy) error {
	return r.run(nil,
		"forget",
		"--keep-daily", strconv.Itoa(policy.Daily),
		"--keep-weekly", strconv.Itoa(policy.Weekly),
		"--keep-monthly", strconv.Itoa(policy.Monthly),
		"--keep-yearly", strconv.Itoa(policy.Yearly),
	)
}

// Prune removes unreferenced data from the repository.
func (r *Runner) Prune() error {
	return r.run(nil, "prune")
}

// Check verifies repository integrity.
func (r *Runner) Check() error {
	return r.run(nil, "check")
}

// run executes the restic binary with the given arguments.
// If stdin is non-nil it is connected to the command's stdin.
func (r *Runner) run(stdin io.Reader, args ...string) error {
	log.Debug().Strs("args", args).Msg("running restic")

	cmd := exec.Command("restic", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()

	if stdin != nil {
		cmd.Stdin = stdin
	}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("restic %v: %w", args, err)
	}
	return nil
}
