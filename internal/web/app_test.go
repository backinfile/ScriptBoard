package web_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"scriptboard/internal/hostfiles"
	app "scriptboard/internal/web"
)

func TestFirstStartCreatesCredentialAndProtectsFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")

	application, err := app.Open(app.Config{
		StateRoot: stateRoot,
	})
	if err != nil {
		t.Fatalf("open application: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })

	passwordPath := filepath.Join(stateRoot, "secrets", "initial-admin-password")
	password, err := os.ReadFile(passwordPath)
	if err != nil {
		t.Fatalf("read initial password: %v", err)
	}
	if len(strings.TrimSpace(string(password))) < 20 {
		t.Fatalf("initial password is unexpectedly short: %d bytes", len(password))
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	response, err := client.Get(server.URL + "/login")
	if err != nil {
		t.Fatalf("get login: %v", err)
	}
	loginBody, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read login response: %v", err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(string(loginBody), "Sign in") {
		t.Fatalf("unexpected login response: status=%d body=%q", response.StatusCode, loginBody)
	}
	if cacheControl := response.Header.Get("Cache-Control"); cacheControl != "no-store" {
		t.Fatalf("login cache control = %q, want no-store", cacheControl)
	}
	if policy := response.Header.Get("Content-Security-Policy"); !strings.Contains(policy, "form-action 'self'") {
		t.Fatalf("login content security policy does not restrict form targets: %q", policy)
	}
	if policy := response.Header.Get("Permissions-Policy"); policy == "" {
		t.Fatal("login response is missing a permissions policy")
	}
	if strings.Contains(string(loginBody), "autofocus") {
		t.Fatalf("login page uses unconditional autofocus: %s", loginBody)
	}
	if !strings.Contains(string(loginBody), `name="username" value="" autocomplete="username" spellcheck="false"`) {
		t.Fatalf("login username field is missing input metadata: %s", loginBody)
	}
	if !strings.Contains(string(loginBody), `<body class="login-page">`) {
		t.Fatalf("login page styling depends on JavaScript: %s", loginBody)
	}

	response, err = client.Get(server.URL + "/resources/files")
	if err != nil {
		t.Fatalf("get protected files page: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("protected page status = %d, want %d", response.StatusCode, http.StatusSeeOther)
	}
	if location := response.Header.Get("Location"); location != "/login" {
		t.Fatalf("protected page redirect = %q, want /login", location)
	}
}

func TestCredentialOverrideRejectsOversizedPasswordFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	passwordPath := filepath.Join(root, "admin-password")
	if err := os.WriteFile(passwordPath, []byte(strings.Repeat("x", 259)), 0o600); err != nil {
		t.Fatal(err)
	}
	application, err := app.Open(app.Config{
		StateRoot:         filepath.Join(root, "state"),
		AdminPasswordFile: passwordPath,
	})
	if application != nil {
		_ = application.Close()
	}
	if err == nil {
		t.Fatal("oversized administrator password file was accepted")
	}
}

func TestCredentialOverrideRejectsNonRegularPasswordFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	application, err := app.Open(app.Config{
		StateRoot:         filepath.Join(root, "state"),
		AdminPasswordFile: root,
	})
	if application != nil {
		_ = application.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("directory credential error=%q", err)
	}
}

func TestResetAdminCredentialsRejectsInvalidUsername(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	application, err := app.Open(app.Config{
		StateRoot: filepath.Join(root, "state"),
	})
	if err != nil {
		t.Fatalf("open application: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })

	if _, err := application.ResetAdminCredentials("unsafe\nusername"); err == nil {
		t.Fatal("invalid administrator username was accepted")
	}
}

func TestRootRedirectsToLoginWhenUnauthenticated(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	application, err := app.Open(app.Config{
		StateRoot: filepath.Join(root, "state"),
	})
	if err != nil {
		t.Fatalf("open application: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })

	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)
	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	response, err := client.Get(server.URL + "/")
	if err != nil {
		t.Fatalf("get root: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("root status = %d, want %d", response.StatusCode, http.StatusSeeOther)
	}
	if location := response.Header.Get("Location"); location != "/login" {
		t.Fatalf("root redirect = %q, want /login", location)
	}
}

func TestLoginRejectsOversizedRequests(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	application, err := app.Open(app.Config{
		StateRoot: filepath.Join(root, "state"),
	})
	if err != nil {
		t.Fatalf("open application: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)
	client := &http.Client{Jar: jar}

	response, err := client.Get(server.URL + "/login")
	if err != nil {
		t.Fatalf("get login: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read login: %v", err)
	}
	response, err = client.PostForm(server.URL+"/login", url.Values{
		"username":   {"admin"},
		"password":   {strings.Repeat("x", 20<<10)},
		"csrf_token": {formToken(t, body)},
	})
	if err != nil {
		t.Fatalf("post oversized login: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("oversized login status = %d, want %d: %s", response.StatusCode, http.StatusRequestEntityTooLarge, body)
	}
	if got := response.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("oversized login cache control = %q, want no-store", got)
	}
}

func TestLoginAcceptsBrowserMultipartForm(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	passwordPath := filepath.Join(root, "admin-password")
	if err := os.WriteFile(passwordPath, []byte("browser-multipart-password\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	application, err := app.Open(app.Config{
		StateRoot:         filepath.Join(root, "state"),
		AdminUsername:     "admin",
		AdminPasswordFile: passwordPath,
	})
	if err != nil {
		t.Fatalf("open application: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)
	client := &http.Client{Jar: jar}

	response, err := client.Get(server.URL + "/login")
	if err != nil {
		t.Fatalf("get login: %v", err)
	}
	loginBody, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read login: %v", err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, value := range map[string]string{
		"username": "admin", "password": "browser-multipart-password", "csrf_token": formToken(t, loginBody),
	} {
		if err := writer.WriteField(name, value); err != nil {
			t.Fatalf("write multipart field: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart form: %v", err)
	}
	request, err := http.NewRequest(http.MethodPost, server.URL+"/login", &body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Accept", "application/json")
	response, err = client.Do(request)
	if err != nil {
		t.Fatalf("post multipart login: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		responseBody, _ := io.ReadAll(response.Body)
		t.Fatalf("multipart login status = %d: %s", response.StatusCode, responseBody)
	}
	var payload map[string]string
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode multipart login response: %v", err)
	}
	if payload["redirect"] != "/monitor" {
		t.Fatalf("multipart login redirect = %q", payload["redirect"])
	}
}

func TestAuthenticatedRootRedirectsToOverviewAndOverviewDataIsPrivate(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

	response, err := client.Get(serverURL + "/")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/monitor" {
		t.Fatalf("root status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}

	response, err = client.Get(serverURL + "/monitor")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !bytes.Contains(page, []byte("Machine status")) || !bytes.Contains(page, []byte(`data-host-overview`)) || !bytes.Contains(page, []byte(`data-fleet-overview`)) {
		t.Fatalf("overview status=%d body=%s", response.StatusCode, page)
	}
	if !bytes.Contains(page, []byte(`data-fleet-node="local"`)) || !bytes.Contains(page, []byte(`class="fc-ms"`)) || bytes.Contains(page, []byte(`data-host-detail`)) {
		t.Fatalf("fleet overview does not keep details out of the fast path: %s", page)
	}
	if bytes.Contains(page, []byte(`class="button button--primary" href="/resources/files/"`)) {
		t.Fatalf("overview should not promote script execution: %s", page)
	}

	response, err = client.Get(serverURL + "/monitor?tab=details")
	if err != nil {
		t.Fatal(err)
	}
	detailPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !bytes.Contains(detailPage, []byte(`data-overview-tab="details"`)) || !bytes.Contains(detailPage, []byte(`data-host-detail`)) || bytes.Contains(detailPage, []byte(`data-metric-card="cpu"`)) {
		t.Fatalf("overview detail tab status=%d body=%s", response.StatusCode, detailPage)
	}

	response, err = client.Get(serverURL + "/monitor/data?range=1h")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("data status=%d", response.StatusCode)
	}
	if got := response.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache-control=%q", got)
	}
	if got := response.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("content-type=%q", got)
	}

	response, err = client.Get(serverURL + "/monitor/data?range=forever")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid range status=%d", response.StatusCode)
	}
}

func TestLoginPageExposesAJAXEnhancementHooks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	application, err := app.Open(app.Config{
		StateRoot: filepath.Join(root, "state"),
	})
	if err != nil {
		t.Fatalf("open application: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)

	response, err := http.Get(server.URL + "/login")
	if err != nil {
		t.Fatalf("get login: %v", err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read login: %v", err)
	}
	for _, expected := range []string{
		`data-login-form`,
		`data-login-error`,
		`data-login-error-message`,
		`aria-live="polite"`,
	} {
		if !bytes.Contains(page, []byte(expected)) {
			t.Fatalf("login page does not contain %q: %s", expected, page)
		}
	}
}

func TestPrimaryNavigationAvoidsFullPageReloads(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	if err := os.MkdirAll(hostRoot, 0o700); err != nil {
		t.Fatalf("create host root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hostRoot, "preview.png"), []byte("preview"), 0o600); err != nil {
		t.Fatalf("create preview fixture: %v", err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, filepath.Join(root, "state"))

	response, err := client.Get(hostFilesRequestURLWithQuery(serverURL, hostRoot, url.Values{"q": {"preview"}}))
	if err != nil {
		t.Fatalf("get files page: %v", err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read files page: %v", err)
	}
	for _, expected := range []string{
		`data-pjax-nav`, `class="skip-link" href="#main-content"`, `main id="main-content"`,
		`type="search"`, `data-lucide="image"`, `data-local-time`,
	} {
		if !bytes.Contains(page, []byte(expected)) {
			t.Fatalf("files page does not contain %q: %s", expected, page)
		}
	}

	assetPattern := regexp.MustCompile(`/assets/(app\.css|app-v2\.js)\?v=([a-f0-9]{12})`)
	assetMatches := assetPattern.FindAllSubmatch(page, -1)
	if len(assetMatches) != 2 {
		t.Fatalf("files page asset URLs = %q, want one CSS and one JS fingerprint", assetMatches)
	}
	assetBodies := make(map[string][]byte, len(assetMatches))
	assetVersions := make(map[string]string, len(assetMatches))
	for _, match := range assetMatches {
		name := string(match[1])
		version := string(match[2])
		asset := "/assets/" + name + "?v=" + version
		response, err = client.Get(serverURL + asset)
		if err != nil {
			t.Fatalf("get %s: %v", asset, err)
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			t.Fatalf("read %s: %v", asset, readErr)
		}
		if cacheControl := response.Header.Get("Cache-Control"); cacheControl != "public, max-age=31536000, immutable" {
			t.Errorf("%s cache control = %q, want immutable versioned caching", asset, cacheControl)
		}
		assetBodies[name] = body
		assetVersions[name] = version
		if name == "app-v2.js" {
			for _, expected := range []string{"preventDefault()", "DOMParser", "history.pushState", "popstate", "replaceWith", "Intl.DateTimeFormat", "task-panel", "initCopyControls(main, cleanups)", "EventSource", "data-markdown-preview", "data-script-preview", "DOMPurify", "markdownit", "hljs.highlight"} {
				if !bytes.Contains(body, []byte(expected)) {
					t.Errorf("interaction script does not contain %q", expected)
				}
			}
		}
		if name == "app.css" {
			for _, expected := range []string{":focus-visible", "@media (prefers-reduced-motion", ".button--danger", ".empty-state", ".segmented-control", "--accent", ".measurement-ledger", ".markdown-preview", ".hljs-keyword"} {
				if !bytes.Contains(body, []byte(expected)) {
					t.Errorf("stylesheet does not contain %q", expected)
				}
			}
			for _, forbidden := range []string{`form:first-of-type`, `button[name="path"]`} {
				if bytes.Contains(body, []byte(forbidden)) {
					t.Errorf("stylesheet retains fragile selector %q", forbidden)
				}
			}
		}
	}
	version := assetVersions["app-v2.js"]
	vendorAssets := map[string]string{
		"markdown-it.min.js":          "markdown-it 14.3.0",
		"purify.min.js":               "DOMPurify 3.4.12",
		"highlight.min.js":            "Highlight.js v11.11.1",
		"highlight-powershell.min.js": "powershell` grammar compiled for Highlight.js 11.11.1",
		"highlight-dos.min.js":        "dos` grammar compiled for Highlight.js 11.11.1",
	}
	for name, marker := range vendorAssets {
		asset := "/assets/" + name + "?v=" + version
		response, err = client.Get(serverURL + asset)
		if err != nil {
			t.Fatalf("get %s: %v", asset, err)
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			t.Fatalf("read %s: %v", asset, readErr)
		}
		if cacheControl := response.Header.Get("Cache-Control"); cacheControl != "public, max-age=31536000, immutable" {
			t.Errorf("%s cache control = %q, want immutable versioned caching", asset, cacheControl)
		}
		if contentType := response.Header.Get("Content-Type"); !strings.Contains(contentType, "javascript") {
			t.Errorf("%s content type = %q, want JavaScript", asset, contentType)
		}
		if !bytes.Contains(body, []byte(marker)) {
			t.Errorf("%s does not contain version marker %q", asset, marker)
		}
		assetBodies[name] = body
		assetVersions[name] = version
	}
	orderedAssets := []string{
		"app.css",
		"app-v2.js",
		"markdown-it.min.js",
		"purify.min.js",
		"highlight.min.js",
		"highlight-powershell.min.js",
		"highlight-dos.min.js",
	}
	combined := make([]byte, 0)
	for index, name := range orderedAssets {
		if index > 0 {
			combined = append(combined, 0)
		}
		combined = append(combined, assetBodies[name]...)
	}
	digest := sha256.Sum256(combined)
	expectedVersion := hex.EncodeToString(digest[:6])
	for name, version := range assetVersions {
		if version != expectedVersion {
			t.Errorf("%s asset version = %q, want content fingerprint %q", name, version, expectedVersion)
		}
	}

	response, err = client.Get(serverURL + "/favicon.ico")
	if err != nil {
		t.Fatalf("get favicon.ico: %v", err)
	}
	favicon, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil {
		t.Fatalf("read favicon.ico: %v", readErr)
	}
	if contentType := response.Header.Get("Content-Type"); contentType != "image/x-icon" {
		t.Errorf("favicon.ico content type = %q, want image/x-icon", contentType)
	}
	if len(favicon) < 4 || !bytes.Equal(favicon[:4], []byte("\x00\x00\x01\x00")) {
		t.Error("favicon.ico does not contain an ICO signature")
	}
}

func TestEmptyCollectionPagesProvideNextStepWithoutPagination(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	tests := []struct {
		path     string
		expected []string
	}{
		{path: "/config/quick-runs", expected: []string{`class="empty-state"`, "There are no Quick Runs yet", "Browse scripts"}},
		{path: "/config/schedules", expected: []string{`class="empty-state"`, "There are no schedules yet", "Create schedule"}},
		{path: "/history/runs", expected: []string{`class="empty-state"`, "There are no Runs yet", "Run script"}},
	}
	for _, test := range tests {
		response, err := client.Get(serverURL + test.path)
		if err != nil {
			t.Fatalf("get %s: %v", test.path, err)
		}
		page, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			t.Fatalf("read %s: %v", test.path, readErr)
		}
		html := string(page)
		for _, expected := range test.expected {
			if !strings.Contains(html, expected) {
				t.Errorf("%s does not contain %q: %s", test.path, expected, html)
			}
		}
		if strings.Contains(html, `class="pagination"`) {
			t.Errorf("%s shows pagination for an empty collection: %s", test.path, html)
		}
	}
}

func TestFileWorkspaceExposesNavigationPreviewAndRunConfiguration(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	if err := os.MkdirAll(filepath.Join(hostRoot, "scripts"), 0o700); err != nil {
		t.Fatalf("create scripts directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hostRoot, "scripts", "demo.ps1"), []byte("Write-Output 'preview me'\n"), 0o600); err != nil {
		t.Fatalf("write preview fixture: %v", err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, filepath.Join(root, "state"))

	response, err := client.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatalf("get root files page: %v", err)
	}
	rootPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read root files page: %v", err)
	}
	rootHTML := string(rootPage)
	for _, expected := range []string{
		`data-lucide="folder"`, `>scripts</a>`, `name="sort"`, `name="direction"`,
	} {
		if !strings.Contains(rootHTML, expected) {
			t.Fatalf("root files page does not contain %q: %s", expected, rootHTML)
		}
	}
	for _, forbidden := range []string{`<th>类型</th>`, `<th>版本保护</th>`, `action="/resources/files/move"`} {
		if strings.Contains(rootHTML, forbidden) {
			t.Fatalf("root files page unexpectedly contains %q: %s", forbidden, rootHTML)
		}
	}

	scriptsDirectory := filepath.Join(hostRoot, "scripts")
	scriptPath := filepath.Join(scriptsDirectory, "demo.ps1")
	response, err = client.Get(hostFilesRequestURL(serverURL, scriptsDirectory))
	if err != nil {
		t.Fatalf("get nested files page: %v", err)
	}
	nestedPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read nested files page: %v", err)
	}
	nestedHTML := string(nestedPage)
	for _, expected := range []string{
		`href="` + hostFilesHrefWithQuery(hostRoot, nil) + `"`, `href="` + hostFileHref("/resources/files/view", scriptPath) + `"`,
		`href="` + hostFileHref("/resources/files/run", scriptPath) + `"`, `data-lucide="file-terminal"`,
	} {
		if !strings.Contains(nestedHTML, expected) {
			t.Fatalf("nested files page does not contain %q: %s", expected, nestedHTML)
		}
	}

	response, err = client.Get(hostFileRequestURL(serverURL, "/resources/files/view", scriptPath))
	if err != nil {
		t.Fatalf("get text preview: %v", err)
	}
	previewPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read text preview: %v", err)
	}
	for _, expected := range []string{
		"Script preview",
		"Write-Output &#39;preview me&#39;",
		`href="` + hostFilesHrefWithQuery(scriptsDirectory, nil) + `"`,
		`data-script-preview`,
		`data-highlight-language="powershell"`,
	} {
		if !strings.Contains(string(previewPage), expected) {
			t.Fatalf("text preview does not contain %q: %s", expected, previewPage)
		}
	}
}

func TestFileWorkspaceOffersPreviewForUnknownTextButNotUnknownBinary(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	if err := os.MkdirAll(hostRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	textPath := filepath.Join(hostRoot, "notes.payload")
	binaryPath := filepath.Join(hostRoot, "archive.payload")
	misleadingTextPath := filepath.Join(hostRoot, "renamed.txt")
	if err := os.WriteFile(textPath, []byte("unknown extension, readable content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binaryPath, []byte("%PDF-1.7\nvalid ASCII but binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(misleadingTextPath, []byte("%PDF-1.7\nvalid ASCII but binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, filepath.Join(root, "state"))

	response, err := client.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatal(err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	html := string(page)
	textPreview := `href="` + hostFileHref("/resources/files/view", textPath) + `"`
	binaryPreview := `href="` + hostFileHref("/resources/files/view", binaryPath) + `"`
	misleadingTextPreview := `href="` + hostFileHref("/resources/files/view", misleadingTextPath) + `"`
	if !strings.Contains(html, textPreview) {
		t.Fatalf("unknown text is missing preview link %q: %s", textPreview, html)
	}
	if strings.Contains(html, binaryPreview) {
		t.Fatalf("unknown binary unexpectedly has preview link %q: %s", binaryPreview, html)
	}
	if strings.Contains(html, misleadingTextPreview) {
		t.Fatalf("binary content with a text extension unexpectedly has preview link %q: %s", misleadingTextPreview, html)
	}
	if strings.Contains(html, `href="`+hostFileHref("/resources/files/edit", textPath)+`"`) {
		t.Fatalf("content-detected text unexpectedly has an edit link: %s", html)
	}
	for _, expected := range []string{
		`>notes.payload</a><small class="file-meta"><span class="file-type-badge">Previewable text</span>`,
		`>archive.payload</span><small class="file-meta"><span class="file-type-badge">Other file</span>`,
		`>renamed.txt</span><small class="file-meta"><span class="file-type-badge">Other file</span>`,
	} {
		if !strings.Contains(html, expected) {
			t.Errorf("content-based preview state is missing %q: %s", expected, html)
		}
	}

	response, err = client.Get(hostFilesRequestURLWithQuery(serverURL, hostRoot, url.Values{"sort": {"type"}}))
	if err != nil {
		t.Fatal(err)
	}
	typeSortedPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	typeSortedHTML := string(typeSortedPage)
	textPosition := strings.Index(typeSortedHTML, ">notes.payload</a>")
	binaryPosition := strings.Index(typeSortedHTML, ">archive.payload</span>")
	if textPosition < 0 || binaryPosition < 0 || textPosition > binaryPosition {
		t.Fatalf("content-based type sort did not place previewable text before other files: %s", typeSortedHTML)
	}

	response, err = client.Get(hostFileRequestURL(serverURL, "/resources/files/view", textPath))
	if err != nil {
		t.Fatal(err)
	}
	preview, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !bytes.Contains(preview, []byte("unknown extension, readable content")) {
		t.Fatalf("unknown text preview status=%d body=%s", response.StatusCode, preview)
	}
}

func TestLargeTextPreviewLoadsInBoundedChunks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	if err := os.MkdirAll(hostRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(hostRoot, "large.txt")
	content := strings.Repeat("0123456789abcdef\n", 70000) + "TAIL_MARKER\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, filepath.Join(root, "state"))

	response, err := client.Get(hostFileRequestURL(serverURL, "/resources/files/view", path))
	if err != nil {
		t.Fatal(err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !bytes.Contains(page, []byte(`data-text-preview-pager`)) || bytes.Contains(page, []byte("TAIL_MARKER")) {
		t.Fatalf("large preview must render only its first bounded chunk: status=%d bytes=%d", response.StatusCode, len(page))
	}
	versionMatch := regexp.MustCompile(`data-text-preview-version="([a-f0-9]+)"`).FindSubmatch(page)
	nextMatch := regexp.MustCompile(`data-text-preview-next="([0-9]+)"`).FindSubmatch(page)
	if len(versionMatch) != 2 || len(nextMatch) != 2 {
		t.Fatalf("large preview is missing its paging cursor: %s", page)
	}

	query := url.Values{"path": {path}, "offset": {string(nextMatch[1])}, "version": {string(versionMatch[1])}}
	response, err = client.Get(serverURL + "/resources/files/view/content?" + query.Encode())
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var chunk struct {
		Content    string `json:"content"`
		NextOffset string `json:"nextOffset"`
		HasMore    bool   `json:"hasMore"`
	}
	if err := json.NewDecoder(response.Body).Decode(&chunk); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || len(chunk.Content) == 0 || len(chunk.Content) > 16<<10 || !chunk.HasMore || chunk.NextOffset == "" {
		t.Fatalf("text preview chunk = status %d, %#v", response.StatusCode, chunk)
	}
}

func TestScriptPreviewUsesDeterministicHighlightLanguages(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	tests := map[string]string{
		"health.ps1": "powershell",
		"health.cmd": "dos",
		"health.bat": "dos",
		"health.sh":  "bash",
		"health.py":  "python",
		"health.txt": "",
	}
	if err := os.MkdirAll(hostRoot, 0o700); err != nil {
		t.Fatalf("create host root: %v", err)
	}
	for name := range tests {
		if err := os.WriteFile(filepath.Join(hostRoot, name), []byte("if ready\n"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	client, serverURL := authenticatedClient(t, hostRoot, filepath.Join(root, "state"))

	for name, expected := range tests {
		response, err := client.Get(hostFileRequestURL(serverURL, "/resources/files/view", filepath.Join(hostRoot, name)))
		if err != nil {
			t.Fatalf("get %s preview: %v", name, err)
		}
		page, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			t.Fatalf("read %s preview: %v", name, readErr)
		}
		html := string(page)
		if expected == "" {
			if strings.Contains(html, "data-script-preview") {
				t.Errorf("%s unexpectedly enables script highlighting: %s", name, html)
			}
			continue
		}
		for _, marker := range []string{`data-script-preview`, `data-highlight-language="` + expected + `"`} {
			if !strings.Contains(html, marker) {
				t.Errorf("%s preview does not contain %q: %s", name, marker, html)
			}
		}
	}
}

func TestMarkdownPreviewProgressivelyEnhancesEscapedSource(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	if err := os.MkdirAll(filepath.Join(hostRoot, "docs"), 0o700); err != nil {
		t.Fatalf("create docs directory: %v", err)
	}
	content := "# Runbook\n\n[Sibling](other.md)\n\n![Diagram](diagram.png)\n\n```powershell\nif ($Ready) { Write-Output 'ready' }\n```\n\n</pre><script>alert('xss')</script>\n"
	if err := os.WriteFile(filepath.Join(hostRoot, "docs", "runbook.md"), []byte(content), 0o600); err != nil {
		t.Fatalf("write markdown fixture: %v", err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, filepath.Join(root, "state"))

	response, err := client.Get(hostFileRequestURL(serverURL, "/resources/files/view", filepath.Join(hostRoot, "docs", "runbook.md")))
	if err != nil {
		t.Fatalf("get markdown preview: %v", err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read markdown preview: %v", err)
	}
	html := string(page)
	for _, expected := range []string{
		`data-markdown-preview`,
		`data-markdown-source`,
		`data-markdown-base="` + filepath.Join(hostRoot, "docs") + `"`,
		`&lt;/pre&gt;&lt;script&gt;alert(&#39;xss&#39;)&lt;/script&gt;`,
		"```powershell",
		`# Runbook`,
	} {
		if !strings.Contains(html, expected) {
			t.Errorf("markdown preview does not contain %q: %s", expected, html)
		}
	}
	if strings.Contains(html, `<script>alert('xss')</script>`) {
		t.Fatalf("markdown source escaped its preview container: %s", html)
	}
}

func TestAccountCredentialsStayReadOnlyUntilTheirTaskPanelsOpen(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	response, err := client.Get(serverURL + "/settings/account")
	if err != nil {
		t.Fatalf("get account settings: %v", err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read account settings: %v", err)
	}
	html := string(page)
	for _, expected := range []string{
		`class="settings-nav"`,
		`class="settings-summary"`,
		`href="/settings/account/username" data-task-link`,
		`href="/settings/account/password" data-task-link`,
		`action="/logout"`,
	} {
		if !strings.Contains(html, expected) {
			t.Fatalf("account settings does not contain %q: %s", expected, html)
		}
	}
	for _, forbidden := range []string{`name="current_password"`, `name="new_password"`, `name="confirm_password"`, `autocomplete="username"`} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("account settings exposes editable credential field %q: %s", forbidden, html)
		}
	}

	for _, task := range []struct {
		path     string
		kind     string
		expected []string
	}{
		{"/settings/account/username", "account-username", []string{`name="username"`, `name="current_password"`}},
		{"/settings/account/password", "account-password", []string{`name="current_password"`, `name="new_password"`, `name="confirm_password"`}},
	} {
		response, err = client.Get(serverURL + task.path)
		if err != nil {
			t.Fatalf("get %s task: %v", task.kind, err)
		}
		taskBody, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			t.Fatalf("read %s task: %v", task.kind, readErr)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s task status=%d, body=%s", task.kind, response.StatusCode, taskBody)
		}
		taskHTML := string(taskBody)
		for _, expected := range append([]string{`data-task-kind="` + task.kind + `"`}, task.expected...) {
			if !strings.Contains(taskHTML, expected) {
				t.Fatalf("%s task does not contain %q: %s", task.kind, expected, taskHTML)
			}
		}
	}
}

func TestInstanceNameSettingsUpdateTheApplicationShell(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	response, err := client.Get(serverURL + "/settings/name")
	if err != nil {
		t.Fatalf("get site name settings: %v", err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read site name settings: %v", err)
	}
	for _, expected := range []string{
		`href="/settings/name" aria-current="page"`,
		`action="/settings/name"`,
		`name="display_name" value="ScriptBoard"`,
		`maxlength="32"`,
	} {
		if !strings.Contains(string(page), expected) {
			t.Fatalf("site name settings are missing %q: %s", expected, page)
		}
	}

	response, err = client.PostForm(serverURL+"/settings/name", url.Values{
		"csrf_token":   {formToken(t, page)},
		"display_name": {"North Host"},
	})
	if err != nil {
		t.Fatalf("update site name: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/settings/name?saved=1" {
		t.Fatalf("update site name status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}

	response, err = client.Get(serverURL + "/monitor")
	if err != nil {
		t.Fatalf("get shell with custom site name: %v", err)
	}
	shell, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read shell with custom site name: %v", err)
	}
	for _, expected := range []string{
		`aria-label="North Host dev"`,
		`<span class="brand-name brand-name--full">North Host</span>`,
	} {
		if !strings.Contains(string(shell), expected) {
			t.Fatalf("custom site name is missing %q: %s", expected, shell)
		}
	}
}

func TestInstanceNameSettingsRejectMissingCSRF(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	response, err := client.PostForm(serverURL+"/settings/name", url.Values{
		"display_name": {"Forged Name"},
	})
	if err != nil {
		t.Fatalf("submit site name without CSRF: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("missing CSRF status=%d, want %d", response.StatusCode, http.StatusForbidden)
	}

	response, err = client.Get(serverURL + "/settings/name")
	if err != nil {
		t.Fatalf("get site name settings after rejected update: %v", err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read site name settings after rejected update: %v", err)
	}
	if strings.Contains(string(page), "Forged Name") || !strings.Contains(string(page), `name="display_name" value="ScriptBoard"`) {
		t.Fatalf("rejected update changed the site name: %s", page)
	}
}

func TestInstanceNameSettingsValidateAndRestoreTheDefault(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	response, err := client.Get(serverURL + "/settings/name")
	if err != nil {
		t.Fatal(err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}

	response, err = client.PostForm(serverURL+"/settings/name", url.Values{
		"csrf_token":   {formToken(t, page)},
		"display_name": {strings.Repeat("界", 33)},
	})
	if err != nil {
		t.Fatalf("submit overlong site name: %v", err)
	}
	invalid, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(invalid), `aria-invalid="true"`) {
		t.Fatalf("overlong site name status=%d body=%s", response.StatusCode, invalid)
	}

	response, err = client.PostForm(serverURL+"/settings/name", url.Values{
		"csrf_token":   {formToken(t, invalid)},
		"display_name": {""},
	})
	if err != nil {
		t.Fatalf("restore default site name: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("restore default site name status=%d", response.StatusCode)
	}

	response, err = client.Get(serverURL + "/monitor")
	if err != nil {
		t.Fatal(err)
	}
	shell, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(shell), `<span class="brand-name brand-name--full">ScriptBoard</span>`) {
		t.Fatalf("default site name was not restored: %s", shell)
	}
}

func TestUpdateSourcesRenderInRightmostSettingsDrawer(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	client, serverURL := authenticatedClientWithConfig(t, app.Config{StateRoot: filepath.Join(root, "state")})
	response, err := client.Get(serverURL + "/settings/updates?sources=1")
	if err != nil {
		t.Fatalf("get update sources: %v", err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read update sources: %v", err)
	}
	html := string(page)
	for _, expected := range []string{
		`data-update-source-open`, `data-update-source-drawer`, `update-source-drawer-host is-open`,
		`name="source_id" value="github"`, `name="source_id" value="gh-proxy"`,
		`name="source_id" value="ghproxy-net"`, `gh-proxy.com`, `ghproxy.net`,
	} {
		if !strings.Contains(html, expected) {
			t.Fatalf("update source drawer does not contain %q: %s", expected, html)
		}
	}
	displayIndex := strings.Index(html, `href="/settings/display"`)
	updateIndex := strings.Index(html, `href="/settings/updates"`)
	if displayIndex < 0 || updateIndex < displayIndex {
		t.Fatalf("updates tab is not after display settings: display=%d updates=%d", displayIndex, updateIndex)
	}
}

func TestManagedServiceCanBeRestartedFromUpdatesPage(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	restarted := make(chan struct{}, 1)
	client, serverURL := authenticatedClientWithConfig(t, app.Config{
		StateRoot: filepath.Join(root, "state"),
		RequestRestart: func() error {
			restarted <- struct{}{}
			return nil
		},
	})
	response, err := client.Get(serverURL + "/settings/updates")
	if err != nil {
		t.Fatalf("get updates page: %v", err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read updates page: %v", err)
	}
	html := string(page)
	for _, expected := range []string{`action="/settings/updates/restart"`, `data-service-restart`, `data-lucide="rotate-cw"`} {
		if !strings.Contains(html, expected) {
			t.Fatalf("updates page does not contain restart control %q: %s", expected, html)
		}
	}

	values := url.Values{"csrf_token": {formToken(t, page)}, "confirm": {"yes"}}
	request, err := http.NewRequest(http.MethodPost, serverURL+"/settings/updates/restart", strings.NewReader(values.Encode()))
	if err != nil {
		t.Fatalf("create restart request: %v", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err = client.Do(request)
	if err != nil {
		t.Fatalf("request service restart: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read restart response: %v", err)
	}
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("restart status=%d body=%s, want 202", response.StatusCode, body)
	}
	var payload map[string]string
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode restart response: %v", err)
	}
	if payload["instance_id"] == "" || payload["status_url"] != "/settings/updates/status" {
		t.Fatalf("restart payload=%v", payload)
	}
	select {
	case <-restarted:
	default:
		t.Fatal("restart callback was not called")
	}

	request, err = http.NewRequest(http.MethodPost, serverURL+"/settings/updates/restart", strings.NewReader(values.Encode()))
	if err != nil {
		t.Fatalf("create duplicate restart request: %v", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err = client.Do(request)
	if err != nil {
		t.Fatalf("request duplicate service restart: %v", err)
	}
	duplicateBody, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read duplicate restart response: %v", err)
	}
	if response.StatusCode != http.StatusConflict || !strings.Contains(string(duplicateBody), "already pending") {
		t.Fatalf("duplicate restart status=%d body=%s, want 409 pending", response.StatusCode, duplicateBody)
	}
}

func TestUpdatesPageHidesRestartWhenServiceControlIsUnavailable(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	client, serverURL := authenticatedClientWithConfig(t, app.Config{StateRoot: filepath.Join(root, "state")})
	response, err := client.Get(serverURL + "/settings/updates")
	if err != nil {
		t.Fatalf("get updates page: %v", err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read updates page: %v", err)
	}
	if strings.Contains(string(page), `action="/settings/updates/restart"`) {
		t.Fatalf("updates page exposed restart without service control: %s", page)
	}
}

func TestAdministratorRenamesAccountFromFocusedTask(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	response, err := client.Get(serverURL + "/settings/account/username")
	if err != nil {
		t.Fatalf("get username task: %v", err)
	}
	taskPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read username task: %v", err)
	}

	const password = "用于自动测试的专用安全密码凭据版本"
	response, err = client.PostForm(serverURL+"/settings/account/username", url.Values{
		"csrf_token":       {formToken(t, taskPage)},
		"username":         {"renamed-admin"},
		"current_password": {password},
	})
	if err != nil {
		t.Fatalf("rename administrator: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/login" {
		t.Fatalf("rename status=%d location=%q, want 303 /login", response.StatusCode, response.Header.Get("Location"))
	}

	loginAs(t, client, serverURL, "admin", password, http.StatusUnauthorized)
	loginAs(t, client, serverURL, "renamed-admin", password, http.StatusSeeOther)
}

func TestInvalidLoginRendersInlineErrorPage(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	application, err := app.Open(app.Config{
		StateRoot: filepath.Join(root, "state"),
	})
	if err != nil {
		t.Fatalf("open application: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)
	client := &http.Client{Jar: jar}

	response, err := client.Get(server.URL + "/login")
	if err != nil {
		t.Fatalf("get login: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read login: %v", err)
	}

	response, err = client.PostForm(server.URL+"/login", url.Values{
		"username":   {"admin"},
		"password":   {"wrong-password"},
		"csrf_token": {formToken(t, body)},
	})
	if err != nil {
		t.Fatalf("post login: %v", err)
	}
	defer response.Body.Close()
	body, err = io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read invalid login response: %v", err)
	}

	page := string(body)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid login status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}
	if contentType := response.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/html") {
		t.Fatalf("invalid login content type = %q, want HTML", contentType)
	}
	for _, expected := range []string{"<!doctype html>", "The username or password is incorrect", `role="alert"`, `value="admin"`, `action="/login"`} {
		if !strings.Contains(page, expected) {
			t.Fatalf("invalid login page does not contain %q: %s", expected, page)
		}
	}
}

func TestInvalidAJAXLoginReturnsStructuredErrorAndFreshCSRFToken(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	application, err := app.Open(app.Config{
		StateRoot: filepath.Join(root, "state"),
	})
	if err != nil {
		t.Fatalf("open application: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)
	client := &http.Client{Jar: jar}

	response, err := client.Get(server.URL + "/login")
	if err != nil {
		t.Fatalf("get login: %v", err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read login: %v", err)
	}

	form := url.Values{
		"username":   {"admin"},
		"password":   {"wrong-password"},
		"csrf_token": {formToken(t, page)},
	}
	request, err := http.NewRequest(http.MethodPost, server.URL+"/login", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("create AJAX login request: %v", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err = client.Do(request)
	if err != nil {
		t.Fatalf("post AJAX login: %v", err)
	}
	defer response.Body.Close()

	var payload struct {
		Error     string `json:"error"`
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode AJAX login response: %v", err)
	}
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("AJAX login status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}
	if contentType := response.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("AJAX login content type = %q, want JSON", contentType)
	}
	if payload.Error != "The username or password is incorrect" {
		t.Fatalf("AJAX login error = %q", payload.Error)
	}
	if payload.CSRFToken == "" || payload.CSRFToken == form.Get("csrf_token") {
		t.Fatalf("AJAX login did not return a fresh CSRF token")
	}
}

func TestAJAXLoginReturnsServerSelectedRedirect(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	application, err := app.Open(app.Config{
		StateRoot: stateRoot,
	})
	if err != nil {
		t.Fatalf("open application: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })
	password, err := os.ReadFile(filepath.Join(stateRoot, "secrets", "initial-admin-password"))
	if err != nil {
		t.Fatalf("read initial password: %v", err)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)
	client := &http.Client{Jar: jar}

	response, err := client.Get(server.URL + "/login")
	if err != nil {
		t.Fatalf("get login: %v", err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read login: %v", err)
	}

	form := url.Values{
		"username":   {"admin"},
		"password":   {strings.TrimSpace(string(password))},
		"csrf_token": {formToken(t, page)},
	}
	request, err := http.NewRequest(http.MethodPost, server.URL+"/login", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("create AJAX login request: %v", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err = client.Do(request)
	if err != nil {
		t.Fatalf("post AJAX login: %v", err)
	}
	defer response.Body.Close()

	var payload struct {
		Redirect string `json:"redirect"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode AJAX login response: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("AJAX login status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if payload.Redirect != "/monitor" {
		t.Fatalf("AJAX login redirect = %q, want /monitor", payload.Redirect)
	}

	response, err = client.Get(server.URL + payload.Redirect)
	if err != nil {
		t.Fatalf("follow AJAX login redirect: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("authenticated redirect status = %d, want %d", response.StatusCode, http.StatusOK)
	}
}

func TestLoginFormRemainsValidWhenLoginPageIsLoadedAgain(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	application, err := app.Open(app.Config{
		StateRoot: stateRoot,
	})
	if err != nil {
		t.Fatalf("open application: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })
	password, err := os.ReadFile(filepath.Join(stateRoot, "secrets", "initial-admin-password"))
	if err != nil {
		t.Fatalf("read initial password: %v", err)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	response, err := client.Get(server.URL + "/login")
	if err != nil {
		t.Fatalf("get first login page: %v", err)
	}
	firstPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read first login page: %v", err)
	}

	response, err = client.Get(server.URL + "/login")
	if err != nil {
		t.Fatalf("get second login page: %v", err)
	}
	_ = response.Body.Close()

	response, err = client.PostForm(server.URL+"/login", url.Values{
		"username":   {"admin"},
		"password":   {strings.TrimSpace(string(password))},
		"csrf_token": {formToken(t, firstPage)},
	})
	if err != nil {
		t.Fatalf("post first login form: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read login response: %v", err)
	}
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("first login status = %d, want %d; body=%q", response.StatusCode, http.StatusSeeOther, body)
	}
	if location := response.Header.Get("Location"); location != "/monitor" {
		t.Fatalf("first login redirect = %q, want /monitor", location)
	}
}

func TestLoginRateLimitCannotBeBypassedByChangingUsername(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	application, err := app.Open(app.Config{
		StateRoot: filepath.Join(root, "state"),
	})
	if err != nil {
		t.Fatalf("open application: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)
	client := &http.Client{Jar: jar}

	for attempt := 1; attempt <= 6; attempt++ {
		response := invalidLoginAttempt(t, client, server.URL, "unknown-"+strconv.Itoa(attempt), "")
		want := http.StatusUnauthorized
		if attempt == 6 {
			want = http.StatusTooManyRequests
		}
		if response.StatusCode != want {
			t.Fatalf("attempt %d status = %d, want %d", attempt, response.StatusCode, want)
		}
		if attempt == 6 && response.Header.Get("Retry-After") != "2" {
			t.Fatalf("first retry delay = %q, want 2 seconds", response.Header.Get("Retry-After"))
		}
		_ = response.Body.Close()
	}
}

func TestLoginRateLimitCannotBeBypassedByChangingSourceAddress(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	application, err := app.Open(app.Config{
		StateRoot:      filepath.Join(root, "state"),
		TrustedProxies: []string{"127.0.0.1"},
	})
	if err != nil {
		t.Fatalf("open application: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)
	client := &http.Client{Jar: jar}

	for attempt := 1; attempt <= 6; attempt++ {
		response := invalidLoginAttempt(t, client, server.URL, "admin", "203.0.113."+strconv.Itoa(attempt))
		want := http.StatusUnauthorized
		if attempt == 6 {
			want = http.StatusTooManyRequests
		}
		if response.StatusCode != want {
			t.Fatalf("attempt %d status = %d, want %d", attempt, response.StatusCode, want)
		}
		_ = response.Body.Close()
	}
}

func TestRateLimitedLoginIsRecordedInAudit(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	authenticated, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	attacker := &http.Client{Jar: jar}
	for attempt := 1; attempt <= 6; attempt++ {
		response := invalidLoginAttempt(t, attacker, serverURL, "unknown-"+strconv.Itoa(attempt), "")
		_ = response.Body.Close()
	}

	response, err := authenticated.Get(serverURL + "/history/audit")
	if err != nil {
		t.Fatalf("get audit page: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read audit page: %v", err)
	}
	if !strings.Contains(string(body), "rate_limited") {
		t.Fatalf("audit page does not contain rate-limited login: %s", body)
	}
}

func TestProtectedErrorsRenderInsideTheApplicationShell(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	response, err := client.Get(serverURL + "/settings/account")
	if err != nil {
		t.Fatalf("get account settings: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read account settings: %v", err)
	}

	response, err = client.PostForm(serverURL+"/settings/account", url.Values{
		"username":         {"admin"},
		"current_password": {"wrong-password"},
		"new_password":     {"这是另一个足够长的安全密码"},
		"confirm_password": {"这是另一个足够长的安全密码"},
		"csrf_token":       {formToken(t, body)},
	})
	if err != nil {
		t.Fatalf("post invalid account change: %v", err)
	}
	body, err = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read account error: %v", err)
	}
	page := string(body)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("account error status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}
	if contentType := response.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/html") {
		t.Fatalf("account error content type = %q, want HTML", contentType)
	}
	for _, expected := range []string{
		`class="app-sidebar"`, `aria-label="Primary navigation"`, `href="/settings/account"`,
		`role="alert"`, `error-page`, "Return to workspace", `data-environment="Local"`,
	} {
		if !strings.Contains(page, expected) {
			t.Fatalf("account error page does not contain %q: %s", expected, page)
		}
	}
	for _, removed := range []string{`class="sidebar-account"`, `action="/logout"`, `<span>admin</span>`} {
		if strings.Contains(page, removed) {
			t.Fatalf("account error page still contains removed sidebar account UI %q: %s", removed, page)
		}
	}
	if strings.Contains(page, "LOCAL / READY") {
		t.Fatalf("account error page contains the former placeholder status: %s", page)
	}
}

func TestApplicationShellMarksTrustedProxyRequestsAsRemote(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClientWithConfig(t, app.Config{
		StateRoot:      filepath.Join(root, "state"),
		TrustedProxies: []string{"127.0.0.1"},
	})
	request, err := http.NewRequest(http.MethodGet, serverURL+"/resources/files", nil)
	if err != nil {
		t.Fatalf("create files request: %v", err)
	}
	request.Header.Set("X-Forwarded-For", "203.0.113.42")
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("get files through trusted proxy: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read files page: %v", err)
	}
	if !strings.Contains(string(body), `data-environment="Remote"`) {
		t.Fatalf("files page does not identify remote management: %s", body)
	}
}

func TestSecondInstanceUsingSameStateRootIsRejected(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	config := app.Config{StateRoot: filepath.Join(root, "state")}
	first, err := app.Open(config)
	if err != nil {
		t.Fatalf("open first instance: %v", err)
	}
	t.Cleanup(func() { _ = first.Close() })
	second, err := app.Open(config)
	if err == nil {
		_ = second.Close()
		t.Fatal("second instance unexpectedly opened the same State Root")
	}
	if !strings.Contains(err.Error(), "另一个 ScriptBoard 实例") {
		t.Fatalf("second instance error = %q", err)
	}
}

func TestInitialPasswordLoginCanAccessApplication(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	application, err := app.Open(app.Config{
		StateRoot: stateRoot,
	})
	if err != nil {
		t.Fatalf("open application: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })

	passwordBytes, err := os.ReadFile(filepath.Join(stateRoot, "secrets", "initial-admin-password"))
	if err != nil {
		t.Fatalf("read initial password: %v", err)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	response, err := client.Get(server.URL + "/login")
	if err != nil {
		t.Fatalf("get login: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read login: %v", err)
	}
	csrf := formToken(t, body)

	response, err = client.PostForm(server.URL+"/login", url.Values{
		"username":   {"admin"},
		"password":   {strings.TrimSpace(string(passwordBytes))},
		"csrf_token": {csrf},
	})
	if err != nil {
		t.Fatalf("post login: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("login status = %d, want %d", response.StatusCode, http.StatusSeeOther)
	}
	if location := response.Header.Get("Location"); location != "/monitor" {
		t.Fatalf("login redirect = %q, want /monitor", location)
	}

	response, err = client.Get(server.URL + "/login")
	if err != nil {
		t.Fatalf("get login while authenticated: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/monitor" {
		t.Fatalf("authenticated login page response: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}

	response, err = client.Get(server.URL + "/resources/files")
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("authenticated files status = %d, want %d", response.StatusCode, http.StatusOK)
	}
}

func TestFirstPasswordChangeRevokesSessionAndRemovesCredentialFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	application, err := app.Open(app.Config{
		StateRoot: stateRoot,
	})
	if err != nil {
		t.Fatalf("open application: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })

	passwordPath := filepath.Join(stateRoot, "secrets", "initial-admin-password")
	passwordBytes, err := os.ReadFile(passwordPath)
	if err != nil {
		t.Fatalf("read initial password: %v", err)
	}
	initialPassword := strings.TrimSpace(string(passwordBytes))
	const newPassword = "这是一个新的安全密码短语凭据版本"

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	login(t, client, server.URL, initialPassword, http.StatusSeeOther)

	response, err := client.Get(server.URL + "/settings/account")
	if err != nil {
		t.Fatalf("get account settings: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read account settings: %v", err)
	}
	csrf := formToken(t, body)

	response, err = client.PostForm(server.URL+"/settings/account", url.Values{
		"current_password": {initialPassword},
		"new_password":     {newPassword},
		"confirm_password": {newPassword},
		"csrf_token":       {csrf},
	})
	if err != nil {
		t.Fatalf("post password change: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/login" {
		t.Fatalf("password change response: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	if _, err := os.Stat(passwordPath); !os.IsNotExist(err) {
		t.Fatalf("initial password file still exists: %v", err)
	}

	response, err = client.Get(server.URL + "/resources/files")
	if err != nil {
		t.Fatalf("get files with revoked session: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/login" {
		t.Fatalf("revoked session response: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}

	login(t, client, server.URL, initialPassword, http.StatusUnauthorized)
	response = login(t, client, server.URL, newPassword, http.StatusSeeOther)
	if response.Header.Get("Location") != "/monitor" {
		t.Fatalf("new password login redirect = %q, want /monitor", response.Header.Get("Location"))
	}
}

func login(t *testing.T, client *http.Client, serverURL, password string, wantStatus int) *http.Response {
	t.Helper()
	response, err := client.Get(serverURL + "/login")
	if err != nil {
		t.Fatalf("get login: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read login: %v", err)
	}
	response, err = client.PostForm(serverURL+"/login", url.Values{
		"username":   {"admin"},
		"password":   {password},
		"csrf_token": {formToken(t, body)},
	})
	if err != nil {
		t.Fatalf("post login: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != wantStatus {
		t.Fatalf("login status = %d, want %d", response.StatusCode, wantStatus)
	}
	return response
}

func authenticatedClient(t *testing.T, hostRoot, stateRoot string) (*http.Client, string) {
	t.Helper()
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatalf("create test host root: %v", err)
	}
	return authenticatedClientWithConfig(t, app.Config{StateRoot: stateRoot, FileTopology: testHostTopology{root: hostRoot}, CustomDashboardClient: &http.Client{}})
}

type testHostTopology struct{ root string }

func (topology testHostTopology) Roots() ([]hostfiles.Entry, error) {
	return []hostfiles.Entry{{Name: filepath.Base(topology.root), Path: topology.root, Kind: hostfiles.Directory}}, nil
}

func (topology testHostTopology) FilesystemRoot(string) (string, error) { return topology.root, nil }

func (testHostTopology) Restricted(string) bool { return false }

func hostFileRequestURL(serverURL, endpoint, path string) string {
	values := url.Values{}
	if path != "" {
		values.Set("path", path)
	}
	if len(values) == 0 {
		return serverURL + endpoint
	}
	return serverURL + endpoint + "?" + values.Encode()
}

func hostFilesRequestURL(serverURL, path string) string {
	return hostFileRequestURL(serverURL, "/resources/files", path)
}

func hostFilesRequestURLWithQuery(serverURL, path string, values url.Values) string {
	copy := url.Values{}
	for key, items := range values {
		copy[key] = append([]string(nil), items...)
	}
	if path != "" {
		copy.Set("path", path)
	}
	if len(copy) == 0 {
		return serverURL + "/resources/files"
	}
	return serverURL + "/resources/files?" + copy.Encode()
}

func hostFileHref(endpoint, path string) string {
	return hostFileRequestURL("", endpoint, path)
}

func hostFilesHrefWithQuery(path string, values url.Values) string {
	return strings.TrimPrefix(hostFilesRequestURLWithQuery("", path, values), "")
}

func authenticatedClientWithConfig(t *testing.T, config app.Config) (*http.Client, string) {
	t.Helper()
	application, err := app.Open(config)
	if err != nil {
		t.Fatalf("open application: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	passwordBytes, err := os.ReadFile(filepath.Join(config.StateRoot, "secrets", "initial-admin-password"))
	if err != nil {
		t.Fatalf("read initial password: %v", err)
	}
	initialPassword := strings.TrimSpace(string(passwordBytes))
	login(t, client, server.URL, initialPassword, http.StatusSeeOther)

	response, err := client.Get(server.URL + "/settings/account")
	if err != nil {
		t.Fatalf("get account settings: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read account settings: %v", err)
	}
	const password = "用于自动测试的专用安全密码凭据版本"
	response, err = client.PostForm(server.URL+"/settings/account", url.Values{
		"current_password": {initialPassword},
		"new_password":     {password},
		"confirm_password": {password},
		"csrf_token":       {formToken(t, body)},
	})
	if err != nil {
		t.Fatalf("change password: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("change password status = %d", response.StatusCode)
	}
	login(t, client, server.URL, password, http.StatusSeeOther)
	return client, server.URL
}

func formToken(t *testing.T, body []byte) string {
	t.Helper()
	match := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("csrf token not found in response: %q", body)
	}
	return string(match[1])
}

func invalidLoginAttempt(t *testing.T, client *http.Client, serverURL, username, forwardedFor string) *http.Response {
	t.Helper()
	response, err := client.Get(serverURL + "/login")
	if err != nil {
		t.Fatalf("get login: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read login: %v", err)
	}
	form := url.Values{
		"username":   {username},
		"password":   {"wrong-password"},
		"csrf_token": {formToken(t, body)},
	}
	request, err := http.NewRequest(http.MethodPost, serverURL+"/login", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("create login request: %v", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if forwardedFor != "" {
		request.Header.Set("X-Forwarded-For", forwardedFor)
	}
	response, err = client.Do(request)
	if err != nil {
		t.Fatalf("post login: %v", err)
	}
	return response
}

func hiddenValue(t *testing.T, body []byte, name string) string {
	t.Helper()
	pattern := regexp.MustCompile(`name="` + regexp.QuoteMeta(name) + `" value="([^"]+)"`)
	match := pattern.FindSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("hidden %s not found in response: %q", name, body)
	}
	return string(match[1])
}
