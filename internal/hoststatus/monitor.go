package hoststatus

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"runtime"
	"sort"
	"sync"
	"time"

	"scriptboard/internal/diskspace"
	"scriptboard/internal/secretredaction"
)

const (
	Range15Minutes = "15m"
	Range1Hour     = "1h"
	Range6Hours    = "6h"
	Range24Hours   = "24h"
)

type Facts struct {
	Hostname         string    `json:"hostname"`
	OS               string    `json:"os"`
	Platform         string    `json:"platform"`
	PlatformVersion  string    `json:"platformVersion"`
	Architecture     string    `json:"architecture"`
	CPUModel         string    `json:"cpuModel"`
	LogicalCores     int       `json:"logicalCores"`
	TotalMemoryBytes uint64    `json:"totalMemoryBytes"`
	BootedAt         time.Time `json:"bootedAt"`
	ServiceStartedAt time.Time `json:"serviceStartedAt"`
}

type CPUCounters struct {
	TotalSeconds  float64
	IdleSeconds   float64
	UserSeconds   float64
	SystemSeconds float64
	IOWaitSeconds float64
}

type DiskCounters struct {
	ReadBytes, WriteBytes uint64
	Reads, Writes         uint64
	ReadTimeMS            uint64
	WriteTimeMS           uint64
}

type NetworkCounters struct {
	ReceivedBytes, SentBytes                         uint64
	ReceivedErrors, SentErrors, ReceivedDrops, Drops uint64
}

type CPU struct {
	UsedPercent, UserPercent, SystemPercent, IOWaitPercent float64  `json:",omitempty"`
	Load1, Load5, Load15                                   *float64 `json:",omitempty"`
}

type Memory struct {
	TotalBytes, AvailableBytes, UsedBytes uint64  `json:",omitempty"`
	UsedPercent                           float64 `json:",omitempty"`
	SwapTotalBytes, SwapUsedBytes         uint64  `json:",omitempty"`
	SwapUsedPercent                       float64 `json:",omitempty"`
	CommittedBytes, CommitLimitBytes      uint64  `json:",omitempty"`
}

type Filesystem struct {
	ID, Device, Mountpoint, Type string   `json:",omitempty"`
	TotalBytes, UsedBytes        uint64   `json:",omitempty"`
	AvailableBytes               uint64   `json:",omitempty"`
	UsedPercent                  float64  `json:",omitempty"`
	Roles                        []string `json:",omitempty"`
	Online                       bool     `json:"online"`
}

type Disk struct {
	ID, Name                                          string   `json:",omitempty"`
	ReadBytesPerSecond, WriteBytesPerSecond           float64  `json:",omitempty"`
	ReadOperationsPerSecond, WriteOperationsPerSecond float64  `json:",omitempty"`
	ReadLatencyMS, WriteLatencyMS                     *float64 `json:",omitempty"`
	Online                                            bool     `json:"online"`
}

type DiskSummary struct {
	ReadBytesPerSecond, WriteBytesPerSecond           float64 `json:",omitempty"`
	ReadOperationsPerSecond, WriteOperationsPerSecond float64 `json:",omitempty"`
}

type NetworkInterface struct {
	ID, Name                   string   `json:",omitempty"`
	Addresses                  []string `json:",omitempty"`
	ReceivedBytesPerSecond     float64  `json:",omitempty"`
	SentBytesPerSecond         float64  `json:",omitempty"`
	ReceivedErrors, SentErrors uint64   `json:",omitempty"`
	ReceivedDrops, SentDrops   uint64   `json:",omitempty"`
	Online                     bool     `json:"online"`
}

type NetworkSummary struct {
	ReceivedBytesPerSecond, SentBytesPerSecond float64 `json:",omitempty"`
	ReceivedErrors, SentErrors                 uint64  `json:",omitempty"`
	ReceivedDrops, SentDrops                   uint64  `json:",omitempty"`
}

type Process struct {
	CPUPercent          float64 `json:",omitempty"`
	ResidentMemoryBytes uint64  `json:",omitempty"`
	Threads             int32   `json:",omitempty"`
	OpenFiles, Handles  int32   `json:",omitempty"`
}

type InterfaceInfo struct {
	ID, Name  string
	Addresses []string
}

type RawSample struct {
	At          time.Time
	CPU         *CPUCounters
	Memory      *Memory
	Filesystems []Filesystem
	Disks       map[string]DiskCounters
	Networks    map[string]NetworkCounters
	Interfaces  map[string]InterfaceInfo
	Process     *Process
	Load        *[3]float64
	Errors      map[string]string
}

type Sample struct {
	At          time.Time          `json:"at"`
	CPU         *CPU               `json:"cpu,omitempty"`
	Memory      *Memory            `json:"memory,omitempty"`
	Storage     *Filesystem        `json:"storage,omitempty"`
	Filesystems []Filesystem       `json:"filesystems,omitempty"`
	Disk        *DiskSummary       `json:"disk,omitempty"`
	Disks       []Disk             `json:"disks,omitempty"`
	Network     *NetworkSummary    `json:"network,omitempty"`
	Interfaces  []NetworkInterface `json:"interfaces,omitempty"`
	Process     *Process           `json:"process,omitempty"`
	Errors      map[string]string  `json:"errors,omitempty"`
}

type MetricValues struct {
	Values      map[string]float64            `json:"values,omitempty"`
	Filesystems map[string]map[string]float64 `json:"filesystems,omitempty"`
	Disks       map[string]map[string]float64 `json:"disks,omitempty"`
	Networks    map[string]map[string]float64 `json:"networks,omitempty"`
}

type SeriesPoint struct {
	At      time.Time    `json:"at"`
	Average MetricValues `json:"average"`
	Maximum MetricValues `json:"maximum"`
}

type Overview struct {
	Facts        Facts             `json:"facts"`
	Current      Sample            `json:"current"`
	Series       []SeriesPoint     `json:"series"`
	Capabilities map[string]bool   `json:"capabilities"`
	Errors       map[string]string `json:"errors,omitempty"`
	CollectedAt  time.Time         `json:"collectedAt"`
	Stale        bool              `json:"stale"`
}

type Probe interface {
	Facts(context.Context) (Facts, error)
	Sample(context.Context) RawSample
}

type Options struct {
	Interval           time.Duration
	Retention          time.Duration
	Now                func() time.Time
	SkipInitialCleanup bool
}

type Monitor struct {
	db         *sql.DB
	probe      Probe
	options    Options
	facts      Facts
	factErr    string
	historyErr string

	mu       sync.RWMutex
	current  Sample
	previous *RawSample
	live     []Sample
	bucket   *minuteAccumulator

	cancel context.CancelFunc
	done   chan struct{}
}

func New(db *sql.DB, probe Probe, options Options) (*Monitor, error) {
	if db == nil || probe == nil {
		return nil, errors.New("host status database and probe are required")
	}
	if options.Interval <= 0 {
		options.Interval = 5 * time.Second
	}
	if options.Retention <= 0 {
		options.Retention = 24 * time.Hour
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	facts, err := probe.Facts(context.Background())
	monitor := &Monitor{db: db, probe: probe, options: options, facts: facts, done: make(chan struct{})}
	if err != nil {
		monitor.factErr = secretredaction.String(err.Error())
	}
	if !options.SkipInitialCleanup {
		if cleanupErr := monitor.cleanup(context.Background()); cleanupErr != nil {
			monitor.historyErr = secretredaction.String(cleanupErr.Error())
		}
	}
	return monitor, nil
}

func (m *Monitor) Start(parent context.Context) {
	m.mu.Lock()
	if m.cancel != nil {
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	m.cancel = cancel
	m.mu.Unlock()
	go func() {
		defer close(m.done)
		m.Collect(ctx)
		ticker := time.NewTicker(m.options.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.Collect(ctx)
			}
		}
	}()
}

func (m *Monitor) Close() {
	m.mu.Lock()
	cancel := m.cancel
	m.mu.Unlock()
	if cancel != nil {
		cancel()
		<-m.done
	}
}

func (m *Monitor) Collect(ctx context.Context) {
	raw := m.probe.Sample(ctx)
	if raw.At.IsZero() {
		raw.At = m.options.Now().UTC()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	sample := m.derive(raw)
	m.previous = &raw
	m.current = sample
	if sample.At.IsZero() {
		return
	}
	cutoff := sample.At.Add(-15 * time.Minute)
	m.live = append(m.live, sample)
	first := 0
	for first < len(m.live) && m.live[first].At.Before(cutoff) {
		first++
	}
	if first > 0 {
		m.live = append([]Sample(nil), m.live[first:]...)
	}
	bucketAt := sample.At.Truncate(time.Minute)
	if m.bucket == nil {
		m.bucket = newMinuteAccumulator(bucketAt)
	} else if !m.bucket.at.Equal(bucketAt) {
		if err := m.persist(m.bucket); err != nil {
			m.historyErr = secretredaction.String(err.Error())
		} else {
			m.historyErr = ""
		}
		m.bucket = newMinuteAccumulator(bucketAt)
		if err := m.cleanup(ctx); err != nil {
			m.historyErr = secretredaction.String(err.Error())
		}
	}
	m.bucket.add(sampleMetrics(sample))
}

func (m *Monitor) derive(raw RawSample) Sample {
	result := Sample{At: raw.At, Memory: raw.Memory, Filesystems: append([]Filesystem(nil), raw.Filesystems...), Process: raw.Process, Errors: cloneErrors(raw.Errors)}
	result.Storage = constrainedFilesystem(result.Filesystems)
	if m.previous == nil {
		return result
	}
	seconds := raw.At.Sub(m.previous.At).Seconds()
	if seconds <= 0 {
		return result
	}
	if raw.CPU != nil && m.previous.CPU != nil {
		total := raw.CPU.TotalSeconds - m.previous.CPU.TotalSeconds
		idle := raw.CPU.IdleSeconds - m.previous.CPU.IdleSeconds
		if total > 0 && idle >= 0 && idle <= total {
			result.CPU = &CPU{
				UsedPercent:   clampPercent((total - idle) / total * 100),
				UserPercent:   clampPercent((raw.CPU.UserSeconds - m.previous.CPU.UserSeconds) / total * 100),
				SystemPercent: clampPercent((raw.CPU.SystemSeconds - m.previous.CPU.SystemSeconds) / total * 100),
				IOWaitPercent: clampPercent((raw.CPU.IOWaitSeconds - m.previous.CPU.IOWaitSeconds) / total * 100),
			}
			if raw.Load != nil {
				result.CPU.Load1, result.CPU.Load5, result.CPU.Load15 = floatPointer(raw.Load[0]), floatPointer(raw.Load[1]), floatPointer(raw.Load[2])
			}
		}
	}
	result.Disks, result.Disk = deriveDisks(m.previous.Disks, raw.Disks, seconds)
	result.Interfaces, result.Network = deriveNetworks(m.previous.Networks, raw.Networks, raw.Interfaces, seconds)
	return result
}

func deriveDisks(previous, current map[string]DiskCounters, seconds float64) ([]Disk, *DiskSummary) {
	var values []Disk
	summary := &DiskSummary{}
	for name, now := range current {
		before, ok := previous[name]
		if !ok || now.ReadBytes < before.ReadBytes || now.WriteBytes < before.WriteBytes || now.Reads < before.Reads || now.Writes < before.Writes {
			continue
		}
		value := Disk{ID: name, Name: name, Online: true,
			ReadBytesPerSecond:       float64(now.ReadBytes-before.ReadBytes) / seconds,
			WriteBytesPerSecond:      float64(now.WriteBytes-before.WriteBytes) / seconds,
			ReadOperationsPerSecond:  float64(now.Reads-before.Reads) / seconds,
			WriteOperationsPerSecond: float64(now.Writes-before.Writes) / seconds,
		}
		if operations := now.Reads - before.Reads; operations > 0 && now.ReadTimeMS >= before.ReadTimeMS {
			value.ReadLatencyMS = floatPointer(float64(now.ReadTimeMS-before.ReadTimeMS) / float64(operations))
		}
		if operations := now.Writes - before.Writes; operations > 0 && now.WriteTimeMS >= before.WriteTimeMS {
			value.WriteLatencyMS = floatPointer(float64(now.WriteTimeMS-before.WriteTimeMS) / float64(operations))
		}
		summary.ReadBytesPerSecond += value.ReadBytesPerSecond
		summary.WriteBytesPerSecond += value.WriteBytesPerSecond
		summary.ReadOperationsPerSecond += value.ReadOperationsPerSecond
		summary.WriteOperationsPerSecond += value.WriteOperationsPerSecond
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Name < values[j].Name })
	if len(values) == 0 {
		return nil, nil
	}
	return values, summary
}

func deriveNetworks(previous, current map[string]NetworkCounters, info map[string]InterfaceInfo, seconds float64) ([]NetworkInterface, *NetworkSummary) {
	var values []NetworkInterface
	summary := &NetworkSummary{}
	for name, now := range current {
		before, ok := previous[name]
		if !ok || now.ReceivedBytes < before.ReceivedBytes || now.SentBytes < before.SentBytes {
			continue
		}
		meta := info[name]
		value := NetworkInterface{ID: name, Name: name, Addresses: append([]string(nil), meta.Addresses...), Online: true,
			ReceivedBytesPerSecond: float64(now.ReceivedBytes-before.ReceivedBytes) / seconds,
			SentBytesPerSecond:     float64(now.SentBytes-before.SentBytes) / seconds,
			ReceivedErrors:         now.ReceivedErrors, SentErrors: now.SentErrors, ReceivedDrops: now.ReceivedDrops, SentDrops: now.Drops,
		}
		summary.ReceivedBytesPerSecond += value.ReceivedBytesPerSecond
		summary.SentBytesPerSecond += value.SentBytesPerSecond
		summary.ReceivedErrors += value.ReceivedErrors
		summary.SentErrors += value.SentErrors
		summary.ReceivedDrops += value.ReceivedDrops
		summary.SentDrops += value.SentDrops
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Name < values[j].Name })
	if len(values) == 0 {
		return nil, nil
	}
	return values, summary
}

func (m *Monitor) Overview(ctx context.Context, selectedRange string) (Overview, error) {
	duration, ok := rangeDuration(selectedRange)
	if !ok {
		return Overview{}, errors.New("invalid host status range")
	}
	m.mu.RLock()
	current := m.current
	facts := m.facts
	factErr := m.factErr
	historyErr := m.historyErr
	live := append([]Sample(nil), m.live...)
	m.mu.RUnlock()
	result := Overview{Facts: facts, Current: current, CollectedAt: current.At, Capabilities: capabilities(current), Errors: cloneErrors(current.Errors)}
	if current.Storage != nil && current.Storage.AvailableBytes < diskspace.MinimumWritableBytes {
		if result.Errors == nil {
			result.Errors = map[string]string{}
		}
		lowSpace := fmt.Sprintf("关键卷 %s 可用空间低于 100 MiB 可写下限", current.Storage.Mountpoint)
		if existing := result.Errors["storage"]; existing != "" {
			result.Errors["storage"] = existing + "；" + lowSpace
		} else {
			result.Errors["storage"] = lowSpace
		}
	}
	if factErr != "" {
		if result.Errors == nil {
			result.Errors = map[string]string{}
		}
		result.Errors["host"] = factErr
	}
	if historyErr != "" {
		if result.Errors == nil {
			result.Errors = map[string]string{}
		}
		result.Errors["history"] = historyErr
	}
	result.Stale = current.At.IsZero() || m.options.Now().Sub(current.At) > 15*time.Second
	if selectedRange == Range15Minutes {
		cutoff := m.options.Now().Add(-duration)
		for _, sample := range live {
			if !sample.At.Before(cutoff) {
				values := sampleMetrics(sample)
				result.Series = append(result.Series, SeriesPoint{At: sample.At, Average: values, Maximum: values})
			}
		}
		return result, nil
	}
	rows, err := m.db.QueryContext(ctx, "SELECT bucket_at, average_json, maximum_json FROM host_metric_minutes WHERE bucket_at >= ? ORDER BY bucket_at", m.options.Now().Add(-duration).Unix())
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var timestamp int64
		var averageJSON, maximumJSON string
		if err := rows.Scan(&timestamp, &averageJSON, &maximumJSON); err != nil {
			return result, err
		}
		var average, maximum MetricValues
		if json.Unmarshal([]byte(averageJSON), &average) != nil || json.Unmarshal([]byte(maximumJSON), &maximum) != nil {
			continue
		}
		result.Series = append(result.Series, SeriesPoint{At: time.Unix(timestamp, 0).UTC(), Average: average, Maximum: maximum})
	}
	return result, rows.Err()
}

func rangeDuration(value string) (time.Duration, bool) {
	switch value {
	case Range15Minutes:
		return 15 * time.Minute, true
	case Range1Hour, "":
		return time.Hour, true
	case Range6Hours:
		return 6 * time.Hour, true
	case Range24Hours:
		return 24 * time.Hour, true
	default:
		return 0, false
	}
}

func (m *Monitor) persist(bucket *minuteAccumulator) error {
	average, maximum := bucket.values()
	averageJSON, err := json.Marshal(average)
	if err != nil {
		return err
	}
	maximumJSON, err := json.Marshal(maximum)
	if err != nil {
		return err
	}
	_, err = m.db.Exec(`INSERT INTO host_metric_minutes(bucket_at, sample_count, average_json, maximum_json)
		VALUES (?, ?, ?, ?) ON CONFLICT(bucket_at) DO UPDATE SET sample_count=excluded.sample_count, average_json=excluded.average_json, maximum_json=excluded.maximum_json`, bucket.at.Unix(), bucket.count, string(averageJSON), string(maximumJSON))
	return err
}

func (m *Monitor) cleanup(ctx context.Context) error {
	_, err := m.db.ExecContext(ctx, "DELETE FROM host_metric_minutes WHERE bucket_at < ?", m.options.Now().Add(-m.options.Retention).Unix())
	return err
}

type metricAccumulator struct {
	sum, max map[string]float64
	count    map[string]int
}
type minuteAccumulator struct {
	at              time.Time
	count           int
	valuesAcc       metricAccumulator
	fs, disks, nets map[string]*metricAccumulator
}

func newMinuteAccumulator(at time.Time) *minuteAccumulator {
	return &minuteAccumulator{at: at, valuesAcc: newMetricAccumulator(), fs: map[string]*metricAccumulator{}, disks: map[string]*metricAccumulator{}, nets: map[string]*metricAccumulator{}}
}
func newMetricAccumulator() metricAccumulator {
	return metricAccumulator{sum: map[string]float64{}, max: map[string]float64{}, count: map[string]int{}}
}
func (a *metricAccumulator) add(values map[string]float64) {
	for key, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			continue
		}
		a.sum[key] += value
		a.count[key]++
		if a.count[key] == 1 || value > a.max[key] {
			a.max[key] = value
		}
	}
}
func (a *metricAccumulator) output() (map[string]float64, map[string]float64) {
	average, maximum := map[string]float64{}, map[string]float64{}
	for key, sum := range a.sum {
		average[key] = sum / float64(a.count[key])
		maximum[key] = a.max[key]
	}
	return average, maximum
}
func (a *minuteAccumulator) add(values MetricValues) {
	a.count++
	a.valuesAcc.add(values.Values)
	addDeviceValues(a.fs, values.Filesystems)
	addDeviceValues(a.disks, values.Disks)
	addDeviceValues(a.nets, values.Networks)
}
func addDeviceValues(target map[string]*metricAccumulator, values map[string]map[string]float64) {
	for id, metrics := range values {
		if target[id] == nil {
			value := newMetricAccumulator()
			target[id] = &value
		}
		target[id].add(metrics)
	}
}
func (a *minuteAccumulator) values() (MetricValues, MetricValues) {
	avgValues, maxValues := a.valuesAcc.output()
	average := MetricValues{Values: avgValues, Filesystems: map[string]map[string]float64{}, Disks: map[string]map[string]float64{}, Networks: map[string]map[string]float64{}}
	maximum := MetricValues{Values: maxValues, Filesystems: map[string]map[string]float64{}, Disks: map[string]map[string]float64{}, Networks: map[string]map[string]float64{}}
	outputDeviceValues(a.fs, average.Filesystems, maximum.Filesystems)
	outputDeviceValues(a.disks, average.Disks, maximum.Disks)
	outputDeviceValues(a.nets, average.Networks, maximum.Networks)
	return average, maximum
}
func outputDeviceValues(source map[string]*metricAccumulator, average, maximum map[string]map[string]float64) {
	for id, accumulator := range source {
		average[id], maximum[id] = accumulator.output()
	}
}

func sampleMetrics(sample Sample) MetricValues {
	result := MetricValues{Values: map[string]float64{}, Filesystems: map[string]map[string]float64{}, Disks: map[string]map[string]float64{}, Networks: map[string]map[string]float64{}}
	if sample.CPU != nil {
		result.Values["cpu.usedPercent"] = sample.CPU.UsedPercent
	}
	if sample.Memory != nil {
		result.Values["memory.usedPercent"] = sample.Memory.UsedPercent
		result.Values["memory.usedBytes"] = float64(sample.Memory.UsedBytes)
	}
	if sample.Storage != nil {
		result.Values["storage.usedPercent"] = sample.Storage.UsedPercent
		result.Values["storage.availableBytes"] = float64(sample.Storage.AvailableBytes)
	}
	if sample.Disk != nil {
		result.Values["disk.readBytesPerSecond"] = sample.Disk.ReadBytesPerSecond
		result.Values["disk.writeBytesPerSecond"] = sample.Disk.WriteBytesPerSecond
	}
	if sample.Network != nil {
		result.Values["network.receivedBytesPerSecond"] = sample.Network.ReceivedBytesPerSecond
		result.Values["network.sentBytesPerSecond"] = sample.Network.SentBytesPerSecond
	}
	if sample.Process != nil {
		result.Values["process.cpuPercent"] = sample.Process.CPUPercent
		result.Values["process.residentMemoryBytes"] = float64(sample.Process.ResidentMemoryBytes)
	}
	for _, value := range sample.Filesystems {
		result.Filesystems[value.ID] = map[string]float64{"usedPercent": value.UsedPercent, "availableBytes": float64(value.AvailableBytes)}
	}
	for _, value := range sample.Disks {
		result.Disks[value.ID] = map[string]float64{"readBytesPerSecond": value.ReadBytesPerSecond, "writeBytesPerSecond": value.WriteBytesPerSecond}
	}
	for _, value := range sample.Interfaces {
		result.Networks[value.ID] = map[string]float64{"receivedBytesPerSecond": value.ReceivedBytesPerSecond, "sentBytesPerSecond": value.SentBytesPerSecond}
	}
	return result
}

func constrainedFilesystem(filesystems []Filesystem) *Filesystem {
	var selected *Filesystem
	for index := range filesystems {
		if len(filesystems[index].Roles) == 0 {
			continue
		}
		if selected == nil || filesystems[index].UsedPercent > selected.UsedPercent {
			copy := filesystems[index]
			selected = &copy
		}
	}
	return selected
}
func capabilities(sample Sample) map[string]bool {
	return map[string]bool{
		"cpu": sample.CPU != nil, "memory": sample.Memory != nil,
		"storage": len(sample.Filesystems) > 0, "disk": sample.Disk != nil,
		"network": sample.Network != nil, "process": sample.Process != nil,
		"loadAverage": runtime.GOOS != "windows", "cpuIOWait": runtime.GOOS != "windows",
		"committedMemory":  runtime.GOOS == "windows",
		"diskLatency":      len(sample.Disks) > 0,
		"processOpenFiles": runtime.GOOS != "windows", "processHandles": runtime.GOOS == "windows",
	}
}
func cloneErrors(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	result := map[string]string{}
	for k, v := range values {
		result[k] = v
	}
	return result
}
func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}
func floatPointer(value float64) *float64 { copy := value; return &copy }
