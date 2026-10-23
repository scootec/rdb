package main

import (
	"reflect"
	"testing"

	"github.com/scootec/rdb/internal/restore"
)

func TestParseRestoreArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    restore.Options
		wantErr bool
	}{
		{"snapshot only", []string{"abc123"}, restore.Options{SnapshotID: "abc123"}, false},
		{"output with separate value", []string{"abc123", "--output", "/tmp/dump.sql"},
			restore.Options{SnapshotID: "abc123", OutputPath: "/tmp/dump.sql"}, false},
		{"output with equals", []string{"abc123", "--output=/tmp/dump.sql"},
			restore.Options{SnapshotID: "abc123", OutputPath: "/tmp/dump.sql"}, false},
		{"flags before snapshot", []string{"--stop", "abc123", "--force"},
			restore.Options{SnapshotID: "abc123", Stop: true, Force: true}, false},
		{"missing snapshot", nil, restore.Options{}, true},
		{"flags but no snapshot", []string{"--stop"}, restore.Options{}, true},
		{"output without value", []string{"abc123", "--output"}, restore.Options{}, true},
		{"output with empty equals", []string{"abc123", "--output="}, restore.Options{}, true},
		{"output followed by a flag", []string{"abc123", "--output", "--force"}, restore.Options{}, true},
		{"misspelled flag", []string{"abc123", "--ouput", "/tmp/dump.sql"}, restore.Options{}, true},
		{"single-dash flag", []string{"abc123", "-o", "/tmp/dump.sql"}, restore.Options{}, true},
		{"second positional", []string{"abc123", "def456"}, restore.Options{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRestoreArgs(tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseRestoreArgs(%v) error = %v, wantErr %v", tt.args, err, tt.wantErr)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseRestoreArgs(%v) = %+v, want %+v", tt.args, got, tt.want)
			}
		})
	}
}
