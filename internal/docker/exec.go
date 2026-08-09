package docker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/rs/zerolog/log"
)

// execReader wraps a hijacked Docker exec connection and demultiplexes the
// Docker stream protocol into plain stdout bytes. When ctx is cancelled the
// hijacked connection is closed, which unblocks any in-flight Read.
type execReader struct {
	cli    *client.Client
	execID string
	conn   types.HijackedResponse
	ctx    context.Context

	// pr/pw are the demuxed pipe ends
	pr *io.PipeReader
	pw *io.PipeWriter

	stderr    bytes.Buffer
	started   bool
	done      chan struct{}
	stopWatch func() bool
}

func (r *execReader) start() {
	r.pr, r.pw = io.Pipe()
	r.done = make(chan struct{})
	if r.ctx != nil {
		// Reads on the hijacked connection do not honour context on their
		// own: closing the connection is the only way to unblock them. The
		// pipe is failed with the context's error first so readers see
		// context.Canceled/DeadlineExceeded rather than a network error.
		r.stopWatch = context.AfterFunc(r.ctx, func() {
			r.pw.CloseWithError(r.ctx.Err())
			r.conn.Close()
		})
	}
	go func() {
		defer close(r.done)
		_, err := stdcopy.StdCopy(r.pw, &r.stderr, r.conn.Reader)
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

// Stderr returns the captured stderr output. Only valid after Close.
func (r *execReader) Stderr() string {
	return r.stderr.String()
}

func (r *execReader) Close() error {
	if !r.started {
		r.start()
	}
	if r.stopWatch != nil {
		r.stopWatch()
	}
	// Drain and close
	r.pr.Close()
	r.conn.Close()
	<-r.done
	return nil
}

// ExecImport runs a command inside the target container and streams stdin into it.
// This is the inverse of ExecDump — used to pipe data (e.g. SQL dumps) into a container.
func (c *Client) ExecImport(ctx context.Context, containerID string, cmd []string, extraEnv []string, stdin io.Reader) error {
	execConfig := container.ExecOptions{
		Cmd:          cmd,
		Env:          extraEnv,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
	}

	execID, err := c.cli.ContainerExecCreate(ctx, containerID, execConfig)
	if err != nil {
		return fmt.Errorf("exec create: %w", err)
	}

	resp, err := c.cli.ContainerExecAttach(ctx, execID.ID, container.ExecStartOptions{})
	if err != nil {
		return fmt.Errorf("exec attach: %w", err)
	}
	defer resp.Close()

	// I/O on the hijacked connection does not honour context on its own:
	// closing the connection is the only way to unblock the copies below.
	stopWatch := context.AfterFunc(ctx, func() { resp.Close() })
	defer stopWatch()

	// Stream stdin into the exec process
	errCh := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(resp.Conn, stdin)
		_ = resp.CloseWrite()
		errCh <- copyErr
	}()

	// Demux stdout/stderr, keeping a bounded copy of each: stderr is
	// surfaced when the command fails, stdout is logged at debug level.
	stdout := newBoundedBuffer(execOutputLimit)
	stderr := newBoundedBuffer(execOutputLimit)
	if _, demuxErr := stdcopy.StdCopy(stdout, stderr, resp.Reader); demuxErr != nil {
		log.Debug().Err(demuxErr).Msg("demuxing exec output")
	}

	copyErr := <-errCh
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("exec import cancelled: %w", ctxErr)
	}
	if copyErr != nil {
		return fmt.Errorf("writing stdin to exec: %w", copyErr)
	}

	if stdout.Len() > 0 {
		log.Debug().Str("stdout", stdout.String()).Msg("import command output")
	}

	exitCode, err := c.ExecExitCode(ctx, execID.ID)
	if err != nil {
		return err
	}
	if exitCode != 0 {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("exec command exited with code %d: %s", exitCode, msg)
		}
		return fmt.Errorf("exec command exited with code %d", exitCode)
	}
	return nil
}

// execOutputLimit caps how much exec stdout/stderr is retained in memory.
const execOutputLimit = 32 * 1024

// boundedBuffer keeps the first limit bytes written to it and silently
// discards the rest, so a chatty exec command cannot grow memory unbounded.
type boundedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func newBoundedBuffer(limit int) *boundedBuffer {
	return &boundedBuffer{limit: limit}
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := b.limit - b.buf.Len(); remaining < n {
		p = p[:remaining]
		b.truncated = true
	}
	b.buf.Write(p)
	return n, nil
}

func (b *boundedBuffer) Len() int { return b.buf.Len() }

func (b *boundedBuffer) String() string {
	if b.truncated {
		return b.buf.String() + " [output truncated]"
	}
	return b.buf.String()
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
