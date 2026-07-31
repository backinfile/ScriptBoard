package localtls

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestPinnedConfigVerifiesConfiguredLocalCertificate(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	certificatePath := filepath.Join(t.TempDir(), "server.pem")
	raw := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.TLS.Certificates[0].Certificate[0]})
	if err := os.WriteFile(certificatePath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := PinnedConfig(certificatePath, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if config.InsecureSkipVerify {
		t.Fatal("pinned probe disabled TLS verification")
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: config}}
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("probe request: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("probe status = %d", response.StatusCode)
	}
}
