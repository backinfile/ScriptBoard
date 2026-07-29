package appstatus

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

const metricRetention = 25 * time.Hour

var ErrUnsupportedHistoryRange = errors.New("unsupported application history range")

type HistoryPoint struct {
	At            time.Time `json:"at"`
	SampleCount   int       `json:"sampleCount"`
	CPUAverage    float64   `json:"cpuAverage"`
	CPUMaximum    float64   `json:"cpuMaximum"`
	MemoryAverage uint64    `json:"memoryAverage"`
	MemoryMaximum uint64    `json:"memoryMaximum"`
	ReadAverage   float64   `json:"readAverage"`
	ReadMaximum   float64   `json:"readMaximum"`
	WriteAverage  float64   `json:"writeAverage"`
	WriteMaximum  float64   `json:"writeMaximum"`
}

type History struct {
	Range         string         `json:"range"`
	From          time.Time      `json:"from"`
	To            time.Time      `json:"to"`
	BucketSeconds int            `json:"bucketSeconds"`
	Points        []HistoryPoint `json:"points"`
}

func historyDuration(selectedRange string) (time.Duration, bool) {
	switch selectedRange {
	case "15m":
		return 15 * time.Minute, true
	case "", "1h":
		return time.Hour, true
	case "6h":
		return 6 * time.Hour, true
	case "24h":
		return 24 * time.Hour, true
	default:
		return 0, false
	}
}

func historyBucketSeconds(selectedRange string) int {
	switch selectedRange {
	case "6h":
		return 5 * 60
	case "24h":
		return 20 * 60
	default:
		return 60
	}
}

func (m *Monitor) History(ctx context.Context, applicationID, selectedRange string) (History, error) {
	duration, ok := historyDuration(selectedRange)
	if !ok {
		return History{}, ErrUnsupportedHistoryRange
	}
	if selectedRange == "" {
		selectedRange = "1h"
	}
	applicationID = strings.TrimSpace(applicationID)
	if applicationID == "" {
		return History{}, errors.New("application id is required")
	}
	to := m.options.Now().UTC()
	from := to.Add(-duration)
	bucketSeconds := historyBucketSeconds(selectedRange)
	result := History{
		Range: selectedRange, From: from, To: to, BucketSeconds: bucketSeconds,
		Points: make([]HistoryPoint, 0),
	}
	rows, err := m.db.QueryContext(ctx, `SELECT bucket_at, sample_count,
		cpu_average, cpu_maximum, memory_average, memory_maximum,
		read_average, read_maximum, write_average, write_maximum
		FROM application_metric_minutes
		WHERE application_id = ? AND bucket_at >= ? AND bucket_at <= ?
		ORDER BY bucket_at`, applicationID, from.Truncate(time.Minute).Unix(), to.Unix())
	if err != nil {
		return History{}, err
	}
	defer rows.Close()
	var bucket historyPointAccumulator
	flush := func() {
		if bucket.sampleCount == 0 {
			return
		}
		result.Points = append(result.Points, bucket.point())
		bucket = historyPointAccumulator{}
	}
	for rows.Next() {
		var timestamp int64
		var point HistoryPoint
		if err := rows.Scan(
			&timestamp, &point.SampleCount,
			&point.CPUAverage, &point.CPUMaximum,
			&point.MemoryAverage, &point.MemoryMaximum,
			&point.ReadAverage, &point.ReadMaximum,
			&point.WriteAverage, &point.WriteMaximum,
		); err != nil {
			return History{}, err
		}
		point.At = time.Unix(timestamp, 0).UTC()
		bucketAt := timestamp / int64(bucketSeconds) * int64(bucketSeconds)
		if bucket.sampleCount > 0 && bucket.at.Unix() != bucketAt {
			flush()
		}
		if bucket.sampleCount == 0 {
			bucket.at = time.Unix(bucketAt, 0).UTC()
		}
		bucket.add(point)
	}
	if err := rows.Err(); err != nil {
		return History{}, err
	}
	flush()
	return result, nil
}

type historyPointAccumulator struct {
	at                                   time.Time
	sampleCount                          int
	cpuSum, memorySum, readSum, writeSum float64
	cpuMax, memoryMax, readMax, writeMax float64
}

func (a *historyPointAccumulator) add(point HistoryPoint) {
	if point.SampleCount <= 0 {
		return
	}
	count := float64(point.SampleCount)
	a.sampleCount += point.SampleCount
	a.cpuSum += point.CPUAverage * count
	a.memorySum += float64(point.MemoryAverage) * count
	a.readSum += point.ReadAverage * count
	a.writeSum += point.WriteAverage * count
	a.cpuMax = max(a.cpuMax, point.CPUMaximum)
	a.memoryMax = max(a.memoryMax, float64(point.MemoryMaximum))
	a.readMax = max(a.readMax, point.ReadMaximum)
	a.writeMax = max(a.writeMax, point.WriteMaximum)
}

func (a historyPointAccumulator) point() HistoryPoint {
	count := float64(a.sampleCount)
	return HistoryPoint{
		At: a.at, SampleCount: a.sampleCount,
		CPUAverage: a.cpuSum / count, CPUMaximum: a.cpuMax,
		MemoryAverage: uint64(math.Round(a.memorySum / count)), MemoryMaximum: uint64(a.memoryMax),
		ReadAverage: a.readSum / count, ReadMaximum: a.readMax,
		WriteAverage: a.writeSum / count, WriteMaximum: a.writeMax,
	}
}

func (m *Monitor) persistMetricSamples(ctx context.Context, collectedAt time.Time, applications []Application) error {
	if collectedAt.IsZero() || len(applications) == 0 {
		return nil
	}
	transaction, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	const statement = `INSERT INTO application_metric_minutes (
			application_id, bucket_at, sample_count,
			cpu_average, cpu_maximum, memory_average, memory_maximum,
			read_average, read_maximum, write_average, write_maximum
		) VALUES (?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(application_id, bucket_at) DO UPDATE SET
			cpu_average = (application_metric_minutes.cpu_average * application_metric_minutes.sample_count + excluded.cpu_average) / (application_metric_minutes.sample_count + 1),
			cpu_maximum = MAX(application_metric_minutes.cpu_maximum, excluded.cpu_maximum),
			memory_average = CAST((application_metric_minutes.memory_average * application_metric_minutes.sample_count + excluded.memory_average) / (application_metric_minutes.sample_count + 1) AS INTEGER),
			memory_maximum = MAX(application_metric_minutes.memory_maximum, excluded.memory_maximum),
			read_average = (application_metric_minutes.read_average * application_metric_minutes.sample_count + excluded.read_average) / (application_metric_minutes.sample_count + 1),
			read_maximum = MAX(application_metric_minutes.read_maximum, excluded.read_maximum),
			write_average = (application_metric_minutes.write_average * application_metric_minutes.sample_count + excluded.write_average) / (application_metric_minutes.sample_count + 1),
			write_maximum = MAX(application_metric_minutes.write_maximum, excluded.write_maximum),
			sample_count = application_metric_minutes.sample_count + 1`
	bucketAt := collectedAt.UTC().Truncate(time.Minute).Unix()
	for _, application := range applications {
		if application.ID == "" {
			continue
		}
		if _, err := transaction.ExecContext(ctx, statement,
			application.ID, bucketAt,
			application.CPUPercent, application.CPUPercent,
			application.MemoryBytes, application.MemoryBytes,
			application.ReadBytesPerSecond, application.ReadBytesPerSecond,
			application.WriteBytesPerSecond, application.WriteBytesPerSecond,
		); err != nil {
			return fmt.Errorf("persist application metric sample: %w", err)
		}
	}
	return transaction.Commit()
}

func (m *Monitor) cleanupMetricSamples(ctx context.Context) error {
	cutoff := m.options.Now().UTC().Add(-metricRetention).Truncate(time.Minute).Unix()
	_, err := m.db.ExecContext(ctx, "DELETE FROM application_metric_minutes WHERE bucket_at < ?", cutoff)
	return err
}
