package backup

import (
	"reflect"
	"testing"

	"github.com/scootec/rdb/internal/docker"
)

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

func TestBuildDumpCmd(t *testing.T) {
	tests := []struct {
		name         string
		env          map[string]string
		dbType       string
		wantCmd      []string
		wantExtraEnv []string
		wantErr      bool
	}{
		{
			name:         "postgres defaults to postgres user without password",
			env:          map[string]string{},
			dbType:       "postgres",
			wantCmd:      []string{"pg_dumpall", "-U", "postgres"},
			wantExtraEnv: nil,
		},
		{
			name:         "postgres uses POSTGRES_USER and POSTGRES_PASSWORD",
			env:          map[string]string{"POSTGRES_USER": "app", "POSTGRES_PASSWORD": "s3cret"},
			dbType:       "postgres",
			wantCmd:      []string{"pg_dumpall", "-U", "app"},
			wantExtraEnv: []string{"PGPASSWORD=s3cret"},
		},
		{
			name:         "mysql uses root with MYSQL_ROOT_PASSWORD",
			env:          map[string]string{"MYSQL_ROOT_PASSWORD": "rootpw"},
			dbType:       "mysql",
			wantCmd:      []string{"mysqldump", "--user=root", "--all-databases", "--single-transaction", "--compact", "--force"},
			wantExtraEnv: []string{"MYSQL_PWD=rootpw"},
		},
		{
			name:         "mysql falls back to MYSQL_USER when no root password",
			env:          map[string]string{"MYSQL_USER": "app", "MYSQL_PASSWORD": "apppw"},
			dbType:       "mysql",
			wantCmd:      []string{"mysqldump", "--user=app", "--all-databases", "--single-transaction", "--compact", "--force"},
			wantExtraEnv: []string{"MYSQL_PWD=apppw"},
		},
		{
			name:         "mysql with no credentials at all uses empty user, no password",
			env:          map[string]string{},
			dbType:       "mysql",
			wantCmd:      []string{"mysqldump", "--user=", "--all-databases", "--single-transaction", "--compact", "--force"},
			wantExtraEnv: nil,
		},
		{
			// Socket auth: root password env present → exec as root with NO password.
			name:         "mariadb with MARIADB_ROOT_PASSWORD uses root via socket auth",
			env:          map[string]string{"MARIADB_ROOT_PASSWORD": "rootpw"},
			dbType:       "mariadb",
			wantCmd:      []string{"mariadb-dump", "--user=root", "--all-databases", "--single-transaction", "--compact", "--force"},
			wantExtraEnv: nil,
		},
		{
			name:         "mariadb with MYSQL_ROOT_PASSWORD also uses root via socket auth",
			env:          map[string]string{"MYSQL_ROOT_PASSWORD": "rootpw"},
			dbType:       "mariadb",
			wantCmd:      []string{"mariadb-dump", "--user=root", "--all-databases", "--single-transaction", "--compact", "--force"},
			wantExtraEnv: nil,
		},
		{
			name:         "mariadb falls back to MARIADB_USER with password auth",
			env:          map[string]string{"MARIADB_USER": "app", "MARIADB_PASSWORD": "apppw"},
			dbType:       "mariadb",
			wantCmd:      []string{"mariadb-dump", "--user=app", "--all-databases", "--single-transaction", "--compact", "--force"},
			wantExtraEnv: []string{"MYSQL_PWD=apppw"},
		},
		{
			name:         "mariadb falls back to MYSQL_USER and MYSQL_PASSWORD",
			env:          map[string]string{"MYSQL_USER": "app", "MYSQL_PASSWORD": "apppw"},
			dbType:       "mariadb",
			wantCmd:      []string{"mariadb-dump", "--user=app", "--all-databases", "--single-transaction", "--compact", "--force"},
			wantExtraEnv: []string{"MYSQL_PWD=apppw"},
		},
		{
			name:    "unknown database type returns an error",
			env:     map[string]string{},
			dbType:  "oracle",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctr := docker.ContainerInfo{Name: "test", Env: tt.env}
			cmd, extraEnv, err := buildDumpCmd(ctr, tt.dbType)
			if (err != nil) != tt.wantErr {
				t.Fatalf("buildDumpCmd() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if !reflect.DeepEqual(cmd, tt.wantCmd) {
				t.Errorf("buildDumpCmd() cmd = %v, want %v", cmd, tt.wantCmd)
			}
			if !reflect.DeepEqual(extraEnv, tt.wantExtraEnv) {
				t.Errorf("buildDumpCmd() extraEnv = %v, want %v", extraEnv, tt.wantExtraEnv)
			}
		})
	}
}
