package backup

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/scootec/rdb/internal/docker"
)

type hookCall struct {
	cmd       []string
	ctxErr    error
	deadline  bool
	remaining time.Duration
}

// fakeHookExecutor records every ExecCommand call and fails those whose
// script matches failScript.
type fakeHookExecutor struct {
	calls      []hookCall
	failScript string
}

func (f *fakeHookExecutor) ExecCommand(ctx context.Context, _ string, cmd []string) error {
	call := hookCall{cmd: cmd, ctxErr: ctx.Err()}
	if dl, ok := ctx.Deadline(); ok {
		call.deadline = true
		call.remaining = time.Until(dl)
	}
	f.calls = append(f.calls, call)
	if len(cmd) == 3 && cmd[2] == f.failScript {
		return errors.New("boom")
	}
	return nil
}

func (f *fakeHookExecutor) scripts() []string {
	var s []string
	for _, c := range f.calls {
		s = append(s, c.cmd[len(c.cmd)-1])
	}
	return s
}

func testContainer(pre, post string) docker.ContainerInfo {
	return docker.ContainerInfo{
		ID:                "abc",
		Name:              "app",
		PreBackupCmd:      pre,
		PreBackupTimeout:  time.Minute,
		PostBackupCmd:     post,
		PostBackupTimeout: time.Minute,
	}
}

func TestRunWithHooks(t *testing.T) {
	backupErr := errors.New("backup failed")

	tests := []struct {
		name        string
		ctr         docker.ContainerInfo
		failScript  string
		backupErr   error
		wantScripts []string
		wantBackups bool
		wantErr     bool
	}{
		{
			name:        "no hooks just runs backups",
			ctr:         testContainer("", ""),
			wantScripts: nil,
			wantBackups: true,
		},
		{
			name:        "pre and post wrap the backups",
			ctr:         testContainer("make-export", "rm-export"),
			wantScripts: []string{"make-export", "rm-export"},
			wantBackups: true,
		},
		{
			name:        "pre failure skips backups but still runs post",
			ctr:         testContainer("make-export", "rm-export"),
			failScript:  "make-export",
			wantScripts: []string{"make-export", "rm-export"},
			wantBackups: false,
			wantErr:     true,
		},
		{
			name:        "backup failure still runs post",
			ctr:         testContainer("make-export", "rm-export"),
			backupErr:   backupErr,
			wantScripts: []string{"make-export", "rm-export"},
			wantBackups: true,
			wantErr:     true,
		},
		{
			name:        "post failure fails the run even when backups succeed",
			ctr:         testContainer("", "rm-export"),
			failScript:  "rm-export",
			wantScripts: []string{"rm-export"},
			wantBackups: true,
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hx := &fakeHookExecutor{failScript: tt.failScript}
			backupsRan := false
			err := runWithHooks(context.Background(), hx, tt.ctr, func(context.Context) error {
				backupsRan = true
				return tt.backupErr
			})

			if (err != nil) != tt.wantErr {
				t.Errorf("runWithHooks() error = %v, wantErr %v", err, tt.wantErr)
			}
			if backupsRan != tt.wantBackups {
				t.Errorf("backups ran = %v, want %v", backupsRan, tt.wantBackups)
			}
			if got := hx.scripts(); !reflect.DeepEqual(got, tt.wantScripts) {
				t.Errorf("scripts run = %v, want %v", got, tt.wantScripts)
			}
			if tt.backupErr != nil && !errors.Is(err, tt.backupErr) {
				t.Errorf("runWithHooks() error = %v, want it to wrap %v", err, tt.backupErr)
			}
		})
	}
}

func TestRunWithHooksScriptsRunViaShell(t *testing.T) {
	hx := &fakeHookExecutor{}
	ctr := testContainer("make-export", "")
	if err := runWithHooks(context.Background(), hx, ctr, func(context.Context) error { return nil }); err != nil {
		t.Fatalf("runWithHooks() error = %v", err)
	}
	want := []string{"sh", "-c", "make-export"}
	if len(hx.calls) != 1 || !reflect.DeepEqual(hx.calls[0].cmd, want) {
		t.Errorf("exec cmd = %v, want %v", hx.calls, want)
	}
}

func TestRunWithHooksPostRunsDetachedFromCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	hx := &fakeHookExecutor{}
	ctr := testContainer("", "rm-export")
	err := runWithHooks(ctx, hx, ctr, func(ctx context.Context) error { return ctx.Err() })

	if err == nil {
		t.Fatal("runWithHooks() = nil, want the cancelled backup's error")
	}
	if len(hx.calls) != 1 {
		t.Fatalf("post script calls = %d, want 1", len(hx.calls))
	}
	if hx.calls[0].ctxErr != nil {
		t.Errorf("post script ctx.Err() = %v, want nil (detached from run cancellation)", hx.calls[0].ctxErr)
	}
}

func TestRunHookScriptTimeouts(t *testing.T) {
	tests := []struct {
		name    string
		timeout time.Duration
		want    time.Duration
	}{
		{"configured timeout is applied", time.Minute, time.Minute},
		{"zero falls back to default", 0, docker.DefaultHookTimeout},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hx := &fakeHookExecutor{}
			ctr := testContainer("make-export", "")
			if err := runHookScript(context.Background(), hx, ctr, "pre-backup", ctr.PreBackupCmd, tt.timeout); err != nil {
				t.Fatalf("runHookScript() error = %v", err)
			}
			call := hx.calls[0]
			if !call.deadline {
				t.Fatal("hook context has no deadline")
			}
			if call.remaining > tt.want || call.remaining < tt.want-10*time.Second {
				t.Errorf("hook deadline %v from now, want ~%v", call.remaining, tt.want)
			}
		})
	}
}
