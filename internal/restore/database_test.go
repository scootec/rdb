package restore

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDumpToFileWritesDump(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dump.sql")

	if err := dumpToFile(strings.NewReader("SELECT 1;"), path); err != nil {
		t.Fatalf("dumpToFile() error = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "SELECT 1;" {
		t.Errorf("file contents = %q, want %q", got, "SELECT 1;")
	}
}

func TestDumpToFileReadErrorRemovesPartialFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dump.sql")
	reader := io.MultiReader(strings.NewReader("SELECT 1;"), failingReader{errors.New("restic dump: exit status 1")})

	if err := dumpToFile(reader, path); err == nil {
		t.Fatal("dumpToFile() error = nil, want the read error")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("Stat(%s) error = %v, want the partial file removed", path, err)
	}
}

type failingReader struct{ err error }

func (f failingReader) Read([]byte) (int, error) { return 0, f.err }
