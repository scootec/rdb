package dbcmd

import (
	"reflect"
	"testing"
)

func TestDumpCmd(t *testing.T) {
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
			name:         "mariadb mixes MARIADB_USER with MYSQL_PASSWORD",
			env:          map[string]string{"MARIADB_USER": "app", "MYSQL_PASSWORD": "apppw"},
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
			cmd, extraEnv, err := DumpCmd(tt.env, tt.dbType)
			if (err != nil) != tt.wantErr {
				t.Fatalf("DumpCmd() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if !reflect.DeepEqual(cmd, tt.wantCmd) {
				t.Errorf("DumpCmd() cmd = %v, want %v", cmd, tt.wantCmd)
			}
			if !reflect.DeepEqual(extraEnv, tt.wantExtraEnv) {
				t.Errorf("DumpCmd() extraEnv = %v, want %v", extraEnv, tt.wantExtraEnv)
			}
		})
	}
}

func TestImportCmd(t *testing.T) {
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
			wantCmd:      []string{"psql", "--set", "ON_ERROR_STOP=on", "-U", "postgres"},
			wantExtraEnv: nil,
		},
		{
			name:         "postgres uses POSTGRES_USER and POSTGRES_PASSWORD",
			env:          map[string]string{"POSTGRES_USER": "app", "POSTGRES_PASSWORD": "s3cret"},
			dbType:       "postgres",
			wantCmd:      []string{"psql", "--set", "ON_ERROR_STOP=on", "-U", "app"},
			wantExtraEnv: []string{"PGPASSWORD=s3cret"},
		},
		{
			name:         "mysql uses root with MYSQL_ROOT_PASSWORD",
			env:          map[string]string{"MYSQL_ROOT_PASSWORD": "rootpw"},
			dbType:       "mysql",
			wantCmd:      []string{"mysql", "--user=root"},
			wantExtraEnv: []string{"MYSQL_PWD=rootpw"},
		},
		{
			name:         "mysql falls back to MYSQL_USER when no root password",
			env:          map[string]string{"MYSQL_USER": "app", "MYSQL_PASSWORD": "apppw"},
			dbType:       "mysql",
			wantCmd:      []string{"mysql", "--user=app"},
			wantExtraEnv: []string{"MYSQL_PWD=apppw"},
		},
		{
			// Socket auth: root password env present → exec as root with NO
			// password, matching the backup side (issue #9).
			name:         "mariadb with MARIADB_ROOT_PASSWORD uses root via socket auth",
			env:          map[string]string{"MARIADB_ROOT_PASSWORD": "rootpw"},
			dbType:       "mariadb",
			wantCmd:      []string{"mariadb", "--user=root"},
			wantExtraEnv: nil,
		},
		{
			name:         "mariadb with MYSQL_ROOT_PASSWORD also uses root via socket auth",
			env:          map[string]string{"MYSQL_ROOT_PASSWORD": "rootpw"},
			dbType:       "mariadb",
			wantCmd:      []string{"mariadb", "--user=root"},
			wantExtraEnv: nil,
		},
		{
			name:         "mariadb falls back to MARIADB_USER with password auth",
			env:          map[string]string{"MARIADB_USER": "app", "MARIADB_PASSWORD": "apppw"},
			dbType:       "mariadb",
			wantCmd:      []string{"mariadb", "--user=app"},
			wantExtraEnv: []string{"MYSQL_PWD=apppw"},
		},
		{
			name:         "mariadb falls back to MYSQL_USER and MYSQL_PASSWORD",
			env:          map[string]string{"MYSQL_USER": "app", "MYSQL_PASSWORD": "apppw"},
			dbType:       "mariadb",
			wantCmd:      []string{"mariadb", "--user=app"},
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
			cmd, extraEnv, err := ImportCmd(tt.env, tt.dbType)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ImportCmd() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if !reflect.DeepEqual(cmd, tt.wantCmd) {
				t.Errorf("ImportCmd() cmd = %v, want %v", cmd, tt.wantCmd)
			}
			if !reflect.DeepEqual(extraEnv, tt.wantExtraEnv) {
				t.Errorf("ImportCmd() extraEnv = %v, want %v", extraEnv, tt.wantExtraEnv)
			}
		})
	}
}
