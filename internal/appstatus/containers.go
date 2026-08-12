package appstatus

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"
)

type ContainerState string

const (
	ContainerRunning    ContainerState = "running"
	ContainerRestarting ContainerState = "restarting"
	ContainerPaused     ContainerState = "paused"
	ContainerStopped    ContainerState = "stopped"
	ContainerMissing    ContainerState = "missing"
)

type ContainerAction string

const (
	ContainerStart   ContainerAction = "start"
	ContainerStop    ContainerAction = "stop"
	ContainerRestart ContainerAction = "restart"
)

type Container struct {
	ApplicationID       string         `json:"applicationId"`
	ID                  string         `json:"id"`
	Name                string         `json:"name"`
	NameKey             string         `json:"nameKey"`
	Image               string         `json:"image"`
	ComposeProject      string         `json:"composeProject,omitempty"`
	ComposeService      string         `json:"composeService,omitempty"`
	State               ContainerState `json:"state"`
	Status              string         `json:"status,omitempty"`
	Health              string         `json:"health,omitempty"`
	Attention           bool           `json:"attention"`
	Pinned              bool           `json:"pinned"`
	CanMoveUp           bool           `json:"canMoveUp"`
	CanMoveDown         bool           `json:"canMoveDown"`
	RateAvailable       bool           `json:"rateAvailable"`
	CPUPercent          float64        `json:"cpuPercent"`
	MemoryPercent       float64        `json:"memoryPercent"`
	MemoryBytes         uint64         `json:"memoryBytes"`
	MemoryLimitBytes    uint64         `json:"memoryLimitBytes"`
	ReadBytesPerSecond  float64        `json:"readBytesPerSecond"`
	WriteBytesPerSecond float64        `json:"writeBytesPerSecond"`
	ProcessCount        int            `json:"processCount"`
	PublishedPorts      []string       `json:"publishedPorts"`
}

type ContainerQuery struct {
	Search, Status, Sort, Direction string
	Limit                           int
}

type ContainerView struct {
	Containers      []Container       `json:"containers"`
	Pinned          []Container       `json:"pinned"`
	CollectedAt     time.Time         `json:"collectedAt"`
	DockerAvailable bool              `json:"dockerAvailable"`
	Total           int               `json:"total"`
	Running         int               `json:"running"`
	Stopped         int               `json:"stopped"`
	Attention       int               `json:"attention"`
	Matched         int               `json:"matched"`
	Truncated       bool              `json:"truncated"`
	Errors          map[string]string `json:"errors,omitempty"`
}

type ContainerVersion struct {
	ObservedAt  time.Time `json:"observedAt"`
	Image       string    `json:"image"`
	ContainerID string    `json:"containerId"`
}

type ContainerDetails struct {
	ApplicationDetails
	Versions []ContainerVersion `json:"versions"`
}

type ContainerOperator interface {
	OperateContainer(context.Context, string, ContainerAction) error
}

func (m *Monitor) ContainerView(ctx context.Context, query ContainerQuery) (ContainerView, error) {
	m.mu.RLock()
	raw := m.current
	applications := append([]Application(nil), m.apps...)
	m.mu.RUnlock()

	pinnedApplications, err := m.loadPins(ctx, applications)
	if err != nil {
		return ContainerView{}, err
	}
	applicationByIdentity := make(map[string]Application)
	for _, application := range applications {
		if application.Kind == KindDocker {
			applicationByIdentity[application.Identity] = application
		}
	}
	containers := make([]Container, 0, len(raw.Containers))
	byIdentity := make(map[string]Container, len(raw.Containers))
	for _, current := range raw.Containers {
		identity := normalizeContainerName(current.Name)
		application := applicationByIdentity[identity]
		container := containerFromSnapshot(current, application)
		containers = append(containers, container)
		byIdentity[identity] = container
	}

	pinned := make([]Container, 0, len(pinnedApplications))
	for _, application := range pinnedApplications {
		if application.Kind != KindDocker {
			continue
		}
		container, ok := byIdentity[application.Identity]
		if !ok {
			container = containerFromPinnedApplication(application)
		}
		container.Pinned = true
		container.CanMoveUp = application.CanMoveUp
		container.CanMoveDown = application.CanMoveDown
		pinned = append(pinned, container)
	}
	pinnedByName := make(map[string]Container, len(pinned))
	for _, container := range pinned {
		pinnedByName[container.NameKey] = container
	}
	for position := range containers {
		if value, ok := pinnedByName[containers[position].NameKey]; ok {
			containers[position].Pinned = true
			containers[position].CanMoveUp = value.CanMoveUp
			containers[position].CanMoveDown = value.CanMoveDown
		}
	}

	view := ContainerView{
		CollectedAt: raw.CollectedAt, DockerAvailable: raw.DockerAvailable,
		Errors: cloneErrors(raw.Errors), Total: len(containers), Pinned: pinned,
	}
	for _, container := range containers {
		if containerStateRunning(string(container.State)) {
			view.Running++
		} else {
			view.Stopped++
		}
		if container.Attention {
			view.Attention++
		}
	}

	search := strings.ToLower(strings.TrimSpace(query.Search))
	filtered := containers[:0]
	for _, container := range containers {
		if !containerMatchesStatus(container, query.Status) {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(strings.Join([]string{container.Name, container.Image, container.ComposeProject, container.ComposeService}, "\x00")), search) {
			continue
		}
		filtered = append(filtered, container)
	}
	view.Matched = len(filtered)
	sortContainers(filtered, query.Sort, query.Direction)
	limit := query.Limit
	if limit <= 0 {
		limit = 100
	}
	if len(filtered) > limit {
		view.Truncated = true
		filtered = filtered[:limit]
	}
	view.Containers = filtered
	return view, nil
}

func containerFromSnapshot(raw RawContainer, application Application) Container {
	state := normalizeContainerState(raw.State)
	health := strings.ToLower(strings.TrimSpace(raw.Health))
	attention := state == ContainerRestarting || strings.EqualFold(raw.State, "dead") || health == "unhealthy"
	name := strings.TrimPrefix(strings.TrimSpace(raw.Name), "/")
	return Container{
		ApplicationID: application.ID, ID: raw.ID, Name: name, NameKey: normalizeContainerName(name), Image: raw.Image,
		ComposeProject: raw.ComposeProject, ComposeService: raw.ComposeService,
		State: state, Status: raw.Status, Health: raw.Health, Attention: attention, Pinned: application.Pinned,
		RateAvailable: application.RateAvailable, CPUPercent: application.CPUPercent,
		MemoryPercent: application.MemoryPercent, MemoryBytes: application.MemoryBytes, MemoryLimitBytes: application.MemoryLimitBytes,
		ReadBytesPerSecond: application.ReadBytesPerSecond, WriteBytesPerSecond: application.WriteBytesPerSecond,
		ProcessCount: application.ProcessCount, PublishedPorts: append([]string(nil), raw.PublishedPorts...),
	}
}

func containerFromPinnedApplication(application Application) Container {
	return Container{
		ApplicationID: application.ID, Name: application.Name, NameKey: application.Identity, Image: application.Technical,
		State: ContainerMissing, Status: "Not present in the current Docker snapshot", Attention: true, Pinned: true,
	}
}

func normalizeContainerState(value string) ContainerState {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "running":
		return ContainerRunning
	case "restarting":
		return ContainerRestarting
	case "paused":
		return ContainerPaused
	case "", "created", "exited", "dead", "removing":
		return ContainerStopped
	default:
		return ContainerStopped
	}
}

func containerStateRunning(value string) bool {
	switch normalizeContainerState(value) {
	case ContainerRunning, ContainerRestarting, ContainerPaused:
		return true
	default:
		return false
	}
}

func containerMatchesStatus(container Container, status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "all":
		return true
	case "running":
		return containerStateRunning(string(container.State))
	case "stopped":
		return !containerStateRunning(string(container.State))
	case "attention":
		return container.Attention
	default:
		return false
	}
}

func sortContainers(containers []Container, field, direction string) {
	if field == "" {
		field = "state"
	}
	descending := strings.EqualFold(direction, "desc")
	sort.SliceStable(containers, func(leftIndex, rightIndex int) bool {
		left, right := containers[leftIndex], containers[rightIndex]
		comparison := 0
		switch field {
		case "name":
			comparison = strings.Compare(left.NameKey, right.NameKey)
		case "cpu":
			comparison = compareOrdered(left.CPUPercent, right.CPUPercent)
		case "memory":
			comparison = compareOrdered(left.MemoryBytes, right.MemoryBytes)
		case "read":
			comparison = compareOrdered(left.ReadBytesPerSecond, right.ReadBytesPerSecond)
		case "write":
			comparison = compareOrdered(left.WriteBytesPerSecond, right.WriteBytesPerSecond)
		case "ports":
			comparison = compareOrdered(len(left.PublishedPorts), len(right.PublishedPorts))
		default:
			comparison = compareOrdered(containerStateRank(left.State), containerStateRank(right.State))
		}
		if comparison == 0 {
			comparison = strings.Compare(left.NameKey, right.NameKey)
		}
		if descending {
			return comparison > 0
		}
		return comparison < 0
	})
}

func containerStateRank(state ContainerState) int {
	switch state {
	case ContainerRunning:
		return 0
	case ContainerRestarting:
		return 1
	case ContainerPaused:
		return 2
	case ContainerStopped:
		return 3
	default:
		return 4
	}
}

func (m *Monitor) PinContainer(ctx context.Context, name string) error {
	identity := normalizeContainerName(name)
	if identity == "" {
		return errors.New("container name is required")
	}
	id := stableID(KindDocker, identity)
	if err := m.Pin(ctx, id); err != nil {
		return err
	}
	if current, collectedAt, ok := m.currentContainer(identity); ok {
		return m.recordContainerVersion(ctx, id, collectedAt, current)
	}
	return nil
}

func (m *Monitor) UnpinContainer(ctx context.Context, name string) error {
	identity := normalizeContainerName(name)
	if identity == "" {
		return errors.New("container name is required")
	}
	return m.Unpin(ctx, stableID(KindDocker, identity))
}

func (m *Monitor) MovePinnedContainer(ctx context.Context, name, direction string) error {
	identity := normalizeContainerName(name)
	if identity == "" {
		return errors.New("container name is required")
	}
	return m.MovePin(ctx, stableID(KindDocker, identity), direction)
}

func (m *Monitor) ContainerDetails(ctx context.Context, name, selectedRange string) (ContainerDetails, error) {
	identity := normalizeContainerName(name)
	if identity == "" {
		return ContainerDetails{}, ErrApplicationNotFound
	}
	applicationID := stableID(KindDocker, identity)
	details, err := m.Details(ctx, applicationID, selectedRange)
	if err != nil {
		return ContainerDetails{}, err
	}
	versions, err := m.containerVersions(ctx, applicationID)
	if err != nil {
		return ContainerDetails{}, err
	}
	return ContainerDetails{ApplicationDetails: details, Versions: versions}, nil
}

func (m *Monitor) OperateContainer(ctx context.Context, name string, action ContainerAction) error {
	identity := normalizeContainerName(name)
	if identity == "" {
		return errors.New("container name is required")
	}
	switch action {
	case ContainerStart, ContainerStop, ContainerRestart:
	default:
		return errors.New("unsupported container action")
	}
	if _, _, ok := m.currentContainer(identity); !ok {
		return errors.New("container is not in the current Docker snapshot")
	}
	operator, ok := m.probe.(ContainerOperator)
	if !ok {
		return errors.New("container operations are unavailable on this probe")
	}
	m.refreshMu.Lock()
	err := operator.OperateContainer(ctx, identity, action)
	m.refreshMu.Unlock()
	if err != nil {
		return err
	}
	return m.Refresh(ctx)
}

func (m *Monitor) currentContainer(identity string) (RawContainer, time.Time, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, current := range m.current.Containers {
		if normalizeContainerName(current.Name) == identity {
			return current, m.current.CollectedAt, true
		}
	}
	return RawContainer{}, m.current.CollectedAt, false
}

func (m *Monitor) persistContainerVersions(ctx context.Context, collectedAt time.Time, applications []Application, containers []RawContainer) error {
	rows, err := m.db.QueryContext(ctx, `SELECT id FROM application_pins WHERE kind=?`, KindDocker)
	if err != nil {
		return err
	}
	pinned := make(map[string]struct{})
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		pinned[id] = struct{}{}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	applicationByIdentity := make(map[string]Application)
	for _, application := range applications {
		if application.Kind == KindDocker {
			applicationByIdentity[application.Identity] = application
		}
	}
	for _, container := range containers {
		application := applicationByIdentity[normalizeContainerName(container.Name)]
		if _, ok := pinned[application.ID]; !ok {
			continue
		}
		if err := m.recordContainerVersion(ctx, application.ID, collectedAt, container); err != nil {
			return err
		}
	}
	return nil
}

func (m *Monitor) recordContainerVersion(ctx context.Context, applicationID string, observedAt time.Time, container RawContainer) error {
	if applicationID == "" || strings.TrimSpace(container.Image) == "" {
		return nil
	}
	transaction, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	var latestImage, latestContainerID string
	var latestAt int64
	err = transaction.QueryRowContext(ctx, `SELECT observed_at,image,container_id FROM application_versions WHERE application_id=? ORDER BY observed_at DESC LIMIT 1`, applicationID).Scan(&latestAt, &latestImage, &latestContainerID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && latestImage == container.Image && latestContainerID == container.ID {
		return nil
	}
	timestamp := observedAt.UTC().UnixNano()
	if timestamp <= latestAt {
		timestamp = latestAt + 1
	}
	if _, err := transaction.ExecContext(ctx, `INSERT INTO application_versions (application_id,observed_at,image,container_id) VALUES (?,?,?,?)`, applicationID, timestamp, container.Image, container.ID); err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, `DELETE FROM application_versions WHERE application_id=? AND observed_at NOT IN (SELECT observed_at FROM application_versions WHERE application_id=? ORDER BY observed_at DESC LIMIT 100)`, applicationID, applicationID); err != nil {
		return err
	}
	return transaction.Commit()
}

func (m *Monitor) containerVersions(ctx context.Context, applicationID string) ([]ContainerVersion, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT observed_at,image,container_id FROM application_versions WHERE application_id=? ORDER BY observed_at DESC LIMIT 100`, applicationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	versions := make([]ContainerVersion, 0)
	for rows.Next() {
		var timestamp int64
		var version ContainerVersion
		if err := rows.Scan(&timestamp, &version.Image, &version.ContainerID); err != nil {
			return nil, err
		}
		version.ObservedAt = time.Unix(0, timestamp).UTC()
		versions = append(versions, version)
	}
	return versions, rows.Err()
}
