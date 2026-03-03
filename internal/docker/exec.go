package docker

import (
	"context"
	"fmt"
	"io"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

// execReader wraps a hijacked Docker exec connection and demultiplexes the
// Docker stream protocol into plain stdout bytes.
type execReader struct {
	cli    *client.Client
	execID string
	conn   types.HijackedResponse

	// pr/pw are the demuxed pipe ends
	pr *io.PipeReader
	pw *io.PipeWriter

	started bool
	done    chan struct{}
	err     error
}

func (r *execReader) start() {
	r.pr, r.pw = io.Pipe()
	r.done = make(chan struct{})
	go func() {
		defer close(r.done)
		_, err := stdcopy.StdCopy(r.pw, io.Discard, r.conn.Reader)
		r.pw.CloseWithError(err)
	}()
	r.started = true
}

func (r *execReader) Read(p []byte) (int, error) {
	if !r.started {
		r.start()
	}
	return r.pr.Read(p)
}

func (r *execReader) Close() error {
	if !r.started {
		r.start()
	}
	// Drain and close
	r.pr.Close()
	r.conn.Close()
	<-r.done
	return nil
}

// ExitCode returns the exit code of the exec after the reader has been fully consumed and closed.
func (c *Client) ExecExitCode(ctx context.Context, execID string) (int, error) {
	inspect, err := c.cli.ContainerExecInspect(ctx, execID)
	if err != nil {
		return -1, fmt.Errorf("exec inspect: %w", err)
	}
	if inspect.Running {
		return -1, fmt.Errorf("exec still running")
	}
	return inspect.ExitCode, nil
}
