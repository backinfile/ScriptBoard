package clusterstatus

import (
	"fmt"
	"sort"
	"strings"
)

type kubeEndpointSlice struct {
	Metadata  kubeMetadata `json:"metadata"`
	Endpoints []struct {
		Addresses  []string
		TargetRef  *struct{ UID, Kind, Namespace, Name string }
		Conditions struct{ Ready *bool }
	}
}

func summarizeTopologyPod(pod kubePod) TopologyPod {
	summary := TopologyPod{
		Pod: Pod{Name: pod.Metadata.Name, Namespace: pod.Metadata.Namespace, Node: pod.Spec.NodeName, Phase: pod.Status.Phase},
		UID: pod.Metadata.UID, Reason: pod.Status.Reason, Status: "progressing",
	}
	ready := 0
	for _, container := range pod.Spec.Containers {
		summary.Containers = append(summary.Containers, container.Name)
	}
	for _, container := range pod.Status.ContainerStatuses {
		if container.Ready {
			ready++
		}
		summary.Restarts += container.RestartCount
		if waiting := container.State.Waiting; waiting != nil && waiting.Reason != "" {
			switch waiting.Reason {
			case "ContainerCreating", "PodInitializing":
				if summary.Reason == "" {
					summary.Reason = waiting.Reason
				}
			default:
				summary.Reason = waiting.Reason
				summary.Status = "degraded"
			}
		}
	}
	summary.Ready = fmt.Sprintf("%d/%d", ready, len(summary.Containers))
	switch {
	case pod.Status.Phase == "Failed":
		summary.Status = "degraded"
	case podReady(pod) || pod.Status.Phase == "Succeeded":
		summary.Status = "ready"
	}
	return summary
}

func collectTopologyServices(resources []kubeService, slices []kubeEndpointSlice, pods []TopologyPod) []TopologyService {
	services := serviceExposures(resources, false)
	result := make([]TopologyService, len(services))
	// Display sorting changes the order of Services, so validate each
	// EndpointSlice owner against the original Service identity by key.
	serviceUIDs := make(map[string]string, len(resources))
	for _, resource := range resources {
		serviceUIDs[resource.Metadata.Namespace+"/"+resource.Metadata.Name] = resource.Metadata.UID
	}
	serviceIndex := make(map[string]int, len(services))
	for index, service := range services {
		result[index].ServiceExposure = service
		serviceIndex[service.Namespace+"/"+service.Name] = index
	}
	podsByUID := make(map[string]TopologyPod, len(pods))
	for _, pod := range pods {
		if pod.UID != "" {
			podsByUID[pod.UID] = pod
		}
	}
	targets := make(map[int]map[string]ServiceEndpoint)
	other := make(map[int]map[string]struct{})
	for _, slice := range slices {
		key := slice.Metadata.Namespace + "/" + slice.Metadata.Labels["kubernetes.io/service-name"]
		index, ok := serviceIndex[key]
		if !ok {
			continue
		}
		ownerMatches := true
		for _, owner := range slice.Metadata.OwnerReferences {
			if owner.Kind == "Service" && owner.UID != serviceUIDs[key] {
				ownerMatches = false
				break
			}
		}
		if !ownerMatches {
			continue
		}
		if targets[index] == nil {
			targets[index] = make(map[string]ServiceEndpoint)
			other[index] = make(map[string]struct{})
		}
		for _, endpoint := range slice.Endpoints {
			ref := endpoint.TargetRef
			if ref == nil || ref.Kind != "Pod" {
				for _, address := range endpoint.Addresses {
					other[index][address] = struct{}{}
				}
				continue
			}
			pod, found := podsByUID[ref.UID]
			if found && pod.Name == ref.Name && pod.Namespace == slice.Metadata.Namespace && (ref.Namespace == "" || ref.Namespace == pod.Namespace) {
				existing, has := targets[index][pod.UID]
				// Multiple slices can briefly carry the same endpoint. Keep an
				// available endpoint if any slice marks it as ready.
				if !has || existing.Ready != nil && !*existing.Ready {
					targets[index][pod.UID] = ServiceEndpoint{PodUID: pod.UID, Ready: endpoint.Conditions.Ready}
				}
			}
		}
	}
	for index, serviceTargets := range targets {
		for _, endpoint := range serviceTargets {
			result[index].Endpoints = append(result[index].Endpoints, endpoint)
		}
		result[index].OtherEndpoints = len(other[index])
		sort.Slice(result[index].Endpoints, func(i, j int) bool { return result[index].Endpoints[i].PodUID < result[index].Endpoints[j].PodUID })
	}
	return result
}

const UnmappedPhysicalHost = "unmapped"

func physicalHostKey(node Node) string {
	if node.PhysicalHost == "" {
		return UnmappedPhysicalHost
	}
	return "host:" + node.PhysicalHost
}

func matchesNamespace(namespace, scope string) bool {
	return scope == "" || scope == "all" || scope == namespace
}

func matchesText(search string, fields ...string) bool {
	return search == "" || strings.Contains(strings.ToLower(strings.Join(fields, "\x00")), search)
}

func scopeTopology(snapshot Snapshot, query Query) Topology {
	topology := Topology{}
	nodesByName := make(map[string]Node, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		nodesByName[node.Name] = node
	}
	podUIDs := make(map[string]struct{})
	workloadNodes := make(map[string]map[string]struct{})
	nodesUsed := make(map[string]struct{})
	for _, pod := range snapshot.TopologyPods {
		if !matchesNamespace(pod.Namespace, query.Namespace) {
			continue
		}
		node, assigned := nodesByName[pod.Node]
		if query.PhysicalHost != "" && (!assigned || physicalHostKey(node) != query.PhysicalHost) {
			continue
		}
		topology.Pods = append(topology.Pods, pod)
		podUIDs[pod.UID] = struct{}{}
		if assigned {
			nodesUsed[node.Name] = struct{}{}
		}
		if pod.WorkloadKey != "" {
			if workloadNodes[pod.WorkloadKey] == nil {
				workloadNodes[pod.WorkloadKey] = make(map[string]struct{})
			}
			if pod.Node != "" {
				workloadNodes[pod.WorkloadKey][pod.Node] = struct{}{}
			}
		}
	}
	for _, node := range snapshot.Nodes {
		if query.PhysicalHost != "" {
			if physicalHostKey(node) != query.PhysicalHost {
				continue
			}
		} else if !matchesNamespace("", query.Namespace) {
			if _, used := nodesUsed[node.Name]; !used {
				continue
			}
		}
		topology.Nodes = append(topology.Nodes, node)
	}
	for _, workload := range snapshot.Workloads {
		if !matchesNamespace(workload.Namespace, query.Namespace) {
			continue
		}
		if query.PhysicalHost != "" {
			names, present := workloadNodes[workload.Key]
			if !present {
				continue
			}
			workload.Nodes = formatNodeNames(names)
		}
		topology.Workloads = append(topology.Workloads, workload)
	}
	services := snapshot.TopologyServices
	if len(services) == 0 && len(snapshot.Services) > 0 {
		for _, service := range snapshot.Services {
			services = append(services, TopologyService{ServiceExposure: service})
		}
	}
	servicesInScope := make(map[string]struct{})
	for _, service := range services {
		if !matchesNamespace(service.Namespace, query.Namespace) {
			continue
		}
		if query.PhysicalHost != "" {
			serviceEndpoints := make([]ServiceEndpoint, 0, len(service.Endpoints))
			for _, endpoint := range service.Endpoints {
				if _, present := podUIDs[endpoint.PodUID]; present {
					serviceEndpoints = append(serviceEndpoints, endpoint)
				}
			}
			if len(serviceEndpoints) == 0 {
				continue
			}
			service.Endpoints = serviceEndpoints
			service.OtherEndpoints = 0
		}
		topology.Services = append(topology.Services, service)
		servicesInScope[service.Namespace+"/"+service.Name] = struct{}{}
	}
	for _, ingress := range snapshot.Ingresses {
		if !matchesNamespace(ingress.Namespace, query.Namespace) {
			continue
		}
		if query.PhysicalHost != "" {
			var rules []IngressRule
			for _, rule := range ingress.Rules {
				if _, present := servicesInScope[ingress.Namespace+"/"+rule.Service]; present {
					rules = append(rules, rule)
				}
			}
			if len(rules) == 0 {
				continue
			}
			ingress.Rules = rules
		}
		topology.Ingresses = append(topology.Ingresses, ingress)
	}
	return topology
}
