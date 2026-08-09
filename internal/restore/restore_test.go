package restore

import (
	"reflect"
	"testing"

	"github.com/scootec/rdb/internal/docker"
	"github.com/scootec/rdb/internal/restic"
)

func snapWithTags(tags ...string) restic.Snapshot {
	return restic.Snapshot{ShortID: "abcd1234", ID: "abcd1234full", Tags: tags}
}

func TestSnapshotDBType(t *testing.T) {
	tests := []struct {
		name string
		snap restic.Snapshot
		want string
	}{
		{"postgres tag", snapWithTags("rdb", "postgres", "service:db"), "postgres"},
		{"mysql tag", snapWithTags("rdb", "mysql"), "mysql"},
		{"mariadb tag", snapWithTags("rdb", "mariadb"), "mariadb"},
		{"volume snapshot has no db type", snapWithTags("rdb", "volume", "service:app"), ""},
		{"no tags", snapWithTags(), ""},
		{"first db tag wins", snapWithTags("mysql", "postgres"), "mysql"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := snapshotDBType(tt.snap); got != tt.want {
				t.Errorf("snapshotDBType(%v) = %q, want %q", tt.snap.Tags, got, tt.want)
			}
		})
	}
}

func TestHasTag(t *testing.T) {
	tests := []struct {
		name string
		snap restic.Snapshot
		tag  string
		want bool
	}{
		{"tag present", snapWithTags("rdb", "volume"), "volume", true},
		{"tag absent", snapWithTags("rdb", "postgres"), "volume", false},
		{"no tags", snapWithTags(), "volume", false},
		{"exact match only", snapWithTags("volumes"), "volume", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasTag(tt.snap, tt.tag); got != tt.want {
				t.Errorf("hasTag(%v, %q) = %v, want %v", tt.snap.Tags, tt.tag, got, tt.want)
			}
		})
	}
}

func TestParseTagValue(t *testing.T) {
	tests := []struct {
		name   string
		snap   restic.Snapshot
		prefix string
		want   string
	}{
		{"project tag", snapWithTags("rdb", "project:myapp", "service:db"), "project", "myapp"},
		{"service tag", snapWithTags("rdb", "project:myapp", "service:db"), "service", "db"},
		{"missing prefix", snapWithTags("rdb", "volume"), "project", ""},
		{"value containing colon is kept whole", snapWithTags("service:db:primary"), "service", "db:primary"},
		{"empty value", snapWithTags("project:"), "project", ""},
		{"prefix without colon does not match", snapWithTags("project"), "project", ""},
		{"first matching tag wins", snapWithTags("service:a", "service:b"), "service", "a"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseTagValue(tt.snap, tt.prefix); got != tt.want {
				t.Errorf("parseTagValue(%v, %q) = %q, want %q", tt.snap.Tags, tt.prefix, got, tt.want)
			}
		})
	}
}

func TestBuildImportCmd(t *testing.T) {
	tests := []struct {
		name         string
		env          map[string]string
		dbType       string
		wantCmd      []string
		wantExtraEnv []string
	}{
		{
			name:         "postgres defaults to postgres user",
			env:          map[string]string{},
			dbType:       "postgres",
			wantCmd:      []string{"psql", "--set", "ON_ERROR_STOP=on", "-U", "postgres"},
			wantExtraEnv: nil,
		},
		{
			name:         "postgres with user and password",
			env:          map[string]string{"POSTGRES_USER": "app", "POSTGRES_PASSWORD": "s3cret"},
			dbType:       "postgres",
			wantCmd:      []string{"psql", "--set", "ON_ERROR_STOP=on", "-U", "app"},
			wantExtraEnv: []string{"PGPASSWORD=s3cret"},
		},
		{
			name:         "mysql root with password",
			env:          map[string]string{"MYSQL_ROOT_PASSWORD": "rootpw"},
			dbType:       "mysql",
			wantCmd:      []string{"mysql", "--user=root"},
			wantExtraEnv: []string{"MYSQL_PWD=rootpw"},
		},
		{
			name:         "mysql falls back to MYSQL_USER",
			env:          map[string]string{"MYSQL_USER": "app", "MYSQL_PASSWORD": "apppw"},
			dbType:       "mysql",
			wantCmd:      []string{"mysql", "--user=app"},
			wantExtraEnv: []string{"MYSQL_PWD=apppw"},
		},
		{
			// Documents current behaviour: unlike the backup side, the restore
			// side passes the root password instead of using socket auth
			// (see issue #9).
			name:         "mariadb root with password",
			env:          map[string]string{"MARIADB_ROOT_PASSWORD": "rootpw"},
			dbType:       "mariadb",
			wantCmd:      []string{"mariadb", "--user=root"},
			wantExtraEnv: []string{"MYSQL_PWD=rootpw"},
		},
		{
			name:         "mariadb falls back to MARIADB_USER",
			env:          map[string]string{"MARIADB_USER": "app", "MARIADB_PASSWORD": "apppw"},
			dbType:       "mariadb",
			wantCmd:      []string{"mariadb", "--user=app"},
			wantExtraEnv: []string{"MYSQL_PWD=apppw"},
		},
		{
			// Documents current behaviour: mariadb restore does not fall back
			// to MYSQL_USER/MYSQL_PASSWORD the way the backup side does.
			name:         "mariadb ignores MYSQL_USER fallback",
			env:          map[string]string{"MYSQL_USER": "app", "MYSQL_PASSWORD": "apppw"},
			dbType:       "mariadb",
			wantCmd:      []string{"mariadb", "--user="},
			wantExtraEnv: nil,
		},
		{
			name:         "unknown db type yields nil cmd",
			env:          map[string]string{},
			dbType:       "oracle",
			wantCmd:      nil,
			wantExtraEnv: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctr := &docker.ContainerInfo{Name: "test", Env: tt.env}
			cmd, extraEnv := buildImportCmd(ctr, tt.dbType)
			if !reflect.DeepEqual(cmd, tt.wantCmd) {
				t.Errorf("buildImportCmd() cmd = %v, want %v", cmd, tt.wantCmd)
			}
			if !reflect.DeepEqual(extraEnv, tt.wantExtraEnv) {
				t.Errorf("buildImportCmd() extraEnv = %v, want %v", extraEnv, tt.wantExtraEnv)
			}
		})
	}
}
