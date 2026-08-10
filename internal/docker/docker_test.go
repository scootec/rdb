package docker

import (
	"reflect"
	"testing"
	"time"
)

func TestLabelBool(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
		key    string
		def    bool
		want   bool
	}{
		{"missing key returns default false", map[string]string{}, "rdb.volumes", false, false},
		{"missing key returns default true", map[string]string{}, "rdb.volumes", true, true},
		{"true", map[string]string{"rdb.volumes": "true"}, "rdb.volumes", false, true},
		{"1", map[string]string{"rdb.volumes": "1"}, "rdb.volumes", false, true},
		{"yes", map[string]string{"rdb.volumes": "yes"}, "rdb.volumes", false, true},
		{"uppercase TRUE", map[string]string{"rdb.volumes": "TRUE"}, "rdb.volumes", false, true},
		{"surrounding whitespace is trimmed", map[string]string{"rdb.volumes": "  true "}, "rdb.volumes", false, true},
		{"false", map[string]string{"rdb.volumes": "false"}, "rdb.volumes", true, false},
		{"0", map[string]string{"rdb.volumes": "0"}, "rdb.volumes", true, false},
		// Documents current behaviour: any unrecognised value is false, even
		// when the default is true — a present key never falls back to def.
		{"garbage value is false despite default true", map[string]string{"rdb.volumes": "banana"}, "rdb.volumes", true, false},
		{"empty value is false despite default true", map[string]string{"rdb.volumes": ""}, "rdb.volumes", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := labelBool(tt.labels, tt.key, tt.def); got != tt.want {
				t.Errorf("labelBool(%v, %q, %v) = %v, want %v", tt.labels, tt.key, tt.def, got, tt.want)
			}
		})
	}
}

func TestLabelDuration(t *testing.T) {
	const key = "rdb.pre-backup.timeout"
	const def = 15 * time.Minute

	tests := []struct {
		name   string
		labels map[string]string
		want   time.Duration
	}{
		{"missing key returns default", map[string]string{}, def},
		{"valid duration", map[string]string{key: "5m"}, 5 * time.Minute},
		{"compound duration", map[string]string{key: "1h30m"}, 90 * time.Minute},
		{"surrounding whitespace is trimmed", map[string]string{key: " 30s "}, 30 * time.Second},
		{"invalid value falls back to default", map[string]string{key: "banana"}, def},
		{"bare number falls back to default", map[string]string{key: "300"}, def},
		{"zero falls back to default", map[string]string{key: "0"}, def},
		{"negative falls back to default", map[string]string{key: "-5m"}, def},
		{"empty value falls back to default", map[string]string{key: ""}, def},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := labelDuration(tt.labels, key, def); got != tt.want {
				t.Errorf("labelDuration(%v, %q, %v) = %v, want %v", tt.labels, key, def, got, tt.want)
			}
		})
	}
}

func TestSplitComma(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"simple list", "/data,/config", []string{"/data", "/config"}},
		{"whitespace around items is trimmed", " /data , /config ", []string{"/data", "/config"}},
		{"empty items are dropped", "/data,,/config,", []string{"/data", "/config"}},
		{"single item", "/data", []string{"/data"}},
		{"empty string yields nil", "", nil},
		{"only commas and spaces yields nil", " , , ", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := splitComma(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("splitComma(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseEnvSlice(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want map[string]string
	}{
		{
			name: "simple pairs",
			in:   []string{"FOO=bar", "BAZ=qux"},
			want: map[string]string{"FOO": "bar", "BAZ": "qux"},
		},
		{
			name: "value containing equals sign is kept whole",
			in:   []string{"DSN=postgres://u:p@host/db?sslmode=disable"},
			want: map[string]string{"DSN": "postgres://u:p@host/db?sslmode=disable"},
		},
		{
			name: "empty value",
			in:   []string{"EMPTY="},
			want: map[string]string{"EMPTY": ""},
		},
		{
			name: "entries without equals sign are skipped",
			in:   []string{"NOEQUALS", "FOO=bar"},
			want: map[string]string{"FOO": "bar"},
		},
		{
			name: "later duplicate wins",
			in:   []string{"FOO=first", "FOO=second"},
			want: map[string]string{"FOO": "second"},
		},
		{
			name: "empty slice yields empty map",
			in:   nil,
			want: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseEnvSlice(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseEnvSlice(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestHasRDBLabel(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
		want   bool
	}{
		{"rdb label present", map[string]string{"rdb.volumes": "true"}, true},
		{"prefix-only key counts", map[string]string{"rdb.": ""}, true},
		{"no rdb labels", map[string]string{"com.docker.compose.project": "myapp"}, false},
		{"empty labels", map[string]string{}, false},
		{"rdb without dot does not count", map[string]string{"rdb": "true"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasRDBLabel(tt.labels); got != tt.want {
				t.Errorf("hasRDBLabel(%v) = %v, want %v", tt.labels, got, tt.want)
			}
		})
	}
}
