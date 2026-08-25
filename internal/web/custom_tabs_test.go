package web_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestCustomTabsDrawerActivationReorderAndFrameContract(t *testing.T) {
	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "host"), filepath.Join(root, "state"))
	response, err := client.Get(serverURL + "/config/custom-tabs")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	response.Body.Close()
	rendered := string(page)
	if !strings.Contains(rendered, `class="page-heading primary-page-heading"`) || !strings.Contains(rendered, `data-dashboard-drawer-id="custom-tab-create"`) {
		t.Fatal("custom tabs page does not use the shared title and drawer structure")
	}
	if strings.Contains(rendered, `/move`) || !strings.Contains(rendered, `>调整顺序</a>`) {
		t.Fatal("move controls must remain hidden before reorder mode")
	}

	response, err = client.PostForm(serverURL+"/config/custom-tabs", url.Values{
		"csrf_token": {formToken(t, page)}, "name": {"本地文档"}, "target_url": {"http://127.0.0.1:8080/docs"}, "credential_mode": {"target_state"},
	})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("create status=%d", response.StatusCode)
	}

	response, _ = client.Get(serverURL + "/config/custom-tabs")
	page, _ = io.ReadAll(response.Body)
	response.Body.Close()
	match := regexp.MustCompile(`/config/custom-tabs/([^/\"]+)/toggle`).FindSubmatch(page)
	if len(match) != 2 {
		t.Fatal("created tab missing")
	}
	id := string(match[1])
	if strings.Contains(string(page), `href="/defined/tabs/`+id+`"`) {
		t.Fatal("disabled tab appeared in Defined navigation")
	}
	response, err = client.PostForm(serverURL+"/config/custom-tabs/"+id+"/toggle", url.Values{"csrf_token": {formToken(t, page)}, "enabled": {"true"}})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()

	response, _ = client.Get(serverURL + "/config/custom-tabs?reorder=1")
	reorder, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if !strings.Contains(string(reorder), `/config/custom-tabs/`+id+`/move`) || !strings.Contains(string(reorder), `>完成排序</a>`) || !strings.Contains(string(reorder), `href="/defined/tabs/`+id+`"`) {
		t.Fatal("reorder controls or Defined navigation missing after activation")
	}
	response, err = client.PostForm(serverURL+"/config/custom-tabs/"+id+"/move", url.Values{"csrf_token": {formToken(t, reorder)}, "direction": {"up"}})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("move without reorder proof status=%d", response.StatusCode)
	}

	response, err = client.Get(serverURL + "/defined/tabs/" + id)
	if err != nil {
		t.Fatal(err)
	}
	frame, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(response.Header.Get("Content-Security-Policy"), "frame-src http://127.0.0.1:8080") || !strings.Contains(string(frame), `sandbox="allow-scripts allow-forms allow-same-origin allow-storage-access-by-user-activation"`) {
		t.Fatal("frame origin or sandbox contract is incorrect")
	}
}

func TestCustomTabKeyUsesOneTimeChallengeAndNeverRendersSecret(t *testing.T) {
	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "host"), filepath.Join(root, "state"))
	response, _ := client.Get(serverURL + "/config/custom-tabs")
	page, _ := io.ReadAll(response.Body)
	response.Body.Close()
	const secret = "local-preview-secret"
	response, err := client.PostForm(serverURL+"/config/custom-tabs", url.Values{"csrf_token": {formToken(t, page)}, "name": {"密钥页面"}, "target_url": {"https://example.test/app"}, "credential_mode": {"key"}, "key_name": {"access_token"}, "key": {secret}})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	response, _ = client.Get(serverURL + "/config/custom-tabs")
	page, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if strings.Contains(string(page), secret) {
		t.Fatal("saved Key was rendered in HTML")
	}
	match := regexp.MustCompile(`/config/custom-tabs/([^/\"]+)/toggle`).FindSubmatch(page)
	if len(match) != 2 {
		t.Fatal("key tab missing")
	}
	id := string(match[1])
	response, _ = client.PostForm(serverURL+"/config/custom-tabs/"+id+"/toggle", url.Values{"csrf_token": {formToken(t, page)}, "enabled": {"true"}})
	response.Body.Close()
	response, _ = client.Get(serverURL + "/defined/tabs/" + id)
	frame, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if strings.Contains(string(frame), secret) {
		t.Fatal("saved Key was rendered in frame HTML")
	}
	csrf := formToken(t, page)
	response, err = client.PostForm(serverURL+"/defined/tabs/"+id+"/key-challenge", url.Values{"csrf_token": {csrf}})
	if err != nil {
		t.Fatal(err)
	}
	var challenge map[string]any
	if err := json.NewDecoder(response.Body).Decode(&challenge); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	nonce, _ := challenge["nonce"].(string)
	if nonce == "" || strings.Contains(mustJSON(t, challenge), secret) {
		t.Fatal("challenge is missing a nonce or contains the Key")
	}
	delivery := url.Values{"csrf_token": {csrf}, "nonce": {nonce}}
	response, _ = client.PostForm(serverURL+"/defined/tabs/"+id+"/key-delivery", delivery)
	delivered, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(delivered), secret) {
		t.Fatal("valid challenge did not deliver the Key")
	}
	response, _ = client.PostForm(serverURL+"/defined/tabs/"+id+"/key-delivery", delivery)
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("replayed challenge status=%d", response.StatusCode)
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
