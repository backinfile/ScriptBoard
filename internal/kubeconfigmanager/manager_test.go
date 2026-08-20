package kubeconfigmanager

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const fixtureConfig = `apiVersion: v1
kind: Config
clusters:
- name: production
  cluster:
    server: https://production.example.test
- name: staging
  cluster:
    server: https://staging.example.test
users:
- name: admin
  user:
    token: secret
- name: developer
  user: {}
contexts:
- name: production-admin
  context:
    cluster: production
    user: admin
    namespace: default
- name: staging-dev
  context:
    cluster: staging
    user: developer
    namespace: preview
current-context: production-admin
`

func writeFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(fixtureConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSuggestedPathsIncludesCommonLinuxDistributions(t *testing.T) {
	paths := suggestedPaths("linux", "/home/scriptboard/.kube/config")
	for _, expected := range []string{
		"/home/scriptboard/.kube/config",
		"/etc/rancher/k3s/k3s.yaml",
		"/etc/rancher/rke2/rke2.yaml",
		"/etc/kubernetes/admin.conf",
		"/var/snap/microk8s/current/credentials/client.config",
		"/var/lib/k0s/pki/admin.conf",
	} {
		if !containsPath(paths, expected) {
			t.Fatalf("suggested paths %q do not include %q", paths, expected)
		}
	}
}

func containsPath(paths []string, expected string) bool {
	for _, path := range paths {
		if path == expected {
			return true
		}
	}
	return false
}

func TestContextLifecycleAndDownloads(t *testing.T) {
	path := writeFixture(t)
	snapshot, err := Inspect(path)
	if err != nil || len(snapshot.Contexts) != 2 || snapshot.Current != "production-admin" {
		t.Fatalf("inspect = %#v, %v", snapshot, err)
	}
	if err := UseContext(path, "staging-dev"); err != nil {
		t.Fatal(err)
	}
	if err := UpdateContext(path, "staging-dev", "production", "admin", "tools"); err != nil {
		t.Fatal(err)
	}
	if err := RenameContext(path, "staging-dev", "tools-admin"); err != nil {
		t.Fatal(err)
	}
	snapshot, err = Inspect(path)
	if err != nil || snapshot.Current != "tools-admin" || snapshot.Contexts[1].Namespace != "tools" {
		t.Fatalf("updated snapshot = %#v, %v", snapshot, err)
	}
	exported, err := DownloadContext(path, "tools-admin")
	if err != nil {
		t.Fatal(err)
	}
	text := string(exported)
	for _, expected := range []string{"name: tools-admin", "name: production", "name: admin", "current-context: tools-admin"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("context export missing %q:\n%s", expected, text)
		}
	}
	if strings.Contains(text, "staging.example.test") || strings.Contains(text, "token: secret\n- name: developer") {
		t.Fatalf("context export contains unrelated entries:\n%s", text)
	}
	if err := DeleteContext(path, "tools-admin"); err != nil {
		t.Fatal(err)
	}
	snapshot, err = Inspect(path)
	if err != nil || snapshot.Current != "" || len(snapshot.Contexts) != 1 {
		t.Fatalf("deleted snapshot = %#v, %v", snapshot, err)
	}
}

func TestImportPreviewsConflictsAndMergesAtomically(t *testing.T) {
	path := writeFixture(t)
	incoming := []byte(`apiVersion: v1
kind: Config
clusters:
- name: staging
  cluster:
    server: https://replacement.example.test
- name: lab
  cluster:
    server: https://lab.example.test
users:
- name: lab-user
  user: {}
contexts:
- name: staging-dev
  context:
    cluster: staging
    user: lab-user
- name: lab
  context:
    cluster: lab
    user: lab-user
current-context: lab
`)
	preview, err := PreviewImport(path, incoming)
	if err != nil || preview.Clusters != 2 || preview.Users != 1 || preview.Contexts != 2 || len(preview.Conflicts) != 2 {
		t.Fatalf("preview = %#v, %v", preview, err)
	}
	if _, err := Import(path, incoming, true); err != nil {
		t.Fatal(err)
	}
	snapshot, err := Inspect(path)
	if err != nil || snapshot.Current != "lab" || len(snapshot.Contexts) != 3 {
		t.Fatalf("merged snapshot = %#v, %v", snapshot, err)
	}
	raw, err := Download(path)
	if err != nil || !strings.Contains(string(raw), "https://replacement.example.test") {
		t.Fatalf("download after import = %v, %v", string(raw), err)
	}
	if info, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("kubeconfig permissions = %v", info.Mode())
	}
}

func TestRejectsInvalidAndOversizedInput(t *testing.T) {
	path := writeFixture(t)
	if _, err := PreviewImport(path, []byte("contexts: wrong\n")); err == nil {
		t.Fatal("invalid contexts sequence was accepted")
	}
	if _, err := PreviewImport(path, make([]byte, MaxFileSize+1)); err == nil {
		t.Fatal("oversized input was accepted")
	}
	if err := RenameContext(path, "production-admin", "staging-dev"); err == nil {
		t.Fatal("duplicate context rename was accepted")
	}
}
