package clusterstatus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type kubeEvent struct {
	Metadata      kubeMetadata `json:"metadata"`
	Type          string       `json:"type"`
	Reason        string       `json:"reason"`
	Message       string       `json:"message"`
	EventTime     string       `json:"eventTime"`
	LastTimestamp string       `json:"lastTimestamp"`
}

func parseWorkloadKey(key string) (namespace, kind, name string, err error) {
	parts := strings.Split(key, "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", errors.New("invalid Kubernetes workload key")
	}
	switch parts[1] {
	case "Deployment", "StatefulSet", "DaemonSet", "CronJob":
	default:
		return "", "", "", errors.New("unsupported Kubernetes workload kind")
	}
	return parts[0], parts[1], parts[2], nil
}

func resourceForKind(kind string) (group, plural string) {
	if kind == "CronJob" {
		return "batch", "cronjobs"
	}
	return "apps", strings.ToLower(kind) + "s"
}

func (client *kubeHTTPClient) Detail(ctx context.Context, key string) (Detail, error) {
	namespace, kind, name, err := parseWorkloadKey(key)
	if err != nil {
		return Detail{}, err
	}
	group, plural := resourceForKind(kind)
	var target struct {
		Metadata kubeMetadata `json:"metadata"`
	}
	if err := client.request(ctx, http.MethodGet, fmt.Sprintf("/apis/%s/v1/namespaces/%s/%s/%s", group, url.PathEscape(namespace), plural, url.PathEscape(name)), "", nil, &target); err != nil {
		return Detail{}, err
	}
	var pods kubeList[kubePod]
	if err := client.request(ctx, http.MethodGet, fmt.Sprintf("/api/v1/namespaces/%s/pods", url.PathEscape(namespace)), "", nil, &pods); err != nil {
		return Detail{}, err
	}
	ownerUIDs := map[string]struct{}{target.Metadata.UID: {}}
	if kind == "Deployment" {
		var replicaSets kubeList[kubeReplicaSet]
		if err := client.request(ctx, http.MethodGet, fmt.Sprintf("/apis/apps/v1/namespaces/%s/replicasets", url.PathEscape(namespace)), "", nil, &replicaSets); err == nil {
			for _, item := range replicaSets.Items {
				if owner := firstControllerOwner(item.Metadata.OwnerReferences, "Deployment"); owner != nil && owner.UID == target.Metadata.UID {
					ownerUIDs[item.Metadata.UID] = struct{}{}
				}
			}
		}
	}
	if kind == "CronJob" {
		var jobs kubeList[kubeJob]
		if err := client.request(ctx, http.MethodGet, fmt.Sprintf("/apis/batch/v1/namespaces/%s/jobs", url.PathEscape(namespace)), "", nil, &jobs); err == nil {
			for _, item := range jobs.Items {
				if owner := firstControllerOwner(item.Metadata.OwnerReferences, "CronJob"); owner != nil && owner.UID == target.Metadata.UID {
					ownerUIDs[item.Metadata.UID] = struct{}{}
				}
			}
		}
	}
	detail := Detail{}
	for _, pod := range pods.Items {
		owner := firstControllerOwner(pod.Metadata.OwnerReferences, "")
		if owner == nil {
			continue
		}
		if _, ok := ownerUIDs[owner.UID]; !ok {
			continue
		}
		readyCount, restarts := 0, 0
		for _, status := range pod.Status.ContainerStatuses {
			if status.Ready {
				readyCount++
			}
			restarts += status.RestartCount
		}
		containers := make([]string, 0, len(pod.Spec.Containers))
		for _, container := range pod.Spec.Containers {
			containers = append(containers, container.Name)
		}
		detail.Pods = append(detail.Pods, Pod{Name: pod.Metadata.Name, Namespace: namespace, Node: pod.Spec.NodeName, Phase: pod.Status.Phase,
			Ready: fmt.Sprintf("%d/%d", readyCount, len(pod.Spec.Containers)), Restarts: restarts, Containers: containers})
	}
	sort.Slice(detail.Pods, func(i, j int) bool { return detail.Pods[i].Name < detail.Pods[j].Name })
	selector := url.QueryEscape("involvedObject.kind=" + kind + ",involvedObject.name=" + name)
	var events kubeList[kubeEvent]
	if err := client.request(ctx, http.MethodGet, fmt.Sprintf("/api/v1/namespaces/%s/events?fieldSelector=%s", url.PathEscape(namespace), selector), "", nil, &events); err == nil {
		for _, event := range events.Items {
			at, _ := time.Parse(time.RFC3339Nano, event.EventTime)
			if at.IsZero() {
				at, _ = time.Parse(time.RFC3339, event.LastTimestamp)
			}
			detail.Events = append(detail.Events, Event{At: at, Type: event.Type, Reason: event.Reason, Message: event.Message})
		}
		sort.Slice(detail.Events, func(i, j int) bool { return detail.Events[i].At.After(detail.Events[j].At) })
	}
	return detail, nil
}

func (client *kubeHTTPClient) Logs(ctx context.Context, key string, limit int) ([]LogLine, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	detail, err := client.Detail(ctx, key)
	if err != nil {
		return nil, err
	}
	var result []LogLine
	for podIndex, pod := range detail.Pods {
		if podIndex >= 3 {
			break
		}
		for _, container := range pod.Containers {
			resourcePath := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/log?container=%s&tailLines=%d&timestamps=true", url.PathEscape(pod.Namespace), url.PathEscape(pod.Name), url.QueryEscape(container), limit)
			var raw json.RawMessage
			if err := client.requestRaw(ctx, http.MethodGet, resourcePath, &raw); err != nil {
				continue
			}
			for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
				if strings.TrimSpace(line) == "" {
					continue
				}
				at, text := splitLogTimestamp(line)
				result = append(result, LogLine{At: at, Pod: pod.Name, Container: container, Text: text})
			}
		}
	}
	if len(result) == 0 {
		return nil, errors.New("no readable Pod logs were returned")
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].At.Before(result[j].At) })
	return result, nil
}

func (client *kubeHTTPClient) clientRequest(ctx context.Context, method, resourcePath, contentType string, body []byte) (*http.Response, error) {
	target := *client.baseURL
	relative, err := url.Parse(resourcePath)
	if err != nil {
		return nil, err
	}
	target.Path = strings.TrimRight(client.baseURL.Path, "/") + relative.Path
	target.RawQuery = relative.RawQuery
	request, err := http.NewRequestWithContext(ctx, method, target.String(), strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if client.token != "" {
		request.Header.Set("Authorization", "Bearer "+client.token)
	} else if client.username != "" {
		request.SetBasicAuth(client.username, client.password)
	}
	return client.http.Do(request)
}

func (client *kubeHTTPClient) requestRaw(ctx context.Context, method, resourcePath string, output *json.RawMessage) error {
	response, err := client.clientRequest(ctx, method, resourcePath, "", nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Kubernetes log request returned %s", response.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil {
		return err
	}
	if len(raw) > 2<<20 {
		return errors.New("Kubernetes Pod log response is too large")
	}
	*output = raw
	return nil
}

func splitLogTimestamp(line string) (time.Time, string) {
	parts := strings.SplitN(line, " ", 2)
	if len(parts) == 2 {
		if at, err := time.Parse(time.RFC3339Nano, parts[0]); err == nil {
			return at, parts[1]
		}
	}
	return time.Time{}, line
}

func (client *kubeHTTPClient) Operate(ctx context.Context, operation Operation) error {
	namespace, kind, name, err := parseWorkloadKey(operation.WorkloadKey)
	if err != nil {
		return err
	}
	group, plural := resourceForKind(kind)
	base := fmt.Sprintf("/apis/%s/v1/namespaces/%s/%s/%s", group, url.PathEscape(namespace), plural, url.PathEscape(name))
	switch operation.Kind {
	case OperationRedeploy:
		if kind == "CronJob" {
			return errors.New("CronJob does not support redeploy")
		}
		payload := map[string]any{"spec": map[string]any{"template": map[string]any{"metadata": map[string]any{"annotations": map[string]string{
			"kubectl.kubernetes.io/restartedAt": time.Now().UTC().Format(time.RFC3339),
		}}}}}
		raw, _ := json.Marshal(payload)
		return client.request(ctx, http.MethodPatch, base, "application/merge-patch+json", raw, nil)
	case OperationScale:
		if kind != "Deployment" && kind != "StatefulSet" {
			return errors.New("only Deployment and StatefulSet support replica scaling")
		}
		if operation.Replicas < 0 || operation.Replicas > 1000 {
			return errors.New("replicas must be between 0 and 1000")
		}
		raw, _ := json.Marshal(map[string]any{"spec": map[string]int{"replicas": operation.Replicas}})
		return client.request(ctx, http.MethodPatch, base+"/scale", "application/merge-patch+json", raw, nil)
	case OperationRunCron:
		if kind != "CronJob" {
			return errors.New("only CronJob supports run now")
		}
		var cron map[string]any
		if err := client.request(ctx, http.MethodGet, base, "", nil, &cron); err != nil {
			return err
		}
		spec, ok := nestedMap(cron, "spec", "jobTemplate", "spec")
		if !ok {
			return errors.New("CronJob has no job template")
		}
		job := map[string]any{"apiVersion": "batch/v1", "kind": "Job", "metadata": map[string]any{
			"generateName": name + "-scriptboard-", "namespace": namespace,
			"annotations": map[string]string{"scriptboard.dev/started-from": operation.WorkloadKey},
		}, "spec": spec}
		raw, _ := json.Marshal(job)
		return client.request(ctx, http.MethodPost, fmt.Sprintf("/apis/batch/v1/namespaces/%s/jobs", url.PathEscape(namespace)), "application/json", raw, nil)
	default:
		return errors.New("unsupported Kubernetes operation")
	}
}

func nestedMap(value map[string]any, path ...string) (map[string]any, bool) {
	current := value
	for _, key := range path {
		next, ok := current[key].(map[string]any)
		if !ok {
			return nil, false
		}
		current = next
	}
	return current, true
}
