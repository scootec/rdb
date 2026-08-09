// Package health records the outcome of scheduled runs to a state file,
// evaluates it for the `rdb health` command / Docker HEALTHCHECK, and pings a
// healthchecks.io-compatible URL (hosted or self-hosted) after each run.
package health

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// Result records the outcome of a job's most recent run.
type Result struct {
	Timestamp time.Time `json:"timestamp"`
	OK        bool      `json:"ok"`
	Error     string    `json:"error,omitempty"`
	// Pending marks a job that is scheduled but has not completed a run yet;
	// Timestamp then holds the scheduler start time, so overdue detection
	// covers jobs that never manage to run at all.
	Pending bool `json:"pending,omitempty"`
}

// State maps job names ("backup", "maintenance") to their latest results.
type State map[string]Result

// ReadState loads the state file. A missing file is an error: the scheduler
// writes pending entries at startup, so absence means no scheduler is running.
func ReadState(path string) (State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("corrupt state file %s: %w", path, err)
	}
	return s, nil
}

// MarkPending registers scheduled jobs at scheduler startup. Jobs that already
// have a recorded result keep it, so a container restart does not hide an
// earlier failure while /tmp persists.
func MarkPending(path string, now time.Time, jobs ...string) error {
	s, err := ReadState(path)
	if err != nil {
		s = State{}
	}
	for _, job := range jobs {
		if _, exists := s[job]; !exists {
			s[job] = Result{Timestamp: now, OK: true, Pending: true}
		}
	}
	return writeState(path, s)
}

// Record stores the outcome of a completed run.
func Record(path, job string, runErr error, now time.Time) error {
	s, err := ReadState(path)
	if err != nil {
		s = State{}
	}
	r := Result{Timestamp: now, OK: runErr == nil}
	if runErr != nil {
		r.Error = runErr.Error()
	}
	s[job] = r
	return writeState(path, s)
}

// writeState writes atomically (temp file + rename) so a concurrent
// `rdb health` never observes a partial file.
func writeState(path string, s State) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".rdb-status-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Evaluate reports why the last runs are unhealthy, or nil if all is well.
// A job is unhealthy when its last run failed, or when its next scheduled run
// after the recorded timestamp is more than grace past due. Jobs without an
// entry in schedules (or with an unparseable schedule) skip the overdue check.
func Evaluate(s State, schedules map[string]string, grace time.Duration, now time.Time) error {
	jobs := make([]string, 0, len(s))
	for job := range s {
		jobs = append(jobs, job)
	}
	sort.Strings(jobs)

	var problems []string
	for _, job := range jobs {
		r := s[job]
		if !r.Pending && !r.OK {
			problems = append(problems, fmt.Sprintf("last %s run failed: %s", job, r.Error))
		}
		spec, ok := schedules[job]
		if !ok || spec == "" {
			continue
		}
		sched, err := cron.ParseStandard(spec)
		if err != nil {
			continue
		}
		due := sched.Next(r.Timestamp)
		if now.After(due.Add(grace)) {
			problems = append(problems, fmt.Sprintf("%s run overdue: was due %s, grace %s",
				job, due.Format(time.RFC3339), grace))
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}
