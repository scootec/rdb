package restic

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestParseBackupSummary(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want string
	}{
		{
			"status lines then summary",
			`{"message_type":"status","percent_done":0.5}
{"message_type":"status","percent_done":1}
{"message_type":"summary","files_new":1,"total_bytes_processed":1024,"snapshot_id":"605b3eee12345678"}
`,
			"605b3eee12345678",
		},
		{
			"summary only",
			`{"message_type":"summary","snapshot_id":"abc123"}`,
			"abc123",
		},
		{
			"no summary message",
			`{"message_type":"status","percent_done":1}`,
			"",
		},
		{
			"empty output",
			"",
			"",
		},
		{
			"non-JSON noise is skipped",
			"warning: something\n{\"message_type\":\"summary\",\"snapshot_id\":\"abc123\"}\n",
			"abc123",
		},
		{
			"summary without snapshot ID",
			`{"message_type":"summary","files_new":1}`,
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseBackupSummary([]byte(tt.out)); got != tt.want {
				t.Errorf("parseBackupSummary() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTagAddArgs(t *testing.T) {
	got := tagAddArgs("abc123", []string{"partial"})
	want := []string{"tag", "--add", "partial", "abc123"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tagAddArgs() = %v, want %v", got, want)
	}
}

func TestExcludePartial(t *testing.T) {
	snaps := []Snapshot{
		{ShortID: "good1", Tags: []string{"rdb", "postgres"}},
		{ShortID: "bad", Tags: []string{"rdb", "mysql", TagPartial}},
		{ShortID: "good2", Tags: []string{"rdb", "volume"}},
	}
	got := excludePartial(snaps)
	if len(got) != 2 || got[0].ShortID != "good1" || got[1].ShortID != "good2" {
		t.Errorf("excludePartial() = %v, want partial-tagged snapshot removed", got)
	}
}

func TestSnapshotHasTag(t *testing.T) {
	snap := Snapshot{Tags: []string{"rdb", "mysql"}}
	if !snap.HasTag("mysql") {
		t.Error("HasTag(mysql) = false, want true")
	}
	if snap.HasTag(TagPartial) {
		t.Error("HasTag(partial) = true, want false")
	}
}

func TestBackupDirArgs(t *testing.T) {
	tests := []struct {
		name string
		path string
		host string
		tags []string
		want []string
	}{
		{
			"host and tags",
			"/var/lib/docker/volumes/myapp_data/_data", "rdb",
			[]string{"rdb", "volume", "project:myapp", "service:app"},
			[]string{
				"backup", "/var/lib/docker/volumes/myapp_data/_data",
				"--host", "rdb",
				"--tag", "rdb", "--tag", "volume", "--tag", "project:myapp", "--tag", "service:app",
			},
		},
		{
			"empty host omits --host",
			"/data", "", []string{"rdb"},
			[]string{"backup", "/data", "--tag", "rdb"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := backupDirArgs(tt.path, tt.host, tt.tags); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("backupDirArgs(%q, %q, %v) = %v, want %v", tt.path, tt.host, tt.tags, got, tt.want)
			}
		})
	}
}

func TestBackupStdinArgs(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		host     string
		tags     []string
		want     []string
	}{
		{
			"host and tags",
			"databases/myapp/db/postgres/all_databases.sql", "rdb",
			[]string{"rdb", "postgres", "project:myapp", "service:db"},
			[]string{
				"backup", "--json", "--stdin", "--stdin-filename", "databases/myapp/db/postgres/all_databases.sql",
				"--host", "rdb",
				"--tag", "rdb", "--tag", "postgres", "--tag", "project:myapp", "--tag", "service:db",
			},
		},
		{
			"empty host omits --host",
			"dump.sql", "", nil,
			[]string{"backup", "--json", "--stdin", "--stdin-filename", "dump.sql"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := backupStdinArgs(tt.filename, tt.host, tt.tags); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("backupStdinArgs(%q, %q, %v) = %v, want %v", tt.filename, tt.host, tt.tags, got, tt.want)
			}
		})
	}
}

func TestForgetArgs(t *testing.T) {
	tests := []struct {
		name   string
		policy RetentionPolicy
		want   []string
	}{
		{
			"base policy scopes to rdb tag and groups by paths,tags",
			RetentionPolicy{Daily: 7, Weekly: 4, Monthly: 12, Yearly: 3},
			[]string{
				"forget",
				"--tag", "rdb",
				"--group-by", "paths,tags",
				"--keep-daily", "7",
				"--keep-weekly", "4",
				"--keep-monthly", "12",
				"--keep-yearly", "3",
			},
		},
		{
			"optional keeps appended when set",
			RetentionPolicy{Daily: 7, Weekly: 4, Monthly: 12, Yearly: 3, Last: 5, Hourly: 24, Within: "30d"},
			[]string{
				"forget",
				"--tag", "rdb",
				"--group-by", "paths,tags",
				"--keep-daily", "7",
				"--keep-weekly", "4",
				"--keep-monthly", "12",
				"--keep-yearly", "3",
				"--keep-last", "5",
				"--keep-hourly", "24",
				"--keep-within", "30d",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := forgetArgs(tt.policy); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("forgetArgs(%+v) = %v, want %v", tt.policy, got, tt.want)
			}
		})
	}
}

func TestRestoreArgs(t *testing.T) {
	tests := []struct {
		name       string
		snapshotID string
		target     string
		includes   []string
		want       []string
	}{
		{
			"no includes",
			"abc123", "/", nil,
			[]string{"restore", "abc123", "--target", "/", "--verify"},
		},
		{
			"single include scopes the restore",
			"abc123", "/", []string{"/var/lib/docker/volumes/myapp_data/_data"},
			[]string{"restore", "abc123", "--target", "/", "--verify", "--include", "/var/lib/docker/volumes/myapp_data/_data"},
		},
		{
			"multiple includes",
			"abc123", "/", []string{"/data/a", "/data/b"},
			[]string{"restore", "abc123", "--target", "/", "--verify", "--include", "/data/a", "--include", "/data/b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := restoreArgs(tt.snapshotID, tt.target, tt.includes); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("restoreArgs(%q, %q, %v) = %v, want %v", tt.snapshotID, tt.target, tt.includes, got, tt.want)
			}
		})
	}
}

func TestRunCancellationAbortsCommand(t *testing.T) {
	orig := resticBinary
	resticBinary = "sleep"
	defer func() { resticBinary = orig }()

	r := New("")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- r.run(ctx, nil, "60") }()

	// Give the command a moment to start before cancelling.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("run() = nil, want error after cancellation")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run() still blocked 5s after cancellation — context does not abort the command")
	}
}

func TestRunCapturePreexpiredContextFails(t *testing.T) {
	orig := resticBinary
	resticBinary = "sleep"
	defer func() { resticBinary = orig }()

	r := New("")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	if _, err := r.runCapture(ctx, nil, "60"); err == nil {
		t.Fatal("runCapture() = nil, want error for already-cancelled context")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("runCapture() took %v with a cancelled context, want fast failure", elapsed)
	}
}
