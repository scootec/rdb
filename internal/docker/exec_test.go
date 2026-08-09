package docker

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
)

func TestBoundedBuffer(t *testing.T) {
	tests := []struct {
		name   string
		limit  int
		writes []string
		want   string
	}{
		{"empty buffer", 8, nil, ""},
		{"under limit", 8, []string{"abc"}, "abc"},
		{"exactly at limit", 3, []string{"abc"}, "abc"},
		{"single write over limit", 3, []string{"abcdef"}, "abc [output truncated]"},
		{"second write crosses limit", 5, []string{"abc", "def"}, "abcde [output truncated]"},
		{"writes after limit are dropped", 3, []string{"abc", "def", "ghi"}, "abc [output truncated]"},
		{"zero limit keeps nothing", 0, []string{"abc"}, " [output truncated]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newBoundedBuffer(tt.limit)
			for _, w := range tt.writes {
				n, err := b.Write([]byte(w))
				if err != nil {
					t.Fatalf("Write(%q) returned error: %v", w, err)
				}
				if n != len(w) {
					t.Errorf("Write(%q) = %d, want %d (short writes break io.Writer contract)", w, n, len(w))
				}
			}
			if got := b.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBoundedBufferLen(t *testing.T) {
	b := newBoundedBuffer(4)
	if _, err := b.Write([]byte("abcdef")); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	if got := b.Len(); got != 4 {
		t.Errorf("Len() = %d, want 4 (retained bytes, not written bytes)", got)
	}
	if !strings.HasPrefix(b.String(), "abcd") {
		t.Errorf("String() = %q, want prefix %q", b.String(), "abcd")
	}
}

// blockedExecReader builds an execReader over a net.Pipe whose remote end
// never sends data, so reads block until something closes the connection.
func blockedExecReader(ctx context.Context) (*execReader, net.Conn) {
	local, remote := net.Pipe()
	r := &execReader{
		ctx:  ctx,
		conn: types.HijackedResponse{Conn: local, Reader: bufio.NewReader(local)},
	}
	return r, remote
}

func TestExecReaderUnblocksOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r, remote := blockedExecReader(ctx)
	defer remote.Close()
	defer r.Close()

	errCh := make(chan error, 1)
	go func() {
		_, err := io.ReadAll(r)
		errCh <- err
	}()

	// Let the reader block on the connection before cancelling.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Read error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Read still blocked 5s after context cancellation")
	}
}

func TestExecReaderCloseWithoutCancellation(t *testing.T) {
	r, remote := blockedExecReader(context.Background())
	defer remote.Close()

	done := make(chan struct{})
	go func() {
		r.Close()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close blocked on a live context — watcher not stopped")
	}
}
