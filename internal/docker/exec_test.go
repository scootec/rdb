package docker

import (
	"strings"
	"testing"
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
