package health

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

type ping struct {
	method string
	path   string
	body   string
}

func pingServer(t *testing.T) (*httptest.Server, *[]ping) {
	t.Helper()
	var pings []ping
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		pings = append(pings, ping{method: r.Method, path: r.URL.Path, body: string(body)})
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &pings
}

func TestPingSuccess(t *testing.T) {
	srv, pings := pingServer(t)

	NewPinger(srv.URL+"/ping/uuid-1234").Ping("backup", nil)

	if len(*pings) != 1 {
		t.Fatalf("got %d pings, want 1", len(*pings))
	}
	got := (*pings)[0]
	if got.method != http.MethodGet || got.path != "/ping/uuid-1234" {
		t.Errorf("success ping = %s %s, want GET /ping/uuid-1234", got.method, got.path)
	}
}

func TestPingFailureHitsFailEndpointWithBody(t *testing.T) {
	srv, pings := pingServer(t)

	NewPinger(srv.URL+"/ping/uuid-1234").Ping("backup", errors.New("volume copy failed"))

	if len(*pings) != 1 {
		t.Fatalf("got %d pings, want 1", len(*pings))
	}
	got := (*pings)[0]
	if got.method != http.MethodPost || got.path != "/ping/uuid-1234/fail" {
		t.Errorf("failure ping = %s %s, want POST /ping/uuid-1234/fail", got.method, got.path)
	}
	if got.body != "backup failed: volume copy failed" {
		t.Errorf("failure body = %q, want error summary", got.body)
	}
}

// Self-hosted healthchecks URLs are often pasted with a trailing slash; the
// /fail suffix must still land on <url>/fail, not <url>//fail.
func TestPingTrailingSlashNormalised(t *testing.T) {
	srv, pings := pingServer(t)

	NewPinger(srv.URL+"/ping/uuid-1234/").Ping("backup", errors.New("boom"))

	if len(*pings) != 1 {
		t.Fatalf("got %d pings, want 1", len(*pings))
	}
	if got := (*pings)[0].path; got != "/ping/uuid-1234/fail" {
		t.Errorf("failure ping path = %q, want /ping/uuid-1234/fail", got)
	}
}

func TestNilPingerIsNoOp(t *testing.T) {
	var p *Pinger
	p.Ping("backup", nil) // must not panic

	if NewPinger("") != nil {
		t.Error("NewPinger(\"\") != nil, want nil")
	}
}

func TestPingUnreachableServerDoesNotPanic(t *testing.T) {
	// Closed port: the ping errors, gets logged, and must not propagate.
	NewPinger("http://127.0.0.1:1").Ping("backup", nil)
}
