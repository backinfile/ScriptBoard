package websitemonitor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestScanNginxReturnsCandidatesWithoutImporting(t *testing.T) {
	root := t.TempDir()
	configRoot := filepath.Join(root, "conf")
	if err := os.MkdirAll(filepath.Join(configRoot, "conf.d"), 0o700); err != nil {
		t.Fatal(err)
	}
	mainConfig := filepath.Join(configRoot, "nginx.conf")
	if err := os.WriteFile(mainConfig, []byte(`
		events {}
		http {
			include "conf.d/*.conf";
		}
	`), 0o600); err != nil {
		t.Fatal(err)
	}
	childConfig := filepath.Join(configRoot, "conf.d", "sites.conf")
	if err := os.WriteFile(childConfig, []byte(`
		server {
			listen 443 ssl;
			server_name app.local api.local;
		}
		server {
			listen 80;
			server_name *.ignored.local;
		}
	`), 0o600); err != nil {
		t.Fatal(err)
	}

	manager := newTestManager(t, Options{})
	preview, err := manager.ScanNginx(context.Background(), NginxScanRequest{ConfigPath: mainConfig})
	if err != nil {
		t.Fatalf("scan Nginx: %v", err)
	}
	if len(preview.Candidates) != 2 {
		t.Fatalf("candidates = %#v, want app.local and api.local", preview.Candidates)
	}
	for _, candidate := range preview.Candidates {
		if candidate.URL != "https://"+candidate.Name+"/" {
			t.Errorf("candidate URL = %q for %q", candidate.URL, candidate.Name)
		}
		sourceInfo, sourceErr := os.Stat(candidate.Source)
		childInfo, childErr := os.Stat(childConfig)
		sameSource := sourceErr == nil && childErr == nil && os.SameFile(sourceInfo, childInfo)
		if candidate.DialHost != "127.0.0.1" || candidate.Digest == "" || !sameSource {
			t.Errorf("candidate = %#v", candidate)
		}
		if candidate.Duplicate {
			t.Errorf("new candidate marked duplicate: %#v", candidate)
		}
	}
	if len(preview.Warnings) == 0 {
		t.Fatal("wildcard server_name was skipped without a warning")
	}
	monitors, err := manager.List(context.Background(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(monitors) != 0 {
		t.Fatalf("scan imported monitors: %#v", monitors)
	}
}

func TestImportNginxAddsOnlySelectedFreshCandidatesAndMarksDuplicates(t *testing.T) {
	root := t.TempDir()
	mainConfig := filepath.Join(root, "nginx.conf")
	if err := os.WriteFile(mainConfig, []byte(`
		http {
			server { listen 8080; server_name admin.local; }
			server { listen 9090; server_name hooks.local; }
		}
	`), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := newTestManager(t, Options{Probe: probeFunc(func(context.Context, Config) Evidence {
		return Evidence{Success: true}
	})})
	preview, err := manager.ScanNginx(context.Background(), NginxScanRequest{ConfigPath: mainConfig})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Candidates) != 2 {
		t.Fatalf("preview = %#v", preview)
	}
	if preview.SelectableCount != 2 || preview.DuplicateCount != 0 {
		t.Fatalf("preview counts = selectable %d duplicate %d", preview.SelectableCount, preview.DuplicateCount)
	}
	_, err = manager.ImportNginx(context.Background(), NginxImportRequest{
		Scan: NginxScanRequest{ConfigPath: mainConfig},
	})
	var operationError *OperationError
	if !errors.As(err, &operationError) || operationError.Code != ErrorSelectionRequired {
		t.Fatalf("empty selection error = %#v, want %q", err, ErrorSelectionRequired)
	}
	selected := preview.Candidates[0]
	imported, err := manager.ImportNginx(context.Background(), NginxImportRequest{
		Scan:    NginxScanRequest{ConfigPath: mainConfig},
		Digests: []string{selected.Digest},
	})
	if err != nil {
		t.Fatalf("import selected candidate: %v", err)
	}
	if len(imported) != 1 || imported[0].Config.URL != selected.URL ||
		imported[0].Config.DialHost != selected.DialHost ||
		imported[0].Config.Source != "nginx:"+selected.Source {
		t.Fatalf("imported = %#v", imported)
	}

	nextPreview, err := manager.ScanNginx(context.Background(), NginxScanRequest{ConfigPath: mainConfig})
	if err != nil {
		t.Fatal(err)
	}
	duplicates := 0
	for _, candidate := range nextPreview.Candidates {
		if candidate.Duplicate {
			duplicates++
		}
	}
	if duplicates != 1 {
		t.Fatalf("duplicate candidates = %#v", nextPreview.Candidates)
	}
	if nextPreview.SelectableCount != 1 || nextPreview.DuplicateCount != 1 {
		t.Fatalf("next preview counts = selectable %d duplicate %d",
			nextPreview.SelectableCount, nextPreview.DuplicateCount)
	}
	_, err = manager.ImportNginx(context.Background(), NginxImportRequest{
		Scan: NginxScanRequest{ConfigPath: mainConfig}, Digests: []string{selected.Digest},
	})
	operationError = nil
	if !errors.As(err, &operationError) || operationError.Code != ErrorDuplicate {
		t.Fatalf("duplicate error = %#v, want %q", err, ErrorDuplicate)
	}
}

func TestScanNginxDiscoversProcessPrefixAndConfigArguments(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "conf", "custom.conf")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`
		http { include conf/sites.conf; }
	`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "conf", "sites.conf"), []byte(`
		server { listen 80; server_name process.local; }
	`), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := newTestManager(t, Options{NginxProcesses: nginxProcessSourceFunc(func(context.Context) ([]NginxProcess, error) {
		return []NginxProcess{{
			Name: "nginx",
			Args: []string{"nginx", "-p", root, "-c", "conf/custom.conf"},
		}}, nil
	})})

	preview, err := manager.ScanNginx(context.Background(), NginxScanRequest{})
	if err != nil {
		t.Fatalf("scan process-discovered config: %v", err)
	}
	if len(preview.Candidates) != 1 || preview.Candidates[0].URL != "http://process.local/" {
		t.Fatalf("preview = %#v", preview)
	}
}

type nginxProcessSourceFunc func(context.Context) ([]NginxProcess, error)

func (function nginxProcessSourceFunc) Processes(ctx context.Context) ([]NginxProcess, error) {
	return function(ctx)
}
