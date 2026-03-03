package docker

import (
	"context"
	"fmt"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
)

const labelPrefix = "rdb."

// ContainerInfo holds parsed backup configuration for a single container.
type ContainerInfo struct {
	ID   string
	Name string

	// Compose metadata
	Project string
	Service string

	// Volume backup config
	VolumesEnabled        bool
	VolumesInclude        []string
	VolumesExclude        []string
	VolumeStopDuringBackup bool

	// Database backup config
	PostgresEnabled bool
	MySQLEnabled    bool
	MariaDBEnabled  bool

	// All environment variables of the container (for DB credentials)
	Env map[string]string

	// Mounts reported by Docker
	Mounts []types.MountPoint
}

// Client wraps the Docker Engine API client.
type Client struct {
	cli *client.Client
}

// New creates a new Client using the DOCKER_HOST environment variable (or default socket).
func New() (*Client, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("creating docker client: %w", err)
	}
	return &Client{cli: cli}, nil
}

// Close releases the underlying Docker client resources.
func (c *Client) Close() error {
	return c.cli.Close()
}

// DiscoverContainers lists all running containers that have at least one rdb.* label.
func (c *Client) DiscoverContainers(ctx context.Context) ([]ContainerInfo, error) {
	f := filters.NewArgs()
	f.Add("status", "running")

	containers, err := c.cli.ContainerList(ctx, container.ListOptions{Filters: f})
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}

	var result []ContainerInfo
	for _, ctr := range containers {
		if !hasRDBLabel(ctr.Labels) {
			continue
		}
		info, err := c.parseContainer(ctx, ctr.ID, ctr.Labels)
		if err != nil {
			return nil, err
		}
		result = append(result, info)
	}
	return result, nil
}

func hasRDBLabel(labels map[string]string) bool {
	for k := range labels {
		if strings.HasPrefix(k, labelPrefix) {
			return true
		}
	}
	return false
}

func (c *Client) parseContainer(ctx context.Context, id string, labels map[string]string) (ContainerInfo, error) {
	inspect, err := c.cli.ContainerInspect(ctx, id)
	if err != nil {
		return ContainerInfo{}, fmt.Errorf("inspecting container %s: %w", id, err)
	}

	name := strings.TrimPrefix(inspect.Name, "/")
	project := inspect.Config.Labels["com.docker.compose.project"]
	service := inspect.Config.Labels["com.docker.compose.service"]

	env := parseEnvSlice(inspect.Config.Env)

	info := ContainerInfo{
		ID:      id,
		Name:    name,
		Project: project,
		Service: service,
		Env:     env,
		Mounts:  inspect.Mounts,

		VolumesEnabled:         labelBool(labels, "rdb.volumes", false),
		VolumeStopDuringBackup: labelBool(labels, "rdb.volumes.stop-during-backup", false),
		PostgresEnabled:        labelBool(labels, "rdb.postgres", false),
		MySQLEnabled:           labelBool(labels, "rdb.mysql", false),
		MariaDBEnabled:         labelBool(labels, "rdb.mariadb", false),
	}

	if inc := labels["rdb.volumes.include"]; inc != "" {
		info.VolumesInclude = splitComma(inc)
	}
	if exc := labels["rdb.volumes.exclude"]; exc != "" {
		info.VolumesExclude = splitComma(exc)
	}

	return info, nil
}

// ExecDump runs a command inside the target container and returns an io.ReadCloser of its stdout.
// The caller is responsible for closing the returned reader.
func (c *Client) ExecDump(ctx context.Context, containerID string, cmd []string, extraEnv []string) (interface{ Read([]byte) (int, error); Close() error }, int, error) {
	execConfig := container.ExecOptions{
		Cmd:          cmd,
		Env:          extraEnv,
		AttachStdout: true,
		AttachStderr: false,
	}

	execID, err := c.cli.ContainerExecCreate(ctx, containerID, execConfig)
	if err != nil {
		return nil, -1, fmt.Errorf("exec create: %w", err)
	}

	resp, err := c.cli.ContainerExecAttach(ctx, execID.ID, container.ExecStartOptions{})
	if err != nil {
		return nil, -1, fmt.Errorf("exec attach: %w", err)
	}

	// We return the raw multiplexed reader; callers use docker.StdCopy to demux.
	return &execReader{
		cli:    c.cli,
		execID: execID.ID,
		conn:   resp,
	}, 0, nil
}

// StopContainer stops the given container.
func (c *Client) StopContainer(ctx context.Context, containerID string) error {
	timeout := 30
	return c.cli.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &timeout})
}

// StartContainer starts the given container.
func (c *Client) StartContainer(ctx context.Context, containerID string) error {
	return c.cli.ContainerStart(ctx, containerID, container.StartOptions{})
}

// --- helpers ---

func parseEnvSlice(envSlice []string) map[string]string {
	m := make(map[string]string, len(envSlice))
	for _, e := range envSlice {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			m[parts[0]] = parts[1]
		}
	}
	return m
}

func labelBool(labels map[string]string, key string, def bool) bool {
	v, ok := labels[key]
	if !ok {
		return def
	}
	v = strings.ToLower(strings.TrimSpace(v))
	return v == "true" || v == "1" || v == "yes"
}

func splitComma(s string) []string {
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
