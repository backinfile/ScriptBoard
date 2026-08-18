package clusterstatus

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

const maxCredentialFileSize = 2 << 20

type HTTPFactory struct{}

type KubeconfigOpenErrorKind string

const (
	KubeconfigUnreadable             KubeconfigOpenErrorKind = "unreadable"
	KubeconfigInvalid                KubeconfigOpenErrorKind = "invalid"
	KubeconfigRequiresEmbeddedCA     KubeconfigOpenErrorKind = "requires_embedded_ca"
	KubeconfigRequiresEmbeddedAuth   KubeconfigOpenErrorKind = "requires_embedded_auth"
	KubeconfigUnsupportedAuth        KubeconfigOpenErrorKind = "unsupported_auth"
	KubeconfigNoSelectedContext      KubeconfigOpenErrorKind = "no_selected_context"
	KubeconfigContextNotFound        KubeconfigOpenErrorKind = "context_not_found"
	KubeconfigInvalidServer          KubeconfigOpenErrorKind = "invalid_server"
	KubeconfigUnsupportedCredentials KubeconfigOpenErrorKind = "unsupported_credentials"
	KubeconfigInvalidTLSMaterial     KubeconfigOpenErrorKind = "invalid_tls_material"
)

// KubeconfigOpenError gives privilege boundaries a stable, non-secret reason
// while retaining the detailed cause for trusted callers and logs.
type KubeconfigOpenError struct {
	Kind  KubeconfigOpenErrorKind
	Cause error
}

func (err *KubeconfigOpenError) Error() string { return err.Cause.Error() }
func (err *KubeconfigOpenError) Unwrap() error { return err.Cause }

func kubeconfigOpenError(kind KubeconfigOpenErrorKind, cause error) error {
	return &KubeconfigOpenError{Kind: kind, Cause: cause}
}

// OpenCandidate validates and opens the same immutable kubeconfig bytes so a
// writable path cannot be swapped between the security check and client setup.
func (HTTPFactory) OpenCandidate(ctx context.Context, connection Connection) (Client, error) {
	raw, err := readBoundedFile(filepath.Clean(connection.KubeconfigPath))
	if err != nil {
		return nil, kubeconfigOpenError(KubeconfigUnreadable, fmt.Errorf("read kubeconfig candidate: %w", err))
	}
	if err := validateKubeconfigCandidate(raw); err != nil {
		return nil, err
	}
	return openKubeconfig(ctx, connection, raw)
}

func validateKubeconfigCandidate(raw []byte) error {
	var config kubeconfigFile
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(false)
	if err := decoder.Decode(&config); err != nil {
		return kubeconfigOpenError(KubeconfigInvalid, fmt.Errorf("parse kubeconfig candidate: %w", err))
	}
	for _, cluster := range config.Clusters {
		if strings.TrimSpace(cluster.Cluster.CertificateAuthority) != "" {
			return kubeconfigOpenError(KubeconfigRequiresEmbeddedCA, errors.New("new kubeconfig connections must embed certificate authority data"))
		}
	}
	for _, user := range config.Users {
		if strings.TrimSpace(user.User.TokenFile) != "" || strings.TrimSpace(user.User.ClientCertificate) != "" || strings.TrimSpace(user.User.ClientKey) != "" {
			return kubeconfigOpenError(KubeconfigRequiresEmbeddedAuth, errors.New("new kubeconfig connections must embed token, client certificate, and key data"))
		}
	}
	return nil
}

type kubeconfigFile struct {
	CurrentContext string `yaml:"current-context"`
	Clusters       []struct {
		Name    string `yaml:"name"`
		Cluster struct {
			Server                   string `yaml:"server"`
			CertificateAuthority     string `yaml:"certificate-authority"`
			CertificateAuthorityData string `yaml:"certificate-authority-data"`
			InsecureSkipTLSVerify    bool   `yaml:"insecure-skip-tls-verify"`
		} `yaml:"cluster"`
	} `yaml:"clusters"`
	Users []struct {
		Name string `yaml:"name"`
		User struct {
			Token                 string         `yaml:"token"`
			TokenFile             string         `yaml:"tokenFile"`
			ClientCertificate     string         `yaml:"client-certificate"`
			ClientCertificateData string         `yaml:"client-certificate-data"`
			ClientKey             string         `yaml:"client-key"`
			ClientKeyData         string         `yaml:"client-key-data"`
			Username              string         `yaml:"username"`
			Password              string         `yaml:"password"`
			Exec                  map[string]any `yaml:"exec"`
			AuthProvider          map[string]any `yaml:"auth-provider"`
		} `yaml:"user"`
	} `yaml:"users"`
	Contexts []struct {
		Name    string `yaml:"name"`
		Context struct {
			Cluster   string `yaml:"cluster"`
			User      string `yaml:"user"`
			Namespace string `yaml:"namespace"`
		} `yaml:"context"`
	} `yaml:"contexts"`
}

type kubeHTTPClient struct {
	baseURL     *url.URL
	http        *http.Client
	token       string
	username    string
	password    string
	defaultNS   string
	fingerprint string
}

func (HTTPFactory) Open(ctx context.Context, connection Connection) (Client, error) {
	path := filepath.Clean(connection.KubeconfigPath)
	raw, err := readBoundedFile(path)
	if err != nil {
		return nil, kubeconfigOpenError(KubeconfigUnreadable, fmt.Errorf("read kubeconfig: %w", err))
	}
	return openKubeconfig(ctx, connection, raw)
}

func openKubeconfig(_ context.Context, connection Connection, raw []byte) (Client, error) {
	path := filepath.Clean(connection.KubeconfigPath)
	var config kubeconfigFile
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(false)
	if err := decoder.Decode(&config); err != nil {
		return nil, kubeconfigOpenError(KubeconfigInvalid, fmt.Errorf("parse kubeconfig: %w", err))
	}
	contextName := strings.TrimSpace(connection.Context)
	if contextName == "" {
		contextName = strings.TrimSpace(config.CurrentContext)
	}
	if contextName == "" {
		return nil, kubeconfigOpenError(KubeconfigNoSelectedContext, errors.New("kubeconfig has no selected context"))
	}
	var clusterName, userName, namespace string
	for _, candidate := range config.Contexts {
		if candidate.Name == contextName {
			clusterName, userName, namespace = candidate.Context.Cluster, candidate.Context.User, candidate.Context.Namespace
			break
		}
	}
	if clusterName == "" {
		return nil, kubeconfigOpenError(KubeconfigContextNotFound, fmt.Errorf("kubeconfig context %q was not found", contextName))
	}
	var server, caFile, caData string
	var insecure bool
	for _, candidate := range config.Clusters {
		if candidate.Name == clusterName {
			server = strings.TrimSpace(candidate.Cluster.Server)
			caFile, caData, insecure = candidate.Cluster.CertificateAuthority, candidate.Cluster.CertificateAuthorityData, candidate.Cluster.InsecureSkipTLSVerify
			break
		}
	}
	baseURL, err := url.Parse(server)
	// The kubeconfig scheme is an explicit transport choice; HTTP is not silently upgraded to TLS.
	if err != nil || (baseURL.Scheme != "http" && baseURL.Scheme != "https") || baseURL.Host == "" {
		return nil, kubeconfigOpenError(KubeconfigInvalidServer, errors.New("Kubernetes server must be an absolute HTTP or HTTPS URL"))
	}
	baseURL.Path = strings.TrimRight(baseURL.Path, "/")
	var token, tokenFile, certificateFile, certificateData, keyFile, keyData, username, password string
	for _, candidate := range config.Users {
		if candidate.Name != userName {
			continue
		}
		if len(candidate.User.Exec) > 0 || len(candidate.User.AuthProvider) > 0 {
			return nil, kubeconfigOpenError(KubeconfigUnsupportedAuth, errors.New("executable and auth-provider kubeconfig credentials are not supported"))
		}
		token, tokenFile = candidate.User.Token, candidate.User.TokenFile
		certificateFile, certificateData = candidate.User.ClientCertificate, candidate.User.ClientCertificateData
		keyFile, keyData = candidate.User.ClientKey, candidate.User.ClientKeyData
		username, password = candidate.User.Username, candidate.User.Password
		break
	}
	directory := filepath.Dir(path)
	if token == "" && tokenFile != "" {
		rawToken, err := readBoundedFile(resolveKubeconfigPath(directory, tokenFile))
		if err != nil {
			return nil, fmt.Errorf("read kubeconfig token file: %w", err)
		}
		token = strings.TrimSpace(string(rawToken))
	}
	caPEM, err := kubeconfigBytes(directory, caFile, caData)
	if err != nil {
		return nil, fmt.Errorf("load Kubernetes certificate authority: %w", err)
	}
	rootCAs, err := kubeconfigRootCAs(caPEM)
	if err != nil {
		return nil, kubeconfigOpenError(KubeconfigInvalidTLSMaterial, err)
	}
	// The kubeconfig explicitly controls peer verification. This mirrors kubectl
	// and keeps HTTPS available for clusters that intentionally use insecure TLS.
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: rootCAs, InsecureSkipVerify: insecure} //nolint:gosec
	certificatePEM, err := kubeconfigBytes(directory, certificateFile, certificateData)
	if err != nil {
		return nil, kubeconfigOpenError(KubeconfigInvalidTLSMaterial, fmt.Errorf("load Kubernetes client certificate: %w", err))
	}
	keyPEM, err := kubeconfigBytes(directory, keyFile, keyData)
	if err != nil {
		return nil, kubeconfigOpenError(KubeconfigInvalidTLSMaterial, fmt.Errorf("load Kubernetes client key: %w", err))
	}
	if len(certificatePEM) > 0 || len(keyPEM) > 0 {
		if len(certificatePEM) == 0 || len(keyPEM) == 0 {
			return nil, kubeconfigOpenError(KubeconfigInvalidTLSMaterial, errors.New("both Kubernetes client certificate and key are required"))
		}
		certificate, err := tls.X509KeyPair(certificatePEM, keyPEM)
		if err != nil {
			return nil, kubeconfigOpenError(KubeconfigInvalidTLSMaterial, fmt.Errorf("parse Kubernetes client certificate: %w", err))
		}
		tlsConfig.Certificates = []tls.Certificate{certificate}
	}
	if token == "" && (baseURL.Scheme != "https" || len(tlsConfig.Certificates) == 0) && username == "" {
		return nil, kubeconfigOpenError(KubeconfigUnsupportedCredentials, errors.New("kubeconfig context has no supported credentials"))
	}
	digest := sha256.Sum256(append([]byte(baseURL.String()+"\x00"), caPEM...))
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment, ForceAttemptHTTP2: true}
	if baseURL.Scheme == "https" {
		transport.TLSClientConfig = tlsConfig
	}
	return &kubeHTTPClient{
		baseURL: baseURL, http: &http.Client{Transport: transport, Timeout: 10 * time.Second}, token: strings.TrimSpace(token),
		username: username, password: password, defaultNS: namespace, fingerprint: "sha256:" + hex.EncodeToString(digest[:]),
	}, nil
}

func kubeconfigRootCAs(caPEM []byte) (*x509.CertPool, error) {
	if len(caPEM) == 0 {
		rootCAs, err := x509.SystemCertPool()
		if err == nil && rootCAs != nil {
			return rootCAs, nil
		}
		return x509.NewCertPool(), nil
	}

	// An explicit kubeconfig CA replaces system trust, matching Kubernetes TLS semantics.
	rootCAs := x509.NewCertPool()
	if !rootCAs.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("Kubernetes certificate authority is invalid")
	}
	return rootCAs, nil
}

func readBoundedFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxCredentialFileSize {
		return nil, errors.New("credential file is not a bounded regular file")
	}
	content, err := io.ReadAll(io.LimitReader(file, maxCredentialFileSize+1))
	if err != nil {
		return nil, err
	}
	if len(content) > maxCredentialFileSize {
		return nil, errors.New("credential file is too large")
	}
	return content, nil
}

func resolveKubeconfigPath(directory, value string) string {
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Join(directory, value)
}

func kubeconfigBytes(directory, file, data string) ([]byte, error) {
	if strings.TrimSpace(data) != "" {
		return base64.StdEncoding.DecodeString(strings.TrimSpace(data))
	}
	if strings.TrimSpace(file) != "" {
		return readBoundedFile(resolveKubeconfigPath(directory, file))
	}
	return nil, nil
}

func (client *kubeHTTPClient) Close() error {
	if transport, ok := client.http.Transport.(*http.Transport); ok {
		transport.CloseIdleConnections()
	}
	return nil
}

func (client *kubeHTTPClient) Fingerprint() string { return client.fingerprint }

func (client *kubeHTTPClient) request(ctx context.Context, method, resourcePath, contentType string, body []byte, output any) error {
	target := *client.baseURL
	relative, err := url.Parse(resourcePath)
	if err != nil {
		return err
	}
	target.Path = strings.TrimRight(client.baseURL.Path, "/") + relative.Path
	target.RawQuery = relative.RawQuery
	request, err := http.NewRequestWithContext(ctx, method, target.String(), bytes.NewReader(body))
	if err != nil {
		return err
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
	response, err := client.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("Kubernetes %s %s returned %s: %s", method, resourcePath, response.Status, strings.TrimSpace(string(raw)))
	}
	if output == nil {
		_, _ = io.Copy(io.Discard, response.Body)
		return nil
	}
	return json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(output)
}

func (client *kubeHTTPClient) Capabilities(ctx context.Context) (Capabilities, error) {
	var version struct {
		GitVersion string `json:"gitVersion"`
	}
	if err := client.request(ctx, http.MethodGet, "/version", "", nil, &version); err != nil {
		return Capabilities{}, err
	}
	workloads := false
	for _, resource := range []struct{ group, name string }{{"apps", "deployments"}, {"apps", "statefulsets"}, {"apps", "daemonsets"}, {"batch", "cronjobs"}} {
		allowed, err := client.allowed(ctx, "list", resource.group, resource.name, "", "")
		if err != nil {
			return Capabilities{}, err
		}
		workloads = workloads || allowed
	}
	nodes, _ := client.allowed(ctx, "list", "", "nodes", "", "")
	metrics, _ := client.allowed(ctx, "list", "metrics.k8s.io", "pods", "", "")
	logs, _ := client.allowed(ctx, "get", "", "pods", "log", "")
	redeploy, _ := client.allowed(ctx, "patch", "apps", "deployments", "", "")
	scale, _ := client.allowed(ctx, "patch", "apps", "deployments", "scale", "")
	runCron, _ := client.allowed(ctx, "create", "batch", "jobs", "", "")
	return Capabilities{Workloads: workloads, Nodes: nodes, Metrics: metrics, Logs: logs, Redeploy: redeploy, Scale: scale, RunCron: runCron}, nil
}

func (client *kubeHTTPClient) allowed(ctx context.Context, verb, group, resource, subresource, namespace string) (bool, error) {
	payload := map[string]any{"apiVersion": "authorization.k8s.io/v1", "kind": "SelfSubjectAccessReview", "spec": map[string]any{
		"resourceAttributes": map[string]string{"verb": verb, "group": group, "resource": resource, "subresource": subresource, "namespace": namespace},
	}}
	raw, _ := json.Marshal(payload)
	var result struct {
		Status struct {
			Allowed bool `json:"allowed"`
		} `json:"status"`
	}
	if err := client.request(ctx, http.MethodPost, "/apis/authorization.k8s.io/v1/selfsubjectaccessreviews", "application/json", raw, &result); err != nil {
		return false, err
	}
	return result.Status.Allowed, nil
}
