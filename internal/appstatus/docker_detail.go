package appstatus

import (
	"context"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/client"
)

const dockerDetailTimeout = 3 * time.Second

func (c *dockerCollector) RuntimeDetail(ctx context.Context, request DetailRequest) RuntimeDetail {
	result := RuntimeDetail{Kind: KindDocker}
	if request.Container == nil || strings.TrimSpace(request.Container.ID) == "" {
		result.State = RuntimeUnavailable
		result.Code = "container_exited"
		result.Message = "The container exited before its runtime details could be read."
		return result
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	detailContext, cancel := context.WithTimeout(ctx, dockerDetailTimeout)
	defer cancel()
	inspection, err := c.client.ContainerInspect(detailContext, request.Container.ID, client.ContainerInspectOptions{Size: false})
	if err != nil {
		result.State = RuntimeUnavailable
		result.Code = "docker_inspect_unavailable"
		result.Message = err.Error()
		return result
	}
	value := inspection.Container
	docker := &DockerRuntimeDetail{
		ContainerID:      value.ID,
		Image:            request.Container.Image,
		Ports:            make([]DockerPort, 0),
		Mounts:           make([]DockerMount, 0, len(value.Mounts)),
		RelatedProcesses: make([]RelatedProcess, 0),
		RestartCount:     value.RestartCount,
	}
	if value.Config != nil {
		command := append([]string(nil), value.Config.Entrypoint...)
		command = append(command, value.Config.Cmd...)
		if len(command) == 0 && value.Path != "" {
			command = append([]string{value.Path}, value.Args...)
		}
		docker.CommandLine = strings.Join(command, " ")
		docker.WorkingDirectory = value.Config.WorkingDir
		if value.Config.Image != "" {
			docker.Image = value.Config.Image
		}
	}
	if value.State != nil {
		docker.HostPID = value.State.Pid
		docker.ContainerPID = containerNamespacePID(value.State.Pid)
		docker.StartedAt = parseDockerTime(value.State.StartedAt)
		if docker.StartedAt != nil {
			until := request.CollectedAt
			if until.IsZero() {
				until = time.Now().UTC()
			}
			docker.DurationSeconds = max(0, int64(until.Sub(*docker.StartedAt).Seconds()))
		}
		if value.State.Health != nil {
			docker.Health = string(value.State.Health.Status)
		}
	}
	if value.HostConfig != nil {
		docker.RestartPolicy = string(value.HostConfig.RestartPolicy.Name)
		docker.NetworkMode = string(value.HostConfig.NetworkMode)
	}
	for _, mount := range value.Mounts {
		docker.Mounts = append(docker.Mounts, DockerMount{
			Type: string(mount.Type), Source: mount.Source,
			Destination: mount.Destination, ReadOnly: !mount.RW,
		})
	}
	if value.NetworkSettings != nil {
		for port, bindings := range value.NetworkSettings.Ports {
			if len(bindings) == 0 {
				docker.Ports = append(docker.Ports, DockerPort{
					Protocol: string(port.Proto()), ContainerPort: uint32(port.Num()),
				})
				continue
			}
			for _, binding := range bindings {
				hostAddress := ""
				if binding.HostIP.IsValid() {
					hostAddress = binding.HostIP.String()
				}
				docker.Ports = append(docker.Ports, DockerPort{
					Protocol: string(port.Proto()), ContainerPort: uint32(port.Num()),
					HostAddress: hostAddress, HostPort: binding.HostPort,
				})
			}
		}
	}
	sort.Slice(docker.Ports, func(i, j int) bool {
		left, right := docker.Ports[i], docker.Ports[j]
		if left.ContainerPort != right.ContainerPort {
			return left.ContainerPort < right.ContainerPort
		}
		if left.Protocol != right.Protocol {
			return left.Protocol < right.Protocol
		}
		if left.HostAddress != right.HostAddress {
			return left.HostAddress < right.HostAddress
		}
		return left.HostPort < right.HostPort
	})
	sort.Slice(docker.Mounts, func(i, j int) bool {
		return docker.Mounts[i].Destination < docker.Mounts[j].Destination
	})

	top, topErr := c.client.ContainerTop(detailContext, request.Container.ID, client.ContainerTopOptions{})
	if topErr == nil {
		docker.RelatedProcesses, _ = dockerTopProcesses(top.Titles, top.Processes)
	}
	result.Docker = docker
	if topErr != nil {
		result.State = RuntimePartial
		result.Code = "docker_top_unavailable"
		result.Message = topErr.Error()
	} else {
		result.State = RuntimeAvailable
	}
	return result
}

func containerNamespacePID(hostPID int) int {
	if runtime.GOOS != "linux" || hostPID <= 0 {
		return 0
	}
	content, err := os.ReadFile("/proc/" + strconv.Itoa(hostPID) + "/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(content), "\n") {
		if !strings.HasPrefix(line, "NSpid:") {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(line, "NSpid:"))
		if len(fields) == 0 {
			return 0
		}
		value, _ := strconv.Atoi(fields[len(fields)-1])
		return value
	}
	return 0
}

func dockerTopProcesses(titles []string, rows [][]string) ([]RelatedProcess, int) {
	columns := make(map[string]int, len(titles))
	for index, title := range titles {
		columns[strings.ToUpper(strings.TrimSpace(title))] = index
	}
	result := make([]RelatedProcess, 0, len(rows))
	containerPID := 0
	for _, row := range rows {
		pid := parseDockerProcessInt(row, columns, "PID")
		if containerPID == 0 {
			containerPID = int(pid)
		}
		name := dockerProcessColumn(row, columns, "COMMAND")
		if name == "" {
			name = dockerProcessColumn(row, columns, "CMD")
		}
		result = append(result, RelatedProcess{
			PID:         pid,
			ParentPID:   parseDockerProcessInt(row, columns, "PPID"),
			Name:        name,
			User:        firstDockerProcessColumn(row, columns, "USER", "UID"),
			CommandLine: name,
		})
	}
	return result, containerPID
}

func parseDockerProcessInt(row []string, columns map[string]int, name string) int32 {
	value := dockerProcessColumn(row, columns, name)
	number, _ := strconv.ParseInt(value, 10, 32)
	return int32(number)
}

func firstDockerProcessColumn(row []string, columns map[string]int, names ...string) string {
	for _, name := range names {
		if value := dockerProcessColumn(row, columns, name); value != "" {
			return value
		}
	}
	return ""
}

func dockerProcessColumn(row []string, columns map[string]int, name string) string {
	index, ok := columns[name]
	if !ok || index < 0 || index >= len(row) {
		return ""
	}
	return row[index]
}

func parseDockerTime(value string) *time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.IsZero() {
		return nil
	}
	parsed = parsed.UTC()
	return &parsed
}
