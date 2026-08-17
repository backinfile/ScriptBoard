package clusterstatus

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKubeconfigFactoryUsesSelectedContextAndTLSCredentials(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer dedicated-token" {
			http.Error(response, "missing token", http.StatusUnauthorized)
			return
		}
		if request.URL.Path == "/version" {
			_, _ = response.Write([]byte(`{"gitVersion":"v1.35.1"}`))
			return
		}
		if request.URL.Path == "/apis/authorization.k8s.io/v1/selfsubjectaccessreviews" {
			_, _ = response.Write([]byte(`{"status":{"allowed":true}}`))
			return
		}
		http.NotFound(response, request)
	}))
	defer server.Close()
	certificate := server.Certificate()
	pool := x509.NewCertPool()
	pool.AddCert(certificate)
	if _, err := certificate.Verify(x509.VerifyOptions{Roots: pool}); err != nil {
		t.Fatal(err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})
	kubeconfig := fmt.Sprintf(`apiVersion: v1
kind: Config
current-context: ignored
clusters:
- name: selected-cluster
  cluster:
    server: %s
    certificate-authority-data: %s
users:
- name: scriptboard
  user:
    token: dedicated-token
contexts:
- name: selected
  context:
    cluster: selected-cluster
    user: scriptboard
- name: ignored
  context:
    cluster: missing
    user: missing
`, server.URL, base64.StdEncoding.EncodeToString(certificatePEM))
	path := filepath.Join(t.TempDir(), "kubeconfig.yaml")
	if err := os.WriteFile(path, []byte(kubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := (HTTPFactory{}).Open(context.Background(), Connection{Name: "local", KubeconfigPath: path, Context: "selected", Mode: ModeLimited})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	capabilities, err := client.Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !capabilities.Workloads || !capabilities.Logs || !capabilities.Redeploy || !capabilities.Scale || !capabilities.RunCron {
		t.Fatalf("capabilities: %#v", capabilities)
	}
	if !strings.HasPrefix(client.Fingerprint(), "sha256:") {
		t.Fatalf("fingerprint=%q", client.Fingerprint())
	}
}

func TestKubeconfigFactorySupportsPlainHTTPServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer plain-token" {
			http.Error(response, "missing token", http.StatusUnauthorized)
			return
		}
		switch request.URL.Path {
		case "/version":
			_, _ = response.Write([]byte(`{"gitVersion":"v1.35.1"}`))
		case "/apis/authorization.k8s.io/v1/selfsubjectaccessreviews":
			_, _ = response.Write([]byte(`{"status":{"allowed":true}}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	kubeconfig := fmt.Sprintf(`apiVersion: v1
kind: Config
current-context: plain
clusters:
- name: plain-cluster
  cluster:
    server: %s
users:
- name: scriptboard
  user:
    token: plain-token
contexts:
- name: plain
  context:
    cluster: plain-cluster
    user: scriptboard
`, server.URL)
	path := filepath.Join(t.TempDir(), "kubeconfig.yaml")
	if err := os.WriteFile(path, []byte(kubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := (HTTPFactory{}).Open(context.Background(), Connection{Name: "plain", KubeconfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	capabilities, err := client.Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !capabilities.Workloads {
		t.Fatalf("capabilities=%#v", capabilities)
	}
}

func TestKubeconfigCandidateRejectsExternalCredentialFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kubeconfig.yaml")
	config := `clusters:
- name: local
  cluster:
    server: https://127.0.0.1:6443
    certificate-authority: root-ca.pem
users:
- name: scriptboard
  user:
    tokenFile: root-token
contexts:
- name: local
  context:
    cluster: local
    user: scriptboard
`
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (HTTPFactory{}).OpenCandidate(context.Background(), Connection{Name: "candidate", KubeconfigPath: path}); err == nil {
		t.Fatal("candidate with external credential files was accepted")
	}
}

func TestKubeconfigRootCAsReplaceSystemTrustWhenExplicit(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	certificate := server.Certificate()
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})

	pool, err := kubeconfigRootCAs(certificatePEM)
	if err != nil {
		t.Fatal(err)
	}
	subjects := pool.Subjects()
	if len(subjects) != 1 || !bytes.Equal(subjects[0], certificate.RawSubject) {
		t.Fatalf("explicit kubeconfig CA did not replace system roots: got %d subjects", len(subjects))
	}
}

func TestKubeconfigFactoryRejectsExecutableAuthentication(t *testing.T) {
	config := `apiVersion: v1
kind: Config
current-context: selected
clusters:
- name: cluster
  cluster:
    server: https://127.0.0.1:6443
users:
- name: scriptboard
  user:
    exec:
      command: steal-credentials
contexts:
- name: selected
  context:
    cluster: cluster
    user: scriptboard
`
	path := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (HTTPFactory{}).Open(context.Background(), Connection{Name: "cluster", KubeconfigPath: path}); err == nil {
		t.Fatal("unsafe kubeconfig was accepted")
	}
}

func TestKubeconfigFactoryAllowsInsecureTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/version":
			_, _ = response.Write([]byte(`{"gitVersion":"v1.35.1"}`))
		case "/apis/authorization.k8s.io/v1/selfsubjectaccessreviews":
			_, _ = response.Write([]byte(`{"status":{"allowed":true}}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	config := fmt.Sprintf(`apiVersion: v1
kind: Config
current-context: selected
clusters:
- name: cluster
  cluster:
    server: %s
    insecure-skip-tls-verify: true
users:
- name: scriptboard
  user:
    token: token
contexts:
- name: selected
  context:
    cluster: cluster
    user: scriptboard
`, server.URL)
	path := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := (HTTPFactory{}).Open(context.Background(), Connection{Name: "cluster", KubeconfigPath: path})
	if err != nil {
		t.Fatalf("open insecure TLS kubeconfig: %v", err)
	}
	defer client.Close()
	capabilities, err := client.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("probe insecure TLS kubeconfig: %v", err)
	}
	if !capabilities.Workloads {
		t.Fatalf("capabilities=%#v", capabilities)
	}
}
