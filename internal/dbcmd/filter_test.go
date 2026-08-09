package dbcmd

import (
	"io"
	"strings"
	"testing"
)

func TestImportFilterPostgres(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		in   string
		want string
	}{
		{
			name: "strips connecting role's DROP and CREATE from preamble",
			env:  map[string]string{},
			in: "--\n" +
				"DROP DATABASE IF EXISTS appdb;\n" +
				"DROP ROLE IF EXISTS app;\n" +
				"DROP ROLE IF EXISTS postgres;\n" +
				"CREATE ROLE app;\n" +
				"CREATE ROLE postgres;\n" +
				"ALTER ROLE postgres WITH SUPERUSER;\n",
			want: "--\n" +
				"DROP DATABASE IF EXISTS appdb;\n" +
				"DROP ROLE IF EXISTS app;\n" +
				"CREATE ROLE app;\n" +
				"ALTER ROLE postgres WITH SUPERUSER;\n",
		},
		{
			name: "uses POSTGRES_USER for the role to strip",
			env:  map[string]string{"POSTGRES_USER": "app"},
			in: "DROP ROLE IF EXISTS app;\n" +
				"DROP ROLE IF EXISTS other;\n" +
				"CREATE ROLE app;\n" +
				"CREATE ROLE other;\n",
			want: "DROP ROLE IF EXISTS other;\n" +
				"CREATE ROLE other;\n",
		},
		{
			name: "matches the quoted identifier form",
			env:  map[string]string{"POSTGRES_USER": "my-user"},
			in: "DROP ROLE IF EXISTS \"my-user\";\n" +
				"CREATE ROLE \"my-user\";\n" +
				"CREATE ROLE keeper;\n",
			want: "CREATE ROLE keeper;\n",
		},
		{
			name: "strips unguarded DROP ROLE from --clean without --if-exists",
			env:  map[string]string{},
			in: "DROP ROLE postgres;\n" +
				"DROP ROLE app;\n",
			want: "DROP ROLE app;\n",
		},
		{
			name: "passes matching lines through untouched after first connect",
			env:  map[string]string{},
			in: "CREATE ROLE postgres;\n" +
				"\\connect appdb\n" +
				"COPY t (v) FROM stdin;\n" +
				"CREATE ROLE postgres;\n" +
				"\\.\n",
			want: "\\connect appdb\n" +
				"COPY t (v) FROM stdin;\n" +
				"CREATE ROLE postgres;\n" +
				"\\.\n",
		},
		{
			name: "handles missing trailing newline",
			env:  map[string]string{},
			in:   "ALTER ROLE postgres WITH SUPERUSER;\nCREATE ROLE postgres;",
			want: "ALTER ROLE postgres WITH SUPERUSER;\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := io.ReadAll(ImportFilter(strings.NewReader(tt.in), tt.env, "postgres"))
			if err != nil {
				t.Fatalf("reading filtered stream: %v", err)
			}
			if string(out) != tt.want {
				t.Errorf("filtered output:\n%q\nwant:\n%q", out, tt.want)
			}
		})
	}
}

func TestImportFilterPassthroughForMySQLFamily(t *testing.T) {
	for _, dbType := range []string{"mysql", "mariadb"} {
		in := strings.NewReader("DROP ROLE IF EXISTS postgres;\n")
		if got := ImportFilter(in, map[string]string{}, dbType); got != in {
			t.Errorf("ImportFilter(%s) wrapped the reader; want passthrough", dbType)
		}
	}
}
