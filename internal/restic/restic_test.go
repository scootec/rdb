package restic

import (
	"reflect"
	"testing"
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
