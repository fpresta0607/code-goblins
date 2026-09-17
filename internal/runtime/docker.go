package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// Labels compose and the Supabase CLI stamp on what they start. They are the
// whole of container attribution: without them a container is an image name
// and nothing else.
const (
	labelComposeProject = "com.docker.compose.project"
	labelComposeWorkDir = "com.docker.compose.project.working_dir"
	labelSupabaseStack  = "com.supabase.cli.project"
	labelSupabaseDir    = "com.supabase.cli.workdir"
)

// Docker reads the container, volume and disk-usage state through the docker
// CLI. Every call is a read.
type Docker struct {
	Commands execx.Runner
}

// inspected is the subset of `docker inspect` this report needs.
//
// The report reads inspect rather than `docker ps --format`, which would be
// one call instead of two, because ps renders labels as a comma-joined string
// and compose label values are Windows paths. A path holding a comma silently
// splits into two bogus labels there, and the working directory is precisely
// the field every attribution below turns on.
type inspected struct {
	ID    string `json:"Id"`
	Name  string `json:"Name"`
	State struct {
		Status     string `json:"Status"`
		Running    bool   `json:"Running"`
		Restarting bool   `json:"Restarting"`
		StartedAt  string `json:"StartedAt"`
		FinishedAt string `json:"FinishedAt"`
	} `json:"State"`
	RestartCount int `json:"RestartCount"`
	Config       struct {
		Image  string            `json:"Image"`
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	HostConfig struct {
		Memory int64 `json:"Memory"`
	} `json:"HostConfig"`
	Mounts []struct {
		Type string `json:"Type"`
		Name string `json:"Name"`
	} `json:"Mounts"`
	NetworkSettings struct {
		Ports map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string `json:"HostPort"`
		} `json:"Ports"`
	} `json:"NetworkSettings"`
}

// Containers reads every container Docker still holds a record for. A Docker
// that is not running is not an error: the report says the section is blind
// and why, which is the one thing an empty section must never be confused
// with.
func (d Docker) Containers(ctx context.Context) ([]Container, error) {
	ids, err := d.run(ctx, "ps", "-aq", "--no-trunc")
	if err != nil {
		return nil, err
	}
	lines := splitLines(string(ids))
	if len(lines) == 0 {
		return []Container{}, nil
	}
	args := append([]string{"inspect"}, lines...)
	raw, err := d.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	var rows []inspected
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("runtime: decode docker inspect: %w", err)
	}
	containers := make([]Container, 0, len(rows))
	for _, row := range rows {
		containers = append(containers, row.container())
	}
	sort.Slice(containers, func(i, j int) bool { return containers[i].Name < containers[j].Name })
	return containers, nil
}

func (row inspected) container() Container {
	labels := row.Config.Labels
	stack := labels[labelComposeProject]
	if stack == "" {
		stack = labels[labelSupabaseStack]
	}
	workDir := labels[labelComposeWorkDir]
	if workDir == "" {
		workDir = labels[labelSupabaseDir]
	}
	container := Container{
		ID:               shortID(row.ID),
		Name:             strings.TrimPrefix(row.Name, "/"),
		Image:            row.Config.Image,
		State:            row.State.Status,
		Since:            row.since(),
		RestartCount:     row.RestartCount,
		Stack:            stack,
		WorkDir:          workDir,
		Ports:            row.ports(),
		Volumes:          row.namedVolumes(),
		MemoryLimitBytes: row.HostConfig.Memory,
	}
	return container
}

// since is when the container entered its current state: when it started if
// it is up, when it stopped if it is not.
func (row inspected) since() time.Time {
	stamp := row.State.FinishedAt
	if row.State.Running || row.State.Restarting {
		stamp = row.State.StartedAt
	}
	parsed, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}

// ports renders the host bindings, deduplicated. Docker reports one binding
// per address family, so an ordinary published port appears twice with
// identical text.
func (row inspected) ports() []string {
	seen := map[string]bool{}
	var ports []string
	for container, bindings := range row.NetworkSettings.Ports {
		for _, binding := range bindings {
			host := binding.HostPort
			if binding.HostIP != "" && binding.HostIP != "0.0.0.0" && binding.HostIP != "::" {
				host = binding.HostIP + ":" + binding.HostPort
			}
			rendered := host + "->" + container
			if seen[rendered] {
				continue
			}
			seen[rendered] = true
			ports = append(ports, rendered)
		}
	}
	sort.Strings(ports)
	return ports
}

// namedVolumes keeps only volumes with a name. An anonymous volume's name is
// its 64-character id, which carries no ownership at all and would only bury
// the named ones this report is about.
func (row inspected) namedVolumes() []string {
	var volumes []string
	for _, mount := range row.Mounts {
		if mount.Type == "volume" && mount.Name != "" && !anonymousVolume(mount.Name) {
			volumes = append(volumes, mount.Name)
		}
	}
	sort.Strings(volumes)
	return volumes
}

// Volumes reads the named volumes and their sizes. Sizes come from
// `docker system df -v`, the only place Docker reports per-volume size.
func (d Docker) Volumes(ctx context.Context) ([]Volume, error) {
	raw, err := d.run(ctx, "system", "df", "-v", "--format", "{{json .Volumes}}")
	if err != nil {
		return nil, err
	}
	var rows []struct {
		Name string `json:"Name"`
		Size string `json:"Size"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("runtime: decode docker volumes: %w", err)
	}
	volumes := make([]Volume, 0, len(rows))
	for _, row := range rows {
		if anonymousVolume(row.Name) {
			continue
		}
		volumes = append(volumes, Volume{Name: row.Name, SizeBytes: parseSize(row.Size)})
	}
	sort.Slice(volumes, func(i, j int) bool { return volumes[i].Name < volumes[j].Name })
	return volumes, nil
}

// Usage reads what Docker occupies on disk.
func (d Docker) Usage(ctx context.Context) (images, containers, volumes, buildCache, reclaimable int64, err error) {
	raw, err := d.run(ctx, "system", "df", "--format", "{{json .}}")
	if err != nil {
		return 0, 0, 0, 0, 0, err
	}
	// `docker system df --format {{json .}}` emits one object per type, one
	// per line, rather than a single array.
	for _, line := range splitLines(string(raw)) {
		var row struct {
			Type        string `json:"Type"`
			Size        string `json:"Size"`
			Reclaimable string `json:"Reclaimable"`
		}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			return 0, 0, 0, 0, 0, fmt.Errorf("runtime: decode docker system df: %w", err)
		}
		size := parseSize(row.Size)
		reclaimable += parseSize(row.Reclaimable)
		switch row.Type {
		case "Images":
			images = size
		case "Containers":
			containers = size
		case "Local Volumes":
			volumes = size
		case "Build Cache":
			buildCache = size
		}
	}
	return images, containers, volumes, buildCache, reclaimable, nil
}

func (d Docker) run(ctx context.Context, args ...string) ([]byte, error) {
	if d.Commands == nil {
		return nil, errors.New("runtime: command runner is required")
	}
	result, err := d.Commands.Run(ctx, execx.Request{Name: "docker", Args: args})
	if err != nil {
		return nil, fmt.Errorf("runtime: docker %s: %w", args[0], err)
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("runtime: docker %s exited %d: %s", args[0], result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	return result.Stdout, nil
}

// anonymousVolume reports whether a volume name is Docker's own 64-character
// hex id rather than a name a compose file chose.
func anonymousVolume(name string) bool {
	if len(name) != 64 {
		return false
	}
	for _, char := range name {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return false
		}
	}
	return true
}

// parseSize reads the human sizes the docker CLI prints ("72.29GB", "1.5kB",
// "49.02GB (67%)") back into bytes. Docker uses decimal units here, unlike
// the binary units the report renders with, so the conversion is by powers of
// 1000 rather than 1024.
func parseSize(value string) int64 {
	value = strings.TrimSpace(value)
	if index := strings.Index(value, "("); index >= 0 {
		value = strings.TrimSpace(value[:index])
	}
	if value == "" {
		return 0
	}
	digits := 0
	for digits < len(value) && (value[digits] >= '0' && value[digits] <= '9' || value[digits] == '.') {
		digits++
	}
	number, err := strconv.ParseFloat(value[:digits], 64)
	if err != nil {
		return 0
	}
	multiplier := float64(1)
	switch strings.ToLower(strings.TrimSpace(value[digits:])) {
	case "kb":
		multiplier = 1e3
	case "mb":
		multiplier = 1e6
	case "gb":
		multiplier = 1e9
	case "tb":
		multiplier = 1e12
	}
	return int64(number * multiplier)
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func splitLines(value string) []string {
	var lines []string
	for _, line := range strings.Split(value, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	return lines
}
