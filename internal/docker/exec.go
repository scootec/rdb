package docker

import (
	"context"
	"fmt"
	"io"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
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

	// Stream stdin into the exec process
	errCh := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(resp.Conn, stdin)
		resp.CloseWrite()
		errCh <- copyErr
	}()

	// Drain stdout/stderr (log any output as debug)
	_, _ = io.Copy(io.Discard, resp.Reader)

	if copyErr := <-errCh; copyErr != nil {
		return fmt.Errorf("writing stdin to exec: %w", copyErr)
	}

	exitCode, err := c.ExecExitCode(ctx, execID.ID)
	if err != nil {
		return err
	}
	if exitCode != 0 {
		return fmt.Errorf("exec command exited with code %d", exitCode)
	}
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
