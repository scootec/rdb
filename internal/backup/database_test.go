package backup

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/scootec/rdb/internal/docker"
)

// fakeDumpStream simulates a docker exec whose stdout emits some bytes and
// whose command then reports the given exit code.
type fakeDumpStream struct {
	io.Reader
	stderr string
	execID string
}

func (f *fakeDumpStream) Stderr() string { return f.stderr }
func (f *fakeDumpStream) ExecID() string { return f.execID }

// fakeRestic records the restic interactions performed by streamDumpToRestic.
type fakeRestic struct {
	snapshotID string
	backupErr  error
	forgetErr  error
	tagErr     error

	backupFilename string
	backupTags     []string
	bytesStreamed  int64
	forgotten      []string
	tagged         []string
	addedTags      []string
}

func (f *fakeRestic) BackupFromStdin(_ context.Context, filename string, reader io.Reader, tags []string) (string, error) {
	f.backupFilename = filename
	f.backupTags = tags
	n, _ := io.Copy(io.Discard, reader)
	f.bytesStreamed = n
	return f.snapshotID, f.backupErr
}

func (f *fakeRestic) ForgetSnapshot(_ context.Context, snapshotID string) error {
	f.forgotten = append(f.forgotten, snapshotID)
	return f.forgetErr
}

func (f *fakeRestic) TagSnapshot(_ context.Context, snapshotID string, tags []string) error {
	f.tagged = append(f.tagged, snapshotID)
	f.addedTags = append(f.addedTags, tags...)
	return f.tagErr
}

func exitWith(code int, err error) func(context.Context, string) (int, error) {
	return func(context.Context, string) (int, error) { return code, err }
}

var testCtr = docker.ContainerInfo{Name: "myapp-db-1", Project: "myapp", Service: "db"}

func TestStreamDumpToResticSuccess(t *testing.T) {
	rc := &fakeRestic{snapshotID: "abc123"}
	stream := &fakeDumpStream{Reader: strings.NewReader("CREATE TABLE t;"), execID: "exec1"}

	err := streamDumpToRestic(context.Background(), stream, exitWith(0, nil), rc, testCtr, "postgres")
	if err != nil {
		t.Fatalf("streamDumpToRestic() error = %v, want nil", err)
	}
	if rc.bytesStreamed == 0 {
		t.Error("no bytes were streamed to restic")
	}
	if rc.backupFilename != "databases/myapp/db/postgres/all_databases.sql" {
		t.Errorf("backup filename = %q", rc.backupFilename)
	}
	if len(rc.forgotten) != 0 || len(rc.tagged) != 0 {
		t.Errorf("successful dump must not forget/tag snapshots, got forget=%v tag=%v", rc.forgotten, rc.tagged)
	}
}

// TestStreamDumpToResticDumpFails covers issue #1: a dump that emits bytes
// and then exits non-zero has already committed a restic snapshot; that
// snapshot must be deleted and the dump reported as failed.
func TestStreamDumpToResticDumpFails(t *testing.T) {
	rc := &fakeRestic{snapshotID: "abc123"}
	stream := &fakeDumpStream{
		Reader: strings.NewReader("CREATE TABLE t;\n-- truncated"),
		stderr: "mysqldump: Got error: 1146: Table 'app.orders' doesn't exist",
		execID: "exec1",
	}

	err := streamDumpToRestic(context.Background(), stream, exitWith(2, nil), rc, testCtr, "mysql")
	if err == nil {
		t.Fatal("streamDumpToRestic() = nil, want error for non-zero dump exit")
	}
	if !strings.Contains(err.Error(), "exited with code 2") {
		t.Errorf("error %q does not mention the exit code", err)
	}
	if !strings.Contains(err.Error(), "1146") {
		t.Errorf("error %q does not include the dump's stderr", err)
	}
	if !reflect.DeepEqual(rc.forgotten, []string{"abc123"}) {
		t.Errorf("forgotten = %v, want the created snapshot deleted", rc.forgotten)
	}
	if len(rc.tagged) != 0 {
		t.Errorf("tagged = %v, want no tagging when forget succeeds", rc.tagged)
	}
}

func TestStreamDumpToResticForgetFallsBackToPartialTag(t *testing.T) {
	rc := &fakeRestic{snapshotID: "abc123", forgetErr: errors.New("repo locked")}
	stream := &fakeDumpStream{Reader: strings.NewReader("data"), execID: "exec1"}

	err := streamDumpToRestic(context.Background(), stream, exitWith(1, nil), rc, testCtr, "mariadb")
	if err == nil {
		t.Fatal("streamDumpToRestic() = nil, want error")
	}
	if !reflect.DeepEqual(rc.forgotten, []string{"abc123"}) {
		t.Errorf("forgotten = %v, want forget attempted first", rc.forgotten)
	}
	if !reflect.DeepEqual(rc.tagged, []string{"abc123"}) {
		t.Errorf("tagged = %v, want partial tag fallback", rc.tagged)
	}
	if !reflect.DeepEqual(rc.addedTags, []string{"partial"}) {
		t.Errorf("addedTags = %v, want [partial]", rc.addedTags)
	}
}

func TestStreamDumpToResticForgetAndTagBothFail(t *testing.T) {
	rc := &fakeRestic{
		snapshotID: "abc123",
		forgetErr:  errors.New("repo locked"),
		tagErr:     errors.New("still locked"),
	}
	stream := &fakeDumpStream{Reader: strings.NewReader("data"), execID: "exec1"}

	err := streamDumpToRestic(context.Background(), stream, exitWith(1, nil), rc, testCtr, "mysql")
	if err == nil || !strings.Contains(err.Error(), "exited with code 1") {
		t.Fatalf("streamDumpToRestic() = %v, want dump failure error even when cleanup fails", err)
	}
}

func TestStreamDumpToResticUnknownSnapshotID(t *testing.T) {
	// Backup succeeded but restic's summary could not be parsed: there is
	// nothing to delete, but the dump failure must still be reported.
	rc := &fakeRestic{snapshotID: ""}
	stream := &fakeDumpStream{Reader: strings.NewReader("data"), execID: "exec1"}

	err := streamDumpToRestic(context.Background(), stream, exitWith(1, nil), rc, testCtr, "mysql")
	if err == nil {
		t.Fatal("streamDumpToRestic() = nil, want error")
	}
	if len(rc.forgotten) != 0 || len(rc.tagged) != 0 {
		t.Errorf("no snapshot ID known, but forget=%v tag=%v were called", rc.forgotten, rc.tagged)
	}
}

func TestStreamDumpToResticBackupErrorDiscardsCommittedSnapshot(t *testing.T) {
	// If restic reports an error but a snapshot was still committed (e.g.
	// the docker stream died after restic finished), it is discarded too.
	rc := &fakeRestic{snapshotID: "abc123", backupErr: errors.New("stdin read failed")}
	stream := &fakeDumpStream{Reader: strings.NewReader("data"), execID: "exec1"}

	err := streamDumpToRestic(context.Background(), stream, exitWith(0, nil), rc, testCtr, "postgres")
	if err == nil || !strings.Contains(err.Error(), "restic backup stdin") {
		t.Fatalf("streamDumpToRestic() = %v, want restic backup error", err)
	}
	if !reflect.DeepEqual(rc.forgotten, []string{"abc123"}) {
		t.Errorf("forgotten = %v, want committed snapshot discarded", rc.forgotten)
	}
}

func TestStreamDumpToResticInspectErrorDoesNotFail(t *testing.T) {
	// Preserve existing behaviour: an uninspectable exit code logs a
	// warning but does not fail the backup.
	rc := &fakeRestic{snapshotID: "abc123"}
	stream := &fakeDumpStream{Reader: strings.NewReader("data"), execID: "exec1"}

	err := streamDumpToRestic(context.Background(), stream, exitWith(-1, errors.New("inspect failed")), rc, testCtr, "postgres")
	if err != nil {
		t.Fatalf("streamDumpToRestic() error = %v, want nil when exit code cannot be inspected", err)
	}
	if len(rc.forgotten) != 0 || len(rc.tagged) != 0 {
		t.Errorf("forget=%v tag=%v, want no cleanup", rc.forgotten, rc.tagged)
	}
}

func TestBuildDBFilename(t *testing.T) {
	tests := []struct {
		name   string
		ctr    docker.ContainerInfo
		dbType string
		want   string
	}{
		{
			name:   "project and service present",
			ctr:    docker.ContainerInfo{Name: "myapp-db-1", Project: "myapp", Service: "db"},
			dbType: "postgres",
			want:   "databases/myapp/db/postgres/all_databases.sql",
		},
		{
			name:   "no project omits the project segment",
			ctr:    docker.ContainerInfo{Name: "standalone-db", Service: "db"},
			dbType: "mysql",
			want:   "databases/db/mysql/all_databases.sql",
		},
		{
			name:   "no service falls back to container name",
			ctr:    docker.ContainerInfo{Name: "standalone-db", Project: "myapp"},
			dbType: "mariadb",
			want:   "databases/myapp/standalone-db/mariadb/all_databases.sql",
		},
		{
			name:   "neither project nor service",
			ctr:    docker.ContainerInfo{Name: "plain"},
			dbType: "postgres",
			want:   "databases/plain/postgres/all_databases.sql",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildDBFilename(tt.ctr, tt.dbType); got != tt.want {
				t.Errorf("buildDBFilename() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildTags(t *testing.T) {
	tests := []struct {
		name      string
		ctr       docker.ContainerInfo
		component string
		want      []string
	}{
		{
			name:      "project and service",
			ctr:       docker.ContainerInfo{Name: "myapp-db-1", Project: "myapp", Service: "db"},
			component: "postgres",
			want:      []string{"rdb", "postgres", "project:myapp", "service:db"},
		},
		{
			name:      "no project omits project tag",
			ctr:       docker.ContainerInfo{Name: "standalone", Service: "db"},
			component: "volume",
			want:      []string{"rdb", "volume", "service:db"},
		},
		{
			name:      "no service falls back to container name",
			ctr:       docker.ContainerInfo{Name: "standalone"},
			component: "volume",
			want:      []string{"rdb", "volume", "service:standalone"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildTags(tt.ctr, tt.component); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("buildTags() = %v, want %v", got, tt.want)
			}
		})
	}
}
