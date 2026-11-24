package clusterstatus

import (
	"context"
	"time"
)

type Mode string

const (
	ModeObserve Mode = "observe"
	ModeLimited Mode = "limited"
)

type Connection struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Note              string `json:"note,omitempty"`
	KubeconfigPath    string `json:"kubeconfigPath"`
	Context           string `json:"context"`
	PhysicalHostLabel string `json:"physicalHostLabel,omitempty"`
	Mode              Mode   `json:"mode"`
}

type Capabilities struct {
	Workloads bool `json:"workloads"`
	Nodes     bool `json:"nodes"`
	Metrics   bool `json:"metrics"`
	Logs      bool `json:"logs"`
	Redeploy  bool `json:"redeploy"`
	Scale     bool `json:"scale"`
	RunCron   bool `json:"runCron"`
}

type ConnectionStatus struct {
	Connection
	Connected    bool         `json:"connected"`
	Fingerprint  string       `json:"fingerprint"`
	Capabilities Capabilities `json:"capabilities"`
	TestedAt     time.Time    `json:"testedAt"`
	Error        string       `json:"error,omitempty"`
}

type Query struct {
	Search, Status, Namespace, Kind, Sort, Direction string
	ConnectionID                                     string
	Limit                                            int
	PhysicalHost, Section, View, TopologyMode        string
	IncludeTopology                                  bool
}

type View struct {
	Connection                   ConnectionStatus
	CollectedAt                  time.Time
	ServerVersion                string
	Nodes                        []Node
	Workloads                    []Workload
	Services                     []ServiceExposure
	Ingresses                    []IngressExposure
	Total, Matched               int
	Ready, Progressing, Degraded int
	PodsReady, PodsTotal         int
	Namespaces                   int
	MetricsAvailable             bool
	Errors                       map[string]string
	AvailableNamespaces          []string
	PhysicalHosts                []string
	UnmappedHosts                bool
	NodesTotal                   int
	NetworkServices              []ServiceExposure `json:"-"`
	Topology                     *Topology         `json:",omitempty"`
}

type Node struct {
	Name, Role, Version           string
	PhysicalHost                  string
	CPUAvailable, MemoryAvailable bool
	Ready                         bool
	CPUPercent                    float64
	MemoryPercent                 float64
}

type Workload struct {
	LastStartedAt                                          time.Time
	Key, Namespace, Kind, Name, Image, Status, StatusLabel string
	Ready, Desired, Restarts                               int
	CPUMillicores                                          int64
	MemoryBytes                                            uint64
	Nodes, Age, Revision                                   string
}

// ServiceExposure describes an externally-addressable Service declaration.
// It intentionally reports configuration rather than claiming network reachability.
type ServiceExposure struct {
	Namespace, Name, Type, ExternalName, ExternalTrafficPolicy string
	ClusterIPs, ExternalAddresses                              []string
	Ports                                                      []ServicePort
}

type ServicePort struct {
	Name, Protocol, AppProtocol, TargetPort string
	Port, NodePort                          int
}

type IngressExposure struct {
	Namespace, Name, Class string
	Addresses              []string
	Rules                  []IngressRule
}

type IngressRule struct {
	Host, Path, PathType, Service, ServicePort string
	TLS                                        bool
}

type Pod struct {
	Name, Namespace, Node, Ready, Phase string
	Restarts                            int
	Containers                          []string
}

type TopologyPod struct {
	Pod
	UID, WorkloadKey, ControllerKind, ControllerName string
	Status, Reason                                   string
}

type ServiceEndpoint struct {
	PodUID string
	Ready  *bool
}

type TopologyService struct {
	ServiceExposure
	Endpoints      []ServiceEndpoint
	OtherEndpoints int
}

type Topology struct {
	Workloads []Workload
	Pods      []TopologyPod
	Services  []TopologyService
	Ingresses []IngressExposure
	Nodes     []Node
}

type Event struct {
	At                    time.Time
	Type, Reason, Message string
}

type Version struct {
	ObservedAt time.Time
	Image      string
	Revision   string
}

type MetricSample struct {
	At                       time.Time
	CPUMillicores            int64
	MemoryBytes              uint64
	Ready, Desired, Restarts int
}

type Snapshot struct {
	TopologyPods         []TopologyPod
	TopologyServices     []TopologyService
	CollectedAt          time.Time
	ServerVersion        string
	Nodes                []Node
	Workloads            []Workload
	Services             []ServiceExposure
	Ingresses            []IngressExposure
	PodsReady, PodsTotal int
	Namespaces           int
	MetricsAvailable     bool
	Errors               map[string]string
}

type Detail struct {
	Workload Workload
	Pods     []Pod
	Events   []Event
	Versions []Version
	Metrics  []MetricSample
}

type LogLine struct {
	At        time.Time `json:"at"`
	Pod       string    `json:"pod"`
	Container string    `json:"container"`
	Text      string    `json:"text"`
}

type OperationKind string

const (
	OperationRedeploy OperationKind = "redeploy"
	OperationScale    OperationKind = "scale"
	OperationRunCron  OperationKind = "run_cron"
)

type Operation struct {
	Kind        OperationKind
	WorkloadKey string
	Replicas    int
}

type Client interface {
	Close() error
	Capabilities(context.Context) (Capabilities, error)
	Fingerprint() string
	Snapshot(context.Context) (Snapshot, error)
	Detail(context.Context, string) (Detail, error)
	Logs(context.Context, string, int) ([]LogLine, error)
	Operate(context.Context, Operation) error
}

// WorkloadPodClient reads one Pod after verifying its workload membership.
type WorkloadPodClient interface {
	PodDetail(context.Context, string, string) (Detail, error)
	PodLogs(context.Context, string, string, int) ([]LogLine, error)
}

type Factory interface {
	Open(context.Context, Connection) (Client, error)
}
