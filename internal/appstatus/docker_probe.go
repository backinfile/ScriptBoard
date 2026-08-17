package appstatus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	containertypes "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

const (
	dockerListTimeout  = 5 * time.Second
	dockerStatsTimeout = 3 * time.Second
	dockerRetryDelay   = 30 * time.Second
	dockerWorkers      = 8
)

type dockerBlockSample struct {
	readBytes, writeBytes uint64
	collectedAt           time.Time
}

type dockerCollector struct {
	client *client.Client

	mu          sync.Mutex
	previous    map[string]dockerBlockSample
	retryAfter  time.Time
	lastFailure error
}

func newDockerCollector() (*dockerCollector, error) {
	api, err := client.New()
	if err != nil {
		return nil, err
	}
	host := strings.ToLower(api.DaemonHost())
	if !strings.HasPrefix(host, "unix://") && !strings.HasPrefix(host, "npipe://") {
		_ = api.Close()
		return nil, fmt.Errorf("refusing non-local Docker endpoint")
	}
	return &dockerCollector{client: api, previous: make(map[string]dockerBlockSample)}, nil
}

func (c *dockerCollector) Close() error {
	if c == nil || c.client == nil {
		return nil
	}
	return c.client.Close()
}

func (c *dockerCollector) Snapshot(ctx context.Context, logicalCores int, now time.Time) ([]RawContainer, []string, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if now.Before(c.retryAfter) {
		return nil, nil, false, c.lastFailure
	}

	listContext, cancelList := context.WithTimeout(ctx, dockerListTimeout)
	list, err := c.client.ContainerList(listContext, client.ContainerListOptions{All: true})
	cancelList()
	if err != nil {
		c.markUnavailable(now, err)
		return nil, nil, false, err
	}
	c.retryAfter = time.Time{}
	c.lastFailure = nil

	containers := make([]RawContainer, len(list.Items))
	ids := make([]string, len(list.Items))
	running := make([]int, 0, len(list.Items))
	for index, summary := range list.Items {
		containers[index] = deriveDockerSummary(summary)
		ids[index] = summary.ID
		if containerStateRunning(string(summary.State)) {
			running = append(running, index)
		}
	}
	if len(running) == 0 {
		c.previous = make(map[string]dockerBlockSample)
		return containers, ids, true, nil
	}
	type result struct {
		index     int
		container RawContainer
		sample    dockerBlockSample
		err       error
	}
	results := make(chan result, len(list.Items))
	work := make(chan int)
	statsContext, cancelStats := context.WithTimeout(ctx, dockerStatsTimeout)
	defer cancelStats()

	workers := min(dockerWorkers, len(running))
	var group sync.WaitGroup
	group.Add(workers)
	for range workers {
		go func() {
			defer group.Done()
			for index := range work {
				summary := list.Items[index]
				container, sample, err := c.collectContainer(statsContext, summary, logicalCores, now)
				results <- result{index: index, container: container, sample: sample, err: err}
			}
		}()
	}
	go func() {
		for _, index := range running {
			work <- index
		}
		close(work)
		group.Wait()
		close(results)
	}()

	nextPrevious := make(map[string]dockerBlockSample, len(list.Items))
	failures := 0
	for item := range results {
		summary := list.Items[item.index]
		if item.err != nil {
			failures++
			continue
		}
		containers[item.index] = item.container
		nextPrevious[summary.ID] = item.sample
	}
	c.previous = nextPrevious
	if failures > 0 {
		return containers, ids, true, fmt.Errorf("Docker statistics unavailable for %d container(s)", failures)
	}
	return containers, ids, true, nil
}

func (c *dockerCollector) collectContainer(ctx context.Context, summary containertypes.Summary, logicalCores int, now time.Time) (RawContainer, dockerBlockSample, error) {
	response, err := c.client.ContainerStats(ctx, summary.ID, client.ContainerStatsOptions{
		Stream:                false,
		IncludePreviousSample: true,
	})
	if err != nil {
		return RawContainer{}, dockerBlockSample{}, err
	}
	defer response.Body.Close()
	var stats containertypes.StatsResponse
	if err := json.NewDecoder(response.Body).Decode(&stats); err != nil {
		return RawContainer{}, dockerBlockSample{}, err
	}
	container, sample := deriveDockerContainer(summary, stats, c.previous[summary.ID], logicalCores, now)
	return container, sample, nil
}

func deriveDockerContainer(summary containertypes.Summary, stats containertypes.StatsResponse, previous dockerBlockSample, logicalCores int, now time.Time) (RawContainer, dockerBlockSample) {
	readBytes, writeBytes := dockerBlockTotals(stats)
	collectedAt := stats.Read
	if collectedAt.IsZero() {
		collectedAt = now
	}
	sample := dockerBlockSample{readBytes: readBytes, writeBytes: writeBytes, collectedAt: collectedAt}
	readRate, writeRate := 0.0, 0.0
	elapsed := collectedAt.Sub(previous.collectedAt).Seconds()
	if elapsed > 0 && readBytes >= previous.readBytes && writeBytes >= previous.writeBytes {
		readRate = float64(readBytes-previous.readBytes) / elapsed
		writeRate = float64(writeBytes-previous.writeBytes) / elapsed
	}

	memoryBytes := dockerMemoryUsage(stats.MemoryStats)
	processCount := int(stats.PidsStats.Current)
	if processCount == 0 {
		processCount = int(stats.NumProcs)
	}
	container := deriveDockerSummary(summary)
	container.CPUPercent = dockerCPUPercent(stats, logicalCores)
	container.MemoryBytes = memoryBytes
	container.MemoryLimitBytes = stats.MemoryStats.Limit
	container.ReadBytesPerSecond = readRate
	container.WriteBytesPerSecond = writeRate
	container.ProcessCount = processCount
	return container, sample
}

func deriveDockerSummary(summary containertypes.Summary) RawContainer {
	health := ""
	if summary.Health != nil {
		health = string(summary.Health.Status)
	}
	return RawContainer{
		ID: summary.ID, Name: containerName(summary), Image: summary.Image,
		State: string(summary.State), Status: summary.Status, Health: health,
		ComposeProject: summary.Labels["com.docker.compose.project"],
		ComposeService: summary.Labels["com.docker.compose.service"],
		PublishedPorts: dockerPublishedPorts(summary.Ports),
	}
}

func dockerPublishedPorts(ports []containertypes.PortSummary) []string {
	result := make([]string, 0, len(ports))
	for _, port := range ports {
		containerEndpoint := strconv.Itoa(int(port.PrivatePort)) + "/" + port.Type
		if port.PublicPort == 0 {
			continue
		}
		host := port.IP
		if !host.IsValid() || host == netip.IPv4Unspecified() {
			host = netip.MustParseAddr("0.0.0.0")
		}
		result = append(result, netip.AddrPortFrom(host, port.PublicPort).String()+" -> "+containerEndpoint)
	}
	sort.Strings(result)
	return result
}

func (c *dockerCollector) OperateContainer(ctx context.Context, name string, action ContainerAction) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	operationContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	list, err := c.client.ContainerList(operationContext, client.ContainerListOptions{All: true})
	if err != nil {
		return err
	}
	containerID := ""
	for _, summary := range list.Items {
		if normalizeContainerName(containerName(summary)) == normalizeContainerName(name) {
			containerID = summary.ID
			break
		}
	}
	if containerID == "" {
		return errors.New("container is no longer present in Docker")
	}
	switch action {
	case ContainerStart:
		_, err = c.client.ContainerStart(operationContext, containerID, client.ContainerStartOptions{})
	case ContainerStop:
		_, err = c.client.ContainerStop(operationContext, containerID, client.ContainerStopOptions{})
	case ContainerRestart:
		_, err = c.client.ContainerRestart(operationContext, containerID, client.ContainerRestartOptions{})
	default:
		return errors.New("unsupported container action")
	}
	return err
}

func dockerMemoryUsage(memory containertypes.MemoryStats) uint64 {
	if memory.PrivateWorkingSet > 0 {
		return memory.PrivateWorkingSet
	}
	cache := memory.Stats["total_inactive_file"]
	if cache == 0 {
		cache = memory.Stats["inactive_file"]
	}
	if cache < memory.Usage {
		return memory.Usage - cache
	}
	return memory.Usage
}

func dockerCPUPercent(stats containertypes.StatsResponse, logicalCores int) float64 {
	current := stats.CPUStats.CPUUsage.TotalUsage
	previous := stats.PreCPUStats.CPUUsage.TotalUsage
	if current < previous {
		return 0
	}
	cpuDelta := current - previous
	systemCurrent := stats.CPUStats.SystemUsage
	systemPrevious := stats.PreCPUStats.SystemUsage
	if systemCurrent > systemPrevious {
		return min(100, float64(cpuDelta)/float64(systemCurrent-systemPrevious)*100)
	}
	elapsed := stats.Read.Sub(stats.PreRead).Seconds()
	if elapsed <= 0 || logicalCores < 1 {
		return 0
	}
	return min(100, float64(cpuDelta)/10_000_000/elapsed/float64(logicalCores)*100)
}

func dockerBlockTotals(stats containertypes.StatsResponse) (uint64, uint64) {
	readBytes := stats.StorageStats.ReadSizeBytes
	writeBytes := stats.StorageStats.WriteSizeBytes
	for _, entry := range stats.BlkioStats.IoServiceBytesRecursive {
		switch strings.ToLower(entry.Op) {
		case "read":
			readBytes += entry.Value
		case "write":
			writeBytes += entry.Value
		}
	}
	return readBytes, writeBytes
}

func containerName(summary containertypes.Summary) string {
	if len(summary.Names) > 0 {
		return strings.TrimPrefix(summary.Names[0], "/")
	}
	if len(summary.ID) > 12 {
		return summary.ID[:12]
	}
	return summary.ID
}

func (c *dockerCollector) markUnavailable(now time.Time, err error) {
	c.retryAfter = now.Add(dockerRetryDelay)
	c.lastFailure = err
}
