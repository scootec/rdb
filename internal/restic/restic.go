package restic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"time"

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

// TagPartial marks a snapshot created from a database dump that later
// reported failure and could not be deleted. Such snapshots hold truncated
// data and are excluded from listing and restore.
const TagPartial = "partial"

// HasTag reports whether the snapshot carries the given tag.
func (s Snapshot) HasTag(tag string) bool {
	for _, t := range s.Tags {
		if t == tag {
			return true
		}
	}
	return false
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
type Runner struct {
	// hostname is recorded on every snapshot via "--host". The rdb container's
	// own hostname is its container ID, which changes on every recreation and
	// would break restic's parent-snapshot selection for incremental backups.
	hostname string
}

// New creates a new Runner. Repository and password are read from environment variables
// (RESTIC_REPOSITORY, RESTIC_PASSWORD) and passed to restic automatically.
// hostname is set as the snapshot hostname on backups; empty means restic's default.
func New(hostname string) *Runner {
	return &Runner{hostname: hostname}
}

// resticBinary is the restic executable name; a variable so tests can
// substitute a harmless command.
var resticBinary = "restic"

// interruptWaitDelay is how long a cancelled restic process gets to exit
// after SIGINT before it is killed.
const interruptWaitDelay = 10 * time.Second

// newCmd builds a restic command bound to ctx. On cancellation the process
// receives SIGINT — restic then removes its repository locks and exits — and
// is killed if it is still running after interruptWaitDelay.
func newCmd(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, resticBinary, args...)
	cmd.Env = os.Environ()
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = interruptWaitDelay
	return cmd
}

// InitRepo initialises the restic repository if it does not exist yet.
// It first checks with "restic cat config"; only runs "restic init" if needed.
func (r *Runner) InitRepo(ctx context.Context) error {
	if err := r.runQuiet(ctx, "cat", "config"); err == nil {
		log.Debug().Msg("restic repository already initialised")
		return nil
	}
	log.Info().Msg("no existing restic repository found, creating a new one")
	return r.run(ctx, nil, "init")
}

// BackupDir runs "restic backup <path>" and tags the snapshot with the given tags.
func (r *Runner) BackupDir(ctx context.Context, path string, tags []string) error {
	return r.run(ctx, nil, backupDirArgs(path, r.hostname, tags)...)
}

func backupDirArgs(path, host string, tags []string) []string {
	args := []string{"backup", path}
	if host != "" {
		args = append(args, "--host", host)
	}
	for _, t := range tags {
		args = append(args, "--tag", t)
	}
	return args
}

// BackupFromStdin streams data from reader into restic using --stdin and
// returns the ID of the created snapshot, parsed from restic's --json
// summary message. The ID is returned even when restic itself fails, if a
// snapshot was committed before the failure, so callers can clean it up. An
// empty ID with a nil error means the backup succeeded but the summary could
// not be parsed.
func (r *Runner) BackupFromStdin(ctx context.Context, filename string, reader io.Reader, tags []string) (string, error) {
	out, err := r.runCapture(ctx, reader, backupStdinArgs(filename, r.hostname, tags)...)
	snapshotID := parseBackupSummary(out)
	if err != nil {
		return snapshotID, err
	}
	if snapshotID == "" {
		log.Warn().Msg("restic backup succeeded but no snapshot ID found in its output")
	} else {
		log.Debug().Str("snapshot", snapshotID).Msg("created snapshot")
	}
	return snapshotID, nil
}

func backupStdinArgs(filename, host string, tags []string) []string {
	args := []string{"backup", "--json", "--stdin", "--stdin-filename", filename}
	if host != "" {
		args = append(args, "--host", host)
	}
	for _, t := range tags {
		args = append(args, "--tag", t)
	}
	return args
}

// backupMessage is one line of restic's --json backup output.
type backupMessage struct {
	MessageType string `json:"message_type"`
	SnapshotID  string `json:"snapshot_id"`
}

// parseBackupSummary extracts the created snapshot ID from restic's --json
// backup output, or returns "" if no summary message is present.
func parseBackupSummary(out []byte) string {
	for _, line := range bytes.Split(out, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var msg backupMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		if msg.MessageType == "summary" && msg.SnapshotID != "" {
			return msg.SnapshotID
		}
	}
	return ""
}

// Snapshots runs "restic snapshots --latest 1" to verify the repository is accessible.
func (r *Runner) Snapshots(ctx context.Context) error {
	return r.run(ctx, nil, "snapshots", "--latest", "1")
}

// Forget removes old snapshots according to the retention policy.
// Only rdb-managed snapshots (tag "rdb") are considered, so foreign snapshots
// in a shared repository are never touched.
func (r *Runner) Forget(ctx context.Context, policy RetentionPolicy) error {
	return r.run(ctx, nil, forgetArgs(policy)...)
}

func forgetArgs(policy RetentionPolicy) []string {
	// restic's default grouping is (hostname, paths); the container hostname
	// changes on every recreation, which fragments retention groups and keeps
	// old groups' snapshots forever. Group by (paths, tags) instead: paths keep
	// each volume of a multi-volume service in its own group, tags separate
	// projects/services/components independent of hostname.
	args := []string{
		"forget",
		"--tag", "rdb",
		"--group-by", "paths,tags",
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
	return args
}

// ForgetSnapshot removes a single snapshot by ID.
func (r *Runner) ForgetSnapshot(ctx context.Context, snapshotID string) error {
	return r.run(ctx, nil, "forget", snapshotID)
}

// TagSnapshot adds tags to an existing snapshot.
func (r *Runner) TagSnapshot(ctx context.Context, snapshotID string, tags []string) error {
	return r.run(ctx, nil, tagAddArgs(snapshotID, tags)...)
}

func tagAddArgs(snapshotID string, tags []string) []string {
	args := []string{"tag"}
	for _, t := range tags {
		args = append(args, "--add", t)
	}
	return append(args, snapshotID)
}

// Prune removes unreferenced data from the repository.
func (r *Runner) Prune(ctx context.Context) error {
	return r.run(ctx, nil, "prune")
}

// Check verifies repository integrity.
func (r *Runner) Check(ctx context.Context) error {
	return r.run(ctx, nil, "check")
}

// SnapshotsByID returns snapshot metadata for a specific snapshot ID.
func (r *Runner) SnapshotsByID(ctx context.Context, id string) ([]Snapshot, error) {
	out, err := r.runCapture(ctx, nil, "snapshots", id, "--json")
	if err != nil {
		return nil, err
	}
	var snaps []Snapshot
	if err := json.Unmarshal(out, &snaps); err != nil {
		return nil, fmt.Errorf("parsing snapshot JSON: %w", err)
	}
	return snaps, nil
}

// SnapshotsAll returns all rdb-managed snapshots, excluding partial-tagged
// snapshots left behind by failed database dumps.
func (r *Runner) SnapshotsAll(ctx context.Context) ([]Snapshot, error) {
	out, err := r.runCapture(ctx, nil, "snapshots", "--tag", "rdb", "--json")
	if err != nil {
		return nil, err
	}
	var snaps []Snapshot
	if err := json.Unmarshal(out, &snaps); err != nil {
		return nil, fmt.Errorf("parsing snapshot JSON: %w", err)
	}
	return excludePartial(snaps), nil
}

// excludePartial filters out snapshots tagged as partial.
func excludePartial(snaps []Snapshot) []Snapshot {
	kept := make([]Snapshot, 0, len(snaps))
	for _, s := range snaps {
		if !s.HasTag(TagPartial) {
			kept = append(kept, s)
		}
	}
	return kept
}

// Restore runs "restic restore <snapshotID> --target <target> --verify",
// optionally scoped to the given include paths so only those subtrees are written.
func (r *Runner) Restore(ctx context.Context, snapshotID, target string, includes []string) error {
	return r.run(ctx, nil, restoreArgs(snapshotID, target, includes)...)
}

func restoreArgs(snapshotID, target string, includes []string) []string {
	args := []string{"restore", snapshotID, "--target", target, "--verify"}
	for _, inc := range includes {
		args = append(args, "--include", inc)
	}
	return args
}

// Dump runs "restic dump <snapshotID> <path>" and returns stdout as a reader.
// The caller must close the returned reader.
func (r *Runner) Dump(ctx context.Context, snapshotID, path string) (io.ReadCloser, error) {
	args := []string{"dump", snapshotID, path}
	log.Debug().Strs("args", args).Msg("running restic")

	cmd := newCmd(ctx, args...)
	cmd.Stderr = os.Stderr

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

// runQuiet executes restic suppressing stdout and stderr.
// Used for probes where restic's output would confuse the user.
func (r *Runner) runQuiet(ctx context.Context, args ...string) error {
	log.Debug().Strs("args", args).Msg("running restic")

	cmd := newCmd(ctx, args...)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("restic %v: %w", args, err)
	}
	return nil
}

// run executes the restic binary with the given arguments.
// If stdin is non-nil it is connected to the command's stdin.
func (r *Runner) run(ctx context.Context, stdin io.Reader, args ...string) error {
	log.Debug().Strs("args", args).Msg("running restic")

	cmd := newCmd(ctx, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if stdin != nil {
		cmd.Stdin = stdin
	}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("restic %v: %w", args, err)
	}
	return nil
}

// runCapture executes restic and returns captured stdout bytes. If stdin is
// non-nil it is connected to the command's stdin. Captured output is
// returned even when the command fails, so callers can inspect partial
// output (e.g. a backup summary emitted before restic exited non-zero).
func (r *Runner) runCapture(ctx context.Context, stdin io.Reader, args ...string) ([]byte, error) {
	log.Debug().Strs("args", args).Msg("running restic")

	cmd := newCmd(ctx, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = os.Stderr

	if stdin != nil {
		cmd.Stdin = stdin
	}

	if err := cmd.Run(); err != nil {
		return buf.Bytes(), fmt.Errorf("restic %v: %w", args, err)
	}
	return buf.Bytes(), nil
}
