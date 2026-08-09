// Package dbcmd builds database dump and import commands with credential
// selection shared between backup and restore, so the two sides cannot
// diverge in how they authenticate against the same container.
package dbcmd

import "fmt"

// credentials holds the user and password selected from a container's
// environment variables. An empty password means no password is passed to
// the client (e.g. unix-socket auth).
type credentials struct {
	user     string
	password string
}

// postgresCreds selects credentials for the official postgres image.
func postgresCreds(env map[string]string) credentials {
	user := env["POSTGRES_USER"]
	if user == "" {
		user = "postgres"
	}
	return credentials{user: user, password: env["POSTGRES_PASSWORD"]}
}

// mysqlCreds selects credentials for the official mysql image: root with
// MYSQL_ROOT_PASSWORD when set, otherwise MYSQL_USER/MYSQL_PASSWORD.
func mysqlCreds(env map[string]string) credentials {
	if password := env["MYSQL_ROOT_PASSWORD"]; password != "" {
		return credentials{user: "root", password: password}
	}
	return credentials{user: env["MYSQL_USER"], password: env["MYSQL_PASSWORD"]}
}

// mariadbCreds selects credentials for the official mariadb image.
// MariaDB images typically configure root with unix socket auth, so
// password-based root login fails. When a root password env var is present
// we know MariaDB is installed and can use socket auth by exec'ing as root
// without a password. Only fall back to password auth for non-root users.
func mariadbCreds(env map[string]string) credentials {
	if env["MARIADB_ROOT_PASSWORD"] != "" || env["MYSQL_ROOT_PASSWORD"] != "" {
		return credentials{user: "root"}
	}
	user := env["MARIADB_USER"]
	if user == "" {
		user = env["MYSQL_USER"]
	}
	password := env["MARIADB_PASSWORD"]
	if password == "" {
		password = env["MYSQL_PASSWORD"]
	}
	return credentials{user: user, password: password}
}

// pgExtraEnv returns the extra environment for postgres clients, or nil when
// no password is needed.
func pgExtraEnv(c credentials) []string {
	if c.password == "" {
		return nil
	}
	return []string{"PGPASSWORD=" + c.password}
}

// mysqlExtraEnv returns the extra environment for mysql/mariadb clients, or
// nil when no password is needed.
func mysqlExtraEnv(c credentials) []string {
	if c.password == "" {
		return nil
	}
	return []string{"MYSQL_PWD=" + c.password}
}

// DumpCmd returns the dump command and extra environment variables for the
// given database type, selecting credentials from the container's
// environment variables.
func DumpCmd(env map[string]string, dbType string) (cmd []string, extraEnv []string, err error) {
	switch dbType {
	case "postgres":
		c := postgresCreds(env)
		// --clean --if-exists emits guarded DROP statements before each
		// CREATE, so restoring into a non-empty cluster converges to the
		// backup state instead of failing with "already exists" and
		// duplicate-key errors.
		return []string{"pg_dumpall", "--clean", "--if-exists", "-U", c.user}, pgExtraEnv(c), nil

	case "mysql":
		c := mysqlCreds(env)
		cmd = []string{
			"mysqldump",
			"--user=" + c.user,
			"--all-databases",
			"--single-transaction",
			"--compact",
			// --add-drop-table must come after --compact, which disables
			// the default per-table DROP statements (last flag wins). The
			// drops make a restore into a non-empty server converge to the
			// backup state. --add-drop-database is not an option: with
			// --all-databases it emits DROP DATABASE for the mysql system
			// schema, which MySQL 8.0+ refuses to drop, aborting the
			// restore.
			"--add-drop-table",
			"--force",
		}
		return cmd, mysqlExtraEnv(c), nil

	case "mariadb":
		c := mariadbCreds(env)
		cmd = []string{
			"mariadb-dump",
			"--user=" + c.user,
			"--all-databases",
			"--single-transaction",
			"--compact",
			// See the mysql case: --add-drop-table must follow --compact,
			// and --add-drop-database would drop the mysql system schema
			// (the grant tables) mid-restore.
			"--add-drop-table",
			"--force",
		}
		return cmd, mysqlExtraEnv(c), nil

	default:
		return nil, nil, fmt.Errorf("unknown database type: %s", dbType)
	}
}

// ImportCmd returns the command and extra environment variables for importing
// a SQL dump into a container, using the same credential selection as
// DumpCmd so restore succeeds wherever backup does.
func ImportCmd(env map[string]string, dbType string) (cmd []string, extraEnv []string, err error) {
	switch dbType {
	case "postgres":
		c := postgresCreds(env)
		// ON_ERROR_STOP makes psql exit non-zero on the first SQL error
		// instead of logging and continuing with exit 0. pg_dumpall output
		// cannot run inside --single-transaction, so this is the only
		// reliable failure signal for restores.
		//
		// Connect to the postgres maintenance database explicitly: without
		// -d, psql connects to the database named after the user, which a
		// --clean dump drops — and dropping the currently open database is
		// an error. pg_dumpall never emits a top-level DROP for "postgres"
		// (it drops/recreates it only while connected elsewhere).
		return []string{"psql", "--set", "ON_ERROR_STOP=on", "-U", c.user, "-d", "postgres"}, pgExtraEnv(c), nil

	case "mysql":
		c := mysqlCreds(env)
		return []string{"mysql", "--user=" + c.user}, mysqlExtraEnv(c), nil

	case "mariadb":
		c := mariadbCreds(env)
		return []string{"mariadb", "--user=" + c.user}, mysqlExtraEnv(c), nil

	default:
		return nil, nil, fmt.Errorf("unknown database type: %s", dbType)
	}
}
