package web_test

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// 覆盖执行参数的后端链路：定义校验、乐观锁版本、启动强类型校验与 env 注入。
func TestQuickRunParamsValidationAndInjection(t *testing.T) {
	root := t.TempDir()
	managed := filepath.Join(root, "managed")
	state := filepath.Join(root, "state")
	client, base := authenticatedClient(t, managed, state)
	name := "params.sh"
	source := "#!/bin/sh\necho \"region=$SCRIPTBOARD_PARAM_REGION,count=$SCRIPTBOARD_PARAM_COUNT\"\n"
	if runtime.GOOS == "windows" {
		name = "params.cmd"
		source = "@echo region=%SCRIPTBOARD_PARAM_REGION%,count=%SCRIPTBOARD_PARAM_COUNT%\r\n"
	}
	path := filepath.Join(managed, name)
	if err := os.WriteFile(path, []byte(source), 0700); err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(hostFileRequestURL(base, "/resources/files/quick-run", path))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	token := formToken(t, body)
	post := func(route string, values url.Values, intended int) []byte {
		t.Helper()
		values.Set("csrf_token", token)
		response, err := client.PostForm(base+route, values)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		if response.StatusCode != intended {
			t.Fatalf("%s: %d %s", route, response.StatusCode, body)
		}
		return body
	}
	db, err := sql.Open("sqlite", filepath.Join(state, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	paramsJSON := `[{"name":"region","label":"区域","type":"enum","required":true,"default":"cn","options":["cn","us"]},{"name":"count","type":"number","default":"01.50"}]`
	post("/config/quick-runs", url.Values{"name": {"Params bad"}, "script": {path}, "params_json": {`[{"name":"1bad","type":"string"}]`}}, http.StatusBadRequest)
	post("/config/quick-runs", url.Values{"name": {"Params quick"}, "script": {path}, "params_json": {paramsJSON}}, http.StatusSeeOther)
	var id, stored string
	var revision int
	if err := db.QueryRow("SELECT id,params_json,revision FROM quick_runs WHERE name='Params quick'").Scan(&id, &stored, &revision); err != nil || stored != paramsJSON {
		t.Fatalf("params_json=%q err=%v", stored, err)
	}

	// 参数定义变化与脚本等发布字段一样推进 revision。
	post("/config/quick-runs/"+id+"/update", url.Values{"name": {"Params quick"}, "params_json": {`[{"name":"x","type":"date"}]`}}, http.StatusBadRequest)
	post("/config/quick-runs/"+id+"/update", url.Values{"name": {"Params quick"}, "params_json": {paramsJSON}}, http.StatusSeeOther)
	var next int
	if err := db.QueryRow("SELECT revision FROM quick_runs WHERE id=?", id).Scan(&next); err != nil || next != revision {
		t.Fatalf("unchanged params bumped revision=%d", next)
	}
	updated := `[{"name":"region","label":"区域","type":"enum","required":true,"default":"cn","options":["cn","us"]},{"name":"count","type":"number","default":"2"}]`
	post("/config/quick-runs/"+id+"/update", url.Values{"name": {"Params quick"}, "params_json": {updated}}, http.StatusSeeOther)
	if err := db.QueryRow("SELECT revision,params_json FROM quick_runs WHERE id=?", id).Scan(&next, &stored); err != nil || next != revision+1 || stored != updated {
		t.Fatalf("revision=%d params=%q err=%v", next, stored, err)
	}

	// 启动时强类型校验：enum 越界拒绝，number 规范化注入。
	if body := post("/config/quick-runs/"+id+"/start", url.Values{"param_region": {"eu"}}, http.StatusBadRequest); !strings.Contains(string(body), "执行参数无效") {
		t.Fatalf("invalid param start body=%s", body)
	}
	waitRun := func() string {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
			var status, logPath string
			if err := db.QueryRow("SELECT status,log_path FROM runs WHERE source_id=? ORDER BY created_at DESC LIMIT 1", id).Scan(&status, &logPath); err == nil &&
				status != "starting" && status != "running" && status != "stopping" && status != "timing_out" {
				content, err := os.ReadFile(logPath)
				if err != nil {
					t.Fatal(err)
				}
				// 运行日志为 JSONL，data 字段是 base64 编码的输出。
				var output strings.Builder
				for _, line := range strings.Split(string(content), "\n") {
					var event struct {
						Data string `json:"data"`
					}
					if json.Unmarshal([]byte(line), &event) == nil && event.Data != "" {
						decoded, err := base64.StdEncoding.DecodeString(event.Data)
						if err != nil {
							t.Fatal(err)
						}
						output.Write(decoded)
					}
				}
				return output.String()
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("run did not finish")
		return ""
	}
	post("/config/quick-runs/"+id+"/start", url.Values{"param_region": {"us"}, "param_count": {"3.0"}}, http.StatusSeeOther)
	if log := waitRun(); !strings.Contains(log, "region=us,count=3") {
		t.Fatalf("run log=%q", log)
	}
	// 非交互启动（无 param_ 字段）走默认值兜底。
	post("/config/quick-runs/"+id+"/start", url.Values{"confirm_overlap": {"yes"}}, http.StatusSeeOther)
	if log := waitRun(); !strings.Contains(log, "region=cn,count=2") {
		t.Fatalf("default run log=%q", log)
	}
}

// 覆盖执行参数的前端链路：编辑器预填、填参页控件、填参提交回跳、列表页入口与复制 ID、overlap 重试带参。
func TestQuickRunParamsPagesAndForms(t *testing.T) {
	root := t.TempDir()
	managed := filepath.Join(root, "managed")
	state := filepath.Join(root, "state")
	client, base := authenticatedClient(t, managed, state)
	name := "paged.sh"
	source := "#!/bin/sh\nsleep 30\n"
	if runtime.GOOS == "windows" {
		name = "paged.cmd"
		source = "@echo off\r\nping 127.0.0.1 -n 31 >nul\r\n"
	}
	path := filepath.Join(managed, name)
	if err := os.WriteFile(path, []byte(source), 0700); err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(hostFileRequestURL(base, "/resources/files/quick-run", path))
	if err != nil {
		t.Fatal(err)
	}
	tokenBody, _ := io.ReadAll(response.Body)
	response.Body.Close()
	token := formToken(t, tokenBody)
	post := func(route string, values url.Values, intended int) *http.Response {
		t.Helper()
		values.Set("csrf_token", token)
		response, err := client.PostForm(base+route, values)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != intended {
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			t.Fatalf("%s: %d %s", route, response.StatusCode, body)
		}
		return response
	}
	get := func(route string) string {
		t.Helper()
		response, err := client.Get(base + route)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d %s", route, response.StatusCode, body)
		}
		return string(body)
	}
	db, err := sql.Open("sqlite", filepath.Join(state, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	paramsJSON := `[{"name":"region","label":"区域","type":"enum","required":true,"default":"cn","options":["cn","us"]},{"name":"count","type":"number","default":"2"},{"name":"flag","type":"boolean","default":"true"},{"name":"note","type":"string"}]`
	post("/config/quick-runs", url.Values{"name": {"Paged params"}, "script": {path}, "params_json": {paramsJSON}}, http.StatusSeeOther).Body.Close()
	post("/config/quick-runs", url.Values{"name": {"Paged plain"}, "script": {path}}, http.StatusSeeOther).Body.Close()
	var id, plainID string
	if err := db.QueryRow("SELECT id FROM quick_runs WHERE name='Paged params'").Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT id FROM quick_runs WHERE name='Paged plain'").Scan(&plainID); err != nil {
		t.Fatal(err)
	}

	// 编辑页含参数编辑器、预填的 params_json 与复制 ID 按钮。
	editPage := get("/config/quick-runs/" + id + "/edit")
	for _, marker := range []string{`data-quickrun-params-editor`, `name="params_json"`, `data-quickrun-param-add`, `data-copy-quick-run-id="` + id + `"`} {
		if !strings.Contains(editPage, marker) {
			t.Fatalf("edit page missing %q", marker)
		}
	}
	if !strings.Contains(editPage, strings.ReplaceAll(paramsJSON, `"`, "&#34;")) {
		t.Fatal("edit page does not prefill params_json")
	}

	// 填参页渲染各类型控件、默认值与必填标注。
	runPage := get("/config/quick-runs/" + id + "/run")
	for _, marker := range []string{
		`name="param_region"`, `<option value="cn" selected>`, `required`,
		`name="param_count" type="number" step="any" value="2"`,
		`name="param_flag"`, `<option value="true" selected>`,
		`name="param_note" value=""`,
		`SCRIPTBOARD_PARAM_REGION`,
		`name="return_to" value="run"`,
		path,
	} {
		if !strings.Contains(runPage, marker) {
			t.Fatalf("run page missing %q", marker)
		}
	}
	// 无参数的执行项访问填参页退回列表。
	response, err = client.Get(base + "/config/quick-runs/" + plainID + "/run")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("plain run page status=%d", response.StatusCode)
	}

	// 填参提交：非法值 303 回填参页并带 error；合法值 303 到运行历史。
	invalid := post("/config/quick-runs/"+id+"/start", url.Values{"return_to": {"run"}, "param_region": {"eu"}}, http.StatusSeeOther)
	location := invalid.Header.Get("Location")
	invalid.Body.Close()
	if !strings.HasPrefix(location, "/config/quick-runs/"+id+"/run?error=") || !strings.Contains(location, "%E6%89%A7%E8%A1%8C%E5%8F%82%E6%95%B0%E6%97%A0%E6%95%88") {
		t.Fatalf("invalid start location=%q", location)
	}
	if page := get("/config/quick-runs/" + id + "/run?error=" + url.QueryEscape("执行参数无效：参数 region（区域）不在可选值内")); !strings.Contains(page, "执行参数无效") {
		t.Fatal("run page does not surface the error query")
	}
	valid := post("/config/quick-runs/"+id+"/start", url.Values{"return_to": {"run"}, "param_region": {"us"}, "param_count": {"3"}}, http.StatusSeeOther)
	location = valid.Header.Get("Location")
	valid.Body.Close()
	if !strings.HasPrefix(location, "/history/runs/") {
		t.Fatalf("valid start location=%q", location)
	}
	// 纯 API 调用保持 400。
	post("/config/quick-runs/"+id+"/start", url.Values{"param_region": {"eu"}, "confirm_overlap": {"yes"}}, http.StatusBadRequest).Body.Close()

	// 列表页：有参数项运行入口为 /run 链接，无参数项保持直接 POST；复制 ID 按钮存在。
	listPage := get("/config/quick-runs")
	for _, marker := range []string{
		`href="/config/quick-runs/` + id + `/run" data-task-link`,
		`action="/config/quick-runs/` + plainID + `/start"`,
		`data-copy-quick-run-id="` + id + `"`,
		`>4 parameters<`,
	} {
		if !strings.Contains(listPage, marker) {
			t.Fatalf("list page missing %q", marker)
		}
	}

	// overlap 确认页以隐藏字段带回执行参数取值。
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		var status string
		if err := db.QueryRow("SELECT status FROM runs WHERE source_id=? ORDER BY created_at DESC LIMIT 1", id).Scan(&status); err == nil && status == "running" {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	overlap := post("/config/quick-runs/"+id+"/start", url.Values{"return_to": {"run"}, "param_region": {"us"}, "param_count": {"4"}}, http.StatusConflict)
	overlapBody, _ := io.ReadAll(overlap.Body)
	overlap.Body.Close()
	if !strings.Contains(string(overlapBody), `name="param_region" value="us"`) || !strings.Contains(string(overlapBody), `name="param_count" value="4"`) {
		t.Fatalf("overlap page lost params: %s", overlapBody)
	}
}

// 执行参数以 {{PARAM_<大写名>}} 变量填入启动参数：保存期校验、运行时替换。
func TestQuickRunParamsInArgumentsTemplate(t *testing.T) {
	root := t.TempDir()
	managed := filepath.Join(root, "managed")
	state := filepath.Join(root, "state")
	client, base := authenticatedClient(t, managed, state)
	name := "args.sh"
	source := "#!/bin/sh\necho \"args:$@\"\n"
	if runtime.GOOS == "windows" {
		name = "args.cmd"
		source = "@echo args:%*\r\n"
	}
	path := filepath.Join(managed, name)
	if err := os.WriteFile(path, []byte(source), 0700); err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(hostFileRequestURL(base, "/resources/files/quick-run", path))
	if err != nil {
		t.Fatal(err)
	}
	tokenBody, _ := io.ReadAll(response.Body)
	response.Body.Close()
	token := formToken(t, tokenBody)
	post := func(route string, values url.Values, intended int) []byte {
		t.Helper()
		values.Set("csrf_token", token)
		response, err := client.PostForm(base+route, values)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		if response.StatusCode != intended {
			t.Fatalf("%s: %d %s", route, response.StatusCode, body)
		}
		return body
	}
	db, err := sql.Open("sqlite", filepath.Join(state, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	paramsJSON := `[{"name":"region","type":"enum","default":"cn","options":["cn","us"]},{"name":"count","type":"number","default":"2"}]`
	// 模板引用了已定义参数：保存通过。
	post("/config/quick-runs", url.Values{"name": {"Args params"}, "script": {path}, "arguments": {"--region {{PARAM_REGION}} --count {{PARAM_COUNT}}"}, "params_json": {paramsJSON}}, http.StatusSeeOther)
	// 模板引用了未定义参数 / 无参数定义：保存拒绝。
	post("/config/quick-runs", url.Values{"name": {"Args unknown"}, "script": {path}, "arguments": {"{{PARAM_NOPE}}"}, "params_json": {paramsJSON}}, http.StatusBadRequest)
	post("/config/quick-runs", url.Values{"name": {"Args nodefs"}, "script": {path}, "arguments": {"{{PARAM_REGION}}"}}, http.StatusBadRequest)

	var id string
	if err := db.QueryRow("SELECT id FROM quick_runs WHERE name='Args params'").Scan(&id); err != nil {
		t.Fatal(err)
	}
	// 编辑路径同样校验：改成引用未定义参数应 400。
	post("/config/quick-runs/"+id+"/update", url.Values{"name": {"Args params"}, "arguments": {"{{PARAM_MISSING}}"}, "params_json": {paramsJSON}}, http.StatusBadRequest)

	post("/config/quick-runs/"+id+"/start", url.Values{"param_region": {"us"}}, http.StatusSeeOther)
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		var status, logPath string
		if err := db.QueryRow("SELECT status,log_path FROM runs WHERE source_id=? ORDER BY created_at DESC LIMIT 1", id).Scan(&status, &logPath); err == nil &&
			status != "starting" && status != "running" && status != "stopping" && status != "timing_out" {
			content, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			var output strings.Builder
			for _, line := range strings.Split(string(content), "\n") {
				var event struct {
					Data string `json:"data"`
				}
				if json.Unmarshal([]byte(line), &event) == nil && event.Data != "" {
					decoded, err := base64.StdEncoding.DecodeString(event.Data)
					if err != nil {
						t.Fatal(err)
					}
					output.Write(decoded)
				}
			}
			// Windows 的 %* 会给参数加引号，统一去掉再比较。
			log := strings.ReplaceAll(output.String(), `"`, "")
			if !strings.Contains(log, "args:--region us --count 2") {
				t.Fatalf("run log=%q", log)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("run did not finish")
}
