package clusterstatus

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

type kubeList[T any] struct {
	Items []T `json:"items"`
}

type kubeOwnerReference struct {
	UID  string `json:"uid"`
	Kind string `json:"kind"`
	Name string `json:"name"`
}

type kubeMetadata struct {
	Name              string               `json:"name"`
	Namespace         string               `json:"namespace"`
	UID               string               `json:"uid"`
	ResourceVersion   string               `json:"resourceVersion"`
	CreationTimestamp string               `json:"creationTimestamp"`
	Labels            map[string]string    `json:"labels"`
	Annotations       map[string]string    `json:"annotations"`
	OwnerReferences   []kubeOwnerReference `json:"ownerReferences"`
}

type kubeContainer struct {
	Name  string `json:"name"`
	Image string `json:"image"`
}

type kubePodTemplate struct {
	Metadata struct {
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
	Spec struct {
		Containers []kubeContainer `json:"containers"`
	} `json:"spec"`
}

type kubeDeployment struct {
	Metadata kubeMetadata `json:"metadata"`
	Spec     struct {
		Replicas *int            `json:"replicas"`
		Template kubePodTemplate `json:"template"`
	} `json:"spec"`
	Status struct {
		ReadyReplicas       int `json:"readyReplicas"`
		UpdatedReplicas     int `json:"updatedReplicas"`
		UnavailableReplicas int `json:"unavailableReplicas"`
	} `json:"status"`
}

type kubeStatefulSet struct {
	Metadata kubeMetadata `json:"metadata"`
	Spec     struct {
		Replicas *int            `json:"replicas"`
		Template kubePodTemplate `json:"template"`
	} `json:"spec"`
	Status struct {
		ReadyReplicas   int    `json:"readyReplicas"`
		CurrentRevision string `json:"currentRevision"`
		UpdateRevision  string `json:"updateRevision"`
	} `json:"status"`
}

type kubeDaemonSet struct {
	Metadata kubeMetadata `json:"metadata"`
	Spec     struct {
		Template kubePodTemplate `json:"template"`
	} `json:"spec"`
	Status struct {
		DesiredNumberScheduled int `json:"desiredNumberScheduled"`
		NumberReady            int `json:"numberReady"`
	} `json:"status"`
}

type kubeCronJob struct {
	Metadata kubeMetadata `json:"metadata"`
	Spec     struct {
		Schedule    string `json:"schedule"`
		Suspend     *bool  `json:"suspend"`
		JobTemplate struct {
			Spec struct {
				Template kubePodTemplate `json:"template"`
			} `json:"spec"`
		} `json:"jobTemplate"`
	} `json:"spec"`
	Status struct {
		Active []map[string]any `json:"active"`
	} `json:"status"`
}

type kubeReplicaSet struct {
	Metadata kubeMetadata `json:"metadata"`
}

type kubeJob struct {
	Metadata kubeMetadata `json:"metadata"`
	Status   struct {
		Active    int `json:"active"`
		Succeeded int `json:"succeeded"`
		Failed    int `json:"failed"`
	} `json:"status"`
}

type kubePod struct {
	Metadata kubeMetadata `json:"metadata"`
	Spec     struct {
		NodeName   string          `json:"nodeName"`
		Containers []kubeContainer `json:"containers"`
	} `json:"spec"`
	Status struct {
		Phase             string `json:"phase"`
		ContainerStatuses []struct {
			Name         string `json:"name"`
			Ready        bool   `json:"ready"`
			RestartCount int    `json:"restartCount"`
		} `json:"containerStatuses"`
	} `json:"status"`
}

type kubeNode struct {
	Metadata kubeMetadata `json:"metadata"`
	Status   struct {
		Conditions []struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"conditions"`
		Capacity map[string]string `json:"capacity"`
		NodeInfo struct {
			KubeletVersion string `json:"kubeletVersion"`
		} `json:"nodeInfo"`
	} `json:"status"`
}

type kubePodMetric struct {
	Metadata   kubeMetadata `json:"metadata"`
	Containers []struct {
		Name  string            `json:"name"`
		Usage map[string]string `json:"usage"`
	} `json:"containers"`
}

type kubeNodeMetric struct {
	Metadata kubeMetadata      `json:"metadata"`
	Usage    map[string]string `json:"usage"`
}

func (client *kubeHTTPClient) Snapshot(ctx context.Context) (Snapshot, error) {
	now := time.Now().UTC()
	snapshot := Snapshot{CollectedAt: now, Errors: make(map[string]string)}
	var version struct {
		GitVersion string `json:"gitVersion"`
	}
	if err := client.request(ctx, http.MethodGet, "/version", "", nil, &version); err != nil {
		return Snapshot{}, err
	}
	snapshot.ServerVersion = version.GitVersion

	var deployments kubeList[kubeDeployment]
	var statefulSets kubeList[kubeStatefulSet]
	var daemonSets kubeList[kubeDaemonSet]
	var cronJobs kubeList[kubeCronJob]
	var replicaSets kubeList[kubeReplicaSet]
	var jobs kubeList[kubeJob]
	var pods kubeList[kubePod]
	var nodes kubeList[kubeNode]
	var namespaces kubeList[struct {
		Metadata kubeMetadata `json:"metadata"`
	}]
	requests := []struct {
		name, path string
		output     any
		required   bool
	}{
		{"deployments", "/apis/apps/v1/deployments", &deployments, false},
		{"statefulsets", "/apis/apps/v1/statefulsets", &statefulSets, false},
		{"daemonsets", "/apis/apps/v1/daemonsets", &daemonSets, false},
		{"cronjobs", "/apis/batch/v1/cronjobs", &cronJobs, false},
		{"replicasets", "/apis/apps/v1/replicasets", &replicaSets, false},
		{"jobs", "/apis/batch/v1/jobs", &jobs, false},
		{"pods", "/api/v1/pods", &pods, false},
		{"nodes", "/api/v1/nodes", &nodes, false},
		{"namespaces", "/api/v1/namespaces", &namespaces, false},
	}
	workloadSources := 0
	for _, source := range requests {
		if err := client.request(ctx, http.MethodGet, source.path, "", nil, source.output); err != nil {
			snapshot.Errors[source.name] = err.Error()
			if source.required {
				return Snapshot{}, err
			}
			continue
		}
		if source.name == "deployments" || source.name == "statefulsets" || source.name == "daemonsets" || source.name == "cronjobs" {
			workloadSources++
		}
	}
	if workloadSources == 0 {
		return Snapshot{}, fmt.Errorf("Kubernetes credentials cannot list supported workloads")
	}
	snapshot.Namespaces = len(namespaces.Items)

	workloads := make(map[string]*Workload)
	ownerToWorkload := make(map[string]string)
	addWorkload := func(metadata kubeMetadata, kind, image, revision string, ready, desired int) {
		key := workloadKey(metadata.Namespace, kind, metadata.Name)
		status, label := workloadStatus(ready, desired)
		workloads[key] = &Workload{Key: key, Namespace: metadata.Namespace, Kind: kind, Name: metadata.Name, Image: image,
			Status: status, StatusLabel: label, Ready: ready, Desired: desired, Revision: revision, Age: workloadAge(metadata.CreationTimestamp, now)}
		ownerToWorkload[metadata.UID] = key
	}
	for _, item := range deployments.Items {
		addWorkload(item.Metadata, "Deployment", containerImages(item.Spec.Template.Spec.Containers), deploymentRevision(item.Metadata), item.Status.ReadyReplicas, replicasOrDefault(item.Spec.Replicas))
	}
	for _, item := range statefulSets.Items {
		revision := item.Status.UpdateRevision
		if revision == "" {
			revision = item.Status.CurrentRevision
		}
		addWorkload(item.Metadata, "StatefulSet", containerImages(item.Spec.Template.Spec.Containers), revision, item.Status.ReadyReplicas, replicasOrDefault(item.Spec.Replicas))
	}
	for _, item := range daemonSets.Items {
		addWorkload(item.Metadata, "DaemonSet", containerImages(item.Spec.Template.Spec.Containers), item.Metadata.ResourceVersion, item.Status.NumberReady, item.Status.DesiredNumberScheduled)
	}
	for _, item := range cronJobs.Items {
		ready := 1
		if item.Spec.Suspend != nil && *item.Spec.Suspend {
			ready = 0
		}
		addWorkload(item.Metadata, "CronJob", containerImages(item.Spec.JobTemplate.Spec.Template.Spec.Containers), "schedule "+item.Spec.Schedule, ready, 1)
	}
	for _, item := range replicaSets.Items {
		if owner := firstControllerOwner(item.Metadata.OwnerReferences, "Deployment"); owner != nil {
			if key := ownerToWorkload[owner.UID]; key != "" {
				ownerToWorkload[item.Metadata.UID] = key
			}
		}
	}
	for _, item := range jobs.Items {
		if owner := firstControllerOwner(item.Metadata.OwnerReferences, "CronJob"); owner != nil {
			if key := ownerToWorkload[owner.UID]; key != "" {
				ownerToWorkload[item.Metadata.UID] = key
			}
		}
	}
	podToWorkload := make(map[string]string)
	workloadNodes := make(map[string]map[string]struct{})
	for _, pod := range pods.Items {
		snapshot.PodsTotal++
		if podReady(pod) {
			snapshot.PodsReady++
		}
		owner := firstControllerOwner(pod.Metadata.OwnerReferences, "")
		if owner == nil {
			continue
		}
		key := ownerToWorkload[owner.UID]
		workload := workloads[key]
		if workload == nil {
			continue
		}
		podToWorkload[pod.Metadata.Namespace+"/"+pod.Metadata.Name] = key
		for _, status := range pod.Status.ContainerStatuses {
			workload.Restarts += status.RestartCount
		}
		if pod.Spec.NodeName != "" {
			if workloadNodes[key] == nil {
				workloadNodes[key] = make(map[string]struct{})
			}
			workloadNodes[key][pod.Spec.NodeName] = struct{}{}
		}
	}
	for key, names := range workloadNodes {
		workloads[key].Nodes = formatNodeNames(names)
	}

	var podMetrics kubeList[kubePodMetric]
	if err := client.request(ctx, http.MethodGet, "/apis/metrics.k8s.io/v1beta1/pods", "", nil, &podMetrics); err != nil {
		snapshot.Errors["metrics"] = err.Error()
	} else {
		snapshot.MetricsAvailable = true
		for _, metric := range podMetrics.Items {
			workload := workloads[podToWorkload[metric.Metadata.Namespace+"/"+metric.Metadata.Name]]
			if workload == nil {
				continue
			}
			for _, container := range metric.Containers {
				workload.CPUMillicores += parseCPU(container.Usage["cpu"])
				workload.MemoryBytes += parseBytes(container.Usage["memory"])
			}
		}
	}

	var nodeMetrics kubeList[kubeNodeMetric]
	_ = client.request(ctx, http.MethodGet, "/apis/metrics.k8s.io/v1beta1/nodes", "", nil, &nodeMetrics)
	nodeUsage := make(map[string]kubeNodeMetric, len(nodeMetrics.Items))
	for _, metric := range nodeMetrics.Items {
		nodeUsage[metric.Metadata.Name] = metric
	}
	for _, item := range nodes.Items {
		node := Node{Name: item.Metadata.Name, Role: nodeRole(item.Metadata.Labels), Version: item.Status.NodeInfo.KubeletVersion, Ready: nodeReady(item)}
		if usage, ok := nodeUsage[item.Metadata.Name]; ok {
			cpuCapacity := parseCPU(item.Status.Capacity["cpu"])
			memoryCapacity := parseBytes(item.Status.Capacity["memory"])
			if cpuCapacity > 0 {
				node.CPUPercent = float64(parseCPU(usage.Usage["cpu"])) / float64(cpuCapacity) * 100
			}
			if memoryCapacity > 0 {
				node.MemoryPercent = float64(parseBytes(usage.Usage["memory"])) / float64(memoryCapacity) * 100
			}
		}
		snapshot.Nodes = append(snapshot.Nodes, node)
	}
	for _, workload := range workloads {
		snapshot.Workloads = append(snapshot.Workloads, *workload)
	}
	sort.Slice(snapshot.Workloads, func(i, j int) bool { return snapshot.Workloads[i].Key < snapshot.Workloads[j].Key })
	sort.Slice(snapshot.Nodes, func(i, j int) bool { return snapshot.Nodes[i].Name < snapshot.Nodes[j].Name })
	if len(snapshot.Errors) == 0 {
		snapshot.Errors = nil
	}
	return snapshot, nil
}

func workloadKey(namespace, kind, name string) string { return namespace + "/" + kind + "/" + name }

func replicasOrDefault(value *int) int {
	if value == nil {
		return 1
	}
	return *value
}

func workloadStatus(ready, desired int) (string, string) {
	if ready >= desired && desired > 0 {
		return "ready", "Ready"
	}
	if ready > 0 {
		return "progressing", "Progressing"
	}
	return "degraded", "Degraded"
}

func deploymentRevision(metadata kubeMetadata) string {
	if value := metadata.Annotations["deployment.kubernetes.io/revision"]; value != "" {
		return "rev " + value
	}
	return metadata.ResourceVersion
}

func containerImages(containers []kubeContainer) string {
	seen := make(map[string]struct{})
	values := make([]string, 0, len(containers))
	for _, container := range containers {
		if container.Image == "" {
			continue
		}
		if _, ok := seen[container.Image]; !ok {
			seen[container.Image] = struct{}{}
			values = append(values, container.Image)
		}
	}
	return strings.Join(values, ", ")
}

func firstControllerOwner(owners []kubeOwnerReference, kind string) *kubeOwnerReference {
	for index := range owners {
		if kind == "" || owners[index].Kind == kind {
			return &owners[index]
		}
	}
	return nil
}

func podReady(pod kubePod) bool {
	if pod.Status.Phase != "Running" || len(pod.Status.ContainerStatuses) == 0 {
		return false
	}
	for _, status := range pod.Status.ContainerStatuses {
		if !status.Ready {
			return false
		}
	}
	return true
}

func formatNodeNames(names map[string]struct{}) string {
	values := make([]string, 0, len(names))
	for name := range names {
		values = append(values, name)
	}
	sort.Strings(values)
	// Keep exact node identities available to both the workload table and
	// detail drawer instead of replacing multi-node placement with a count.
	return strings.Join(values, ", ")
}

func nodeRole(labels map[string]string) string {
	if _, ok := labels["node-role.kubernetes.io/control-plane"]; ok {
		return "control-plane"
	}
	if _, ok := labels["node-role.kubernetes.io/master"]; ok {
		return "control-plane"
	}
	return "worker"
}

func nodeReady(node kubeNode) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == "Ready" {
			return condition.Status == "True"
		}
	}
	return false
}

func workloadAge(value string, now time.Time) string {
	created, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return "—"
	}
	duration := now.Sub(created)
	if duration < time.Hour {
		return fmt.Sprintf("%d 分钟", max(0, int(duration.Minutes())))
	}
	if duration < 24*time.Hour {
		return fmt.Sprintf("%d 小时", int(duration.Hours()))
	}
	return fmt.Sprintf("%d 天", int(duration.Hours()/24))
}

func parseCPU(value string) int64 {
	value = strings.TrimSpace(value)
	multiplier := 1000.0
	for suffix, factor := range map[string]float64{"n": 1e-6, "u": 1e-3, "m": 1} {
		if strings.HasSuffix(value, suffix) {
			multiplier = factor
			value = strings.TrimSuffix(value, suffix)
			break
		}
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0
	}
	return int64(math.Round(number * multiplier))
}

func parseBytes(value string) uint64 {
	value = strings.TrimSpace(value)
	multiplier := float64(1)
	for suffix, factor := range map[string]float64{"Ki": 1 << 10, "Mi": 1 << 20, "Gi": 1 << 30, "Ti": 1 << 40, "K": 1e3, "M": 1e6, "G": 1e9, "T": 1e12} {
		if strings.HasSuffix(value, suffix) {
			multiplier = factor
			value = strings.TrimSuffix(value, suffix)
			break
		}
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || number < 0 {
		return 0
	}
	return uint64(number * multiplier)
}
