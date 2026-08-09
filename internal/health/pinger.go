package health

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

const pingTimeout = 10 * time.Second

// Pinger sends healthchecks.io-style pings: a GET to the base URL on success,
// a POST to <url>/fail with the error summary as the body on failure. Both
// the hosted service and self-hosted healthchecks instances (any scheme/host,
// including plain HTTP on a LAN) use these exact semantics. Ping failures are
// logged and never propagate — a broken notifier must not fail a backup.
type Pinger struct {
	url    string
	client *http.Client
}

// NewPinger returns a Pinger for the given base ping URL, or nil when url is
// empty. A nil Pinger is valid and all its methods are no-ops.
func NewPinger(url string) *Pinger {
	if url == "" {
		return nil
	}
	return &Pinger{
		url:    strings.TrimRight(url, "/"),
		client: &http.Client{Timeout: pingTimeout},
	}
}

// Ping reports the outcome of a run: runErr == nil signals success, anything
// else pings the /fail endpoint with the error text.
func (p *Pinger) Ping(job string, runErr error) {
	if p == nil {
		return
	}

	var resp *http.Response
	var err error
	if runErr == nil {
		resp, err = p.client.Get(p.url)
	} else {
		body := strings.NewReader(fmt.Sprintf("%s failed: %s", job, runErr))
		resp, err = p.client.Post(p.url+"/fail", "text/plain", body)
	}
	if err != nil {
		log.Warn().Err(err).Str("job", job).Msg("healthcheck ping failed")
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 300 {
		log.Warn().Int("status", resp.StatusCode).Str("job", job).Msg("healthcheck ping rejected")
	}
}
