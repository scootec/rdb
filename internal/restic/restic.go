package restic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"

	"github.com/rs/zerolog/log"
)

// Snapshot represents a restic snapshot returned by "restic snapshots --json".
type Snapshot struct {
	ShortID  string   `json:"short_id"`
	ID       string   `json:"id"`
	Time     string   `json:"time"`
	Paths    []string `json:"paths"`
	Tags     []string `json:"tags"`
	Hostname string   `json:"hostname"`
}

// RetentionPolicy specifies how many snapshots to keep.
type RetentionPolicy struct {
	Daily   int
	Weekly  int
	Monthly int
	Yearly  int
	Last    int
	Hourly  int
	Within  string
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
	args := []string{
		"forget",
		"--keep-daily", strconv.Itoa(policy.Daily),
		"--keep-weekly", strconv.Itoa(policy.Weekly),
		"--keep-monthly", strconv.Itoa(policy.Monthly),
		"--keep-yearly", strconv.Itoa(policy.Yearly),
	}
	if policy.Last > 0 {
		args = append(args, "--keep-last", strconv.Itoa(policy.Last))
	}
	if policy.Hourly > 0 {
		args = append(args, "--keep-hourly", strconv.Itoa(policy.Hourly))
	}
	if policy.Within != "" {
		args = append(args, "--keep-within", policy.Within)
	}
	return r.run(nil, args...)
}

// Prune removes unreferenced data from the repository.
func (r *Runner) Prune() error {
	return r.run(nil, "prune")
}

// Check verifies repository integrity.
func (r *Runner) Check() error {
	return r.run(nil, "check")
}

// SnapshotsByID returns snapshot metadata for a specific snapshot ID.
func (r *Runner) SnapshotsByID(id string) ([]Snapshot, error) {
	out, err := r.runCapture("snapshots", id, "--json")
	if err != nil {
		return nil, err
	}
	var snaps []Snapshot
	if err := json.Unmarshal(out, &snaps); err != nil {
		return nil, fmt.Errorf("parsing snapshot JSON: %w", err)
	}
	return snaps, nil
}

// SnapshotsAll returns all rdb-managed snapshots.
func (r *Runner) SnapshotsAll() ([]Snapshot, error) {
	out, err := r.runCapture("snapshots", "--tag", "rdb", "--json")
	if err != nil {
		return nil, err
	}
	var snaps []Snapshot
	if err := json.Unmarshal(out, &snaps); err != nil {
		return nil, fmt.Errorf("parsing snapshot JSON: %w", err)
	}
	return snaps, nil
}

// Restore runs "restic restore <snapshotID> --target <target>".
func (r *Runner) Restore(snapshotID, target string) error {
	return r.run(nil, "restore", snapshotID, "--target", target)
}

// Dump runs "restic dump <snapshotID> <path>" and returns stdout as a reader.
// The caller must close the returned reader.
func (r *Runner) Dump(snapshotID, path string) (io.ReadCloser, error) {
	args := []string{"dump", snapshotID, path}
	log.Debug().Strs("args", args).Msg("running restic")

	cmd := exec.Command("restic", args...)
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("restic dump stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("restic dump start: %w", err)
	}

	return &cmdReader{cmd: cmd, ReadCloser: stdout}, nil
}

// cmdReader wraps a command's stdout pipe and waits for the command on Close.
type cmdReader struct {
	cmd *exec.Cmd
	io.ReadCloser
}

func (r *cmdReader) Close() error {
	r.ReadCloser.Close()
	return r.cmd.Wait()
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

// runCapture executes restic and returns captured stdout bytes.
func (r *Runner) runCapture(args ...string) ([]byte, error) {
	log.Debug().Strs("args", args).Msg("running restic")

	cmd := exec.Command("restic", args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("restic %v: %w", args, err)
	}
	return buf.Bytes(), nil
}
