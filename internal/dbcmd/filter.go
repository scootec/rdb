package dbcmd

import (
	"bufio"
	"io"
	"strings"
)

// ImportFilter prepares a SQL dump stream for import. For postgres it removes
// the statements pg_dumpall emits for the role the restore connects as:
// DROP ROLE fails with "current user cannot be dropped" and the subsequent
// CREATE ROLE with "role already exists" — pg_dumpall documents both as
// expected-to-fail, but with ON_ERROR_STOP=on they would abort the restore.
// Only the globals preamble (everything before the first \connect) is
// scanned; the rest of the stream, including COPY data, passes through
// untouched. Other database types are returned unchanged.
//
// The returned reader should be closed by the caller when it implements
// io.Closer, so the filter goroutine is released if the import stops early.
func ImportFilter(r io.Reader, env map[string]string, dbType string) io.Reader {
	if dbType != "postgres" {
		return r
	}
	c := postgresCreds(env)
	return filterOwnRoleStatements(r, c.user)
}

// ownRoleStatements returns the exact statement lines pg_dumpall emits for
// the given role, in both plain and quoted identifier form (pg_dumpall
// quotes the identifier only when it needs quoting).
func ownRoleStatements(role string) map[string]bool {
	quoted := `"` + strings.ReplaceAll(role, `"`, `""`) + `"`
	skip := make(map[string]bool)
	for _, id := range []string{role, quoted} {
		skip["DROP ROLE IF EXISTS "+id+";"] = true
		skip["DROP ROLE "+id+";"] = true
		skip["CREATE ROLE "+id+";"] = true
	}
	return skip
}

// filterOwnRoleStatements streams r, dropping the connecting role's own
// DROP/CREATE ROLE lines from the preamble.
func filterOwnRoleStatements(r io.Reader, role string) io.ReadCloser {
	skip := ownRoleStatements(role)
	pr, pw := io.Pipe()
	go func() {
		br := bufio.NewReader(r)
		for {
			line, err := br.ReadString('\n')
			if line != "" {
				// Role statements only appear before the per-database
				// sections; from the first \connect on, copy verbatim so
				// dump data can never be mistaken for one.
				if strings.HasPrefix(line, `\connect`) {
					if _, werr := io.WriteString(pw, line); werr != nil {
						pw.CloseWithError(werr)
						return
					}
					_, cerr := io.Copy(pw, br)
					pw.CloseWithError(cerr)
					return
				}
				if !skip[strings.TrimRight(line, "\r\n")] {
					if _, werr := io.WriteString(pw, line); werr != nil {
						pw.CloseWithError(werr)
						return
					}
				}
			}
			if err != nil {
				pw.CloseWithError(err)
				return
			}
		}
	}()
	return pr
}
