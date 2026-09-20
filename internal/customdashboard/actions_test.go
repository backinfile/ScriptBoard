package customdashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// testActionManager 返回带快捷执行项存在性校验的 Manager：仅 qr-1 存在。
func testActionManager(t *testing.T) *Manager {
	t.Helper()
	manager := testManager(t)
	manager.quickRunExists = func(_ context.Context, id string) (bool, error) { return id == "qr-1", nil }
	return manager
}

func createActionDashboard(t *testing.T, manager *Manager) Dashboard {
	t.Helper()
	dashboard, err := manager.CreateDashboard(context.Background(), DashboardInput{Name: "操作面板", Slug: "actions"})
	if err != nil {
		t.Fatal(err)
	}
	return dashboard
}

func cardInputWithActions(config string) CardInput {
	return CardInput{Name: "额度", Type: CardNumber, SourceURL: "https://example.test/usage", ValuePath: "value", Config: json.RawMessage(config)}
}

func TestCardActionsAcceptValidDefinitions(t *testing.T) {
	manager := testActionManager(t)
	ctx := context.Background()
	dashboard := createActionDashboard(t, manager)
	card, err := manager.CreateCard(ctx, dashboard.ID, cardInputWithActions(`{"actions":[
		{"label":"部署","kind":"quick_run","quickRunId":"qr-1","confirm":true,"confirmText":"确认部署？","style":"primary"},
		{"label":"打开客厅灯","kind":"browser_http","method":"post","url":"http://ha.local:8123/api/services/light/turn_on","visitorCredential":"Authorization: Bearer","publicAllowed":true}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Actions []CardAction `json:"actions"`
	}
	if err := json.Unmarshal(card.Config, &config); err != nil {
		t.Fatal(err)
	}
	if len(config.Actions) != 2 {
		t.Fatalf("actions=%#v", config.Actions)
	}
	first, second := config.Actions[0], config.Actions[1]
	if !strings.HasPrefix(first.ID, "act_") || !strings.HasPrefix(second.ID, "act_") || first.ID == second.ID {
		t.Fatalf("action ids not generated: %q %q", first.ID, second.ID)
	}
	if first.Style != "primary" || first.QuickRunID != "qr-1" || first.Method != "" || first.URL != "" {
		t.Fatalf("quick_run action not normalized: %#v", first)
	}
	if second.Method != "POST" || second.VisitorCredential != "Authorization: Bearer" || second.QuickRunID != "" {
		t.Fatalf("browser_http action not normalized: %#v", second)
	}
}

func TestCardActionIDsArePreservedOnUpdate(t *testing.T) {
	manager := testActionManager(t)
	ctx := context.Background()
	dashboard := createActionDashboard(t, manager)
	card, err := manager.CreateCard(ctx, dashboard.ID, cardInputWithActions(`{"actions":[{"id":"act-stable","label":"重启","kind":"quick_run","quickRunId":"qr-1"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	updated, err := manager.UpdateCard(ctx, card.ID, cardInputWithActions(`{"actions":[{"id":"act-stable","label":"重启服务","kind":"quick_run","quickRunId":"qr-1","style":"danger","confirm":true,"confirmText":"确认重启？"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Actions []CardAction `json:"actions"`
	}
	if err := json.Unmarshal(updated.Config, &config); err != nil || len(config.Actions) != 1 {
		t.Fatalf("updated config: %s %v", updated.Config, err)
	}
	if config.Actions[0].ID != "act-stable" || config.Actions[0].Style != "danger" {
		t.Fatalf("action id not preserved: %#v", config.Actions[0])
	}
}

func TestCardActionsRejectInvalidDefinitions(t *testing.T) {
	manager := testActionManager(t)
	ctx := context.Background()
	dashboard := createActionDashboard(t, manager)
	long41 := strings.Repeat("按", 41)
	long201 := strings.Repeat("确", 201)
	nine := `{"actions":[` + strings.Repeat(`{"label":"a","kind":"browser_http","method":"GET","url":"https://example.test"},`, 8) + `{"label":"a","kind":"browser_http","method":"GET","url":"https://example.test"}]}`
	cases := map[string]string{
		"超过 8 个操作":  nine,
		"名称为空":      `{"actions":[{"label":" ","kind":"browser_http","method":"GET","url":"https://example.test"}]}`,
		"名称超过 40 字": `{"actions":[{"label":"` + long41 + `","kind":"browser_http","method":"GET","url":"https://example.test"}]}`,
		"类型未知":      `{"actions":[{"label":"a","kind":"exec"}]}`,
		"样式未知":      `{"actions":[{"label":"a","kind":"browser_http","method":"GET","url":"https://example.test","style":"fancy"}]}`,
		"危险样式未确认":   `{"actions":[{"label":"a","kind":"browser_http","method":"GET","url":"https://example.test","style":"danger"}]}`,
		"确认缺文案":     `{"actions":[{"label":"a","kind":"browser_http","method":"GET","url":"https://example.test","confirm":true}]}`,
		"确认文案超长":    `{"actions":[{"label":"a","kind":"browser_http","method":"GET","url":"https://example.test","confirm":true,"confirmText":"` + long201 + `"}]}`,
		"快捷执行为空":    `{"actions":[{"label":"a","kind":"quick_run"}]}`,
		"快捷执行不存在":   `{"actions":[{"label":"a","kind":"quick_run","quickRunId":"qr-missing"}]}`,
		"夹带静态请求头":   `{"actions":[{"label":"a","kind":"browser_http","method":"GET","url":"https://example.test","headers":{"Authorization":"Bearer secret"}}]}`,
		"方法不支持":     `{"actions":[{"label":"a","kind":"browser_http","method":"PUT","url":"https://example.test"}]}`,
		"相对地址":      `{"actions":[{"label":"a","kind":"browser_http","method":"GET","url":"/relative"}]}`,
		"非 HTTP 协议": `{"actions":[{"label":"a","kind":"browser_http","method":"GET","url":"ftp://example.test/x"}]}`,
		"地址带凭据":     `{"actions":[{"label":"a","kind":"browser_http","method":"GET","url":"https://user:pass@example.test/x"}]}`,
		"地址带片段":     `{"actions":[{"label":"a","kind":"browser_http","method":"GET","url":"https://example.test/x#frag"}]}`,
		"访客凭据含控制字符": `{"actions":[{"label":"a","kind":"browser_http","method":"GET","url":"https://example.test","visitorCredential":"Authorization: Bearer\nX-Evil: 1"}]}`,
		"操作 ID 重复":  `{"actions":[{"id":"act-dup","label":"a","kind":"browser_http","method":"GET","url":"https://example.test"},{"id":"act-dup","label":"b","kind":"browser_http","method":"GET","url":"https://example.test"}]}`,
		"操作不是数组":    `{"actions":{"label":"a"}}`,
	}
	for name, config := range cases {
		if _, err := manager.CreateCard(ctx, dashboard.ID, cardInputWithActions(config)); err == nil {
			t.Fatalf("%s：无效操作配置被接受", name)
		}
	}
}

func TestCardActionsRequireQuickRunExistenceProbe(t *testing.T) {
	manager := testManager(t) // 未注入 QuickRunExists
	dashboard := createActionDashboard(t, manager)
	_, err := manager.CreateCard(context.Background(), dashboard.ID, cardInputWithActions(`{"actions":[{"label":"a","kind":"quick_run","quickRunId":"qr-1"}]}`))
	if err == nil {
		t.Fatal("quick_run action accepted without existence probe")
	}
}

func TestFlowCardTypeRequiresValidFlowConfig(t *testing.T) {
	// B 阶段起 flow 卡片配置做完整校验：空配置与非对象配置均被拒绝。
	manager := flowTestManager(t, map[string]string{"qr-a": "run-a"})
	ctx := context.Background()
	dashboard := createActionDashboard(t, manager)
	config, err := manager.NormalizeFlowYAML(ctx, "version: 1\nflow:\n  nodes:\n    - {id: a, name: A, run: run-a}\n")
	if err != nil {
		t.Fatal(err)
	}
	card, err := manager.CreateCard(ctx, dashboard.ID, CardInput{Name: "发布流程", Type: CardFlow, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CreateCard(ctx, dashboard.ID, CardInput{Name: "空流程", Type: CardFlow}); err == nil {
		t.Fatal("flow card accepted empty config")
	}
	if _, err := manager.CreateCard(ctx, dashboard.ID, CardInput{Name: "坏流程", Type: CardFlow, Config: json.RawMessage(`[1]`)}); err == nil {
		t.Fatal("flow card accepted non-object config")
	}
	if _, err := manager.RefreshCard(ctx, card.ID); err == nil {
		t.Fatal("flow card must not go through data refresh")
	}
}

func TestSetVisibilityLifecycleGeneratesAndDropsAccessKey(t *testing.T) {
	manager := testManager(t)
	ctx := context.Background()
	dashboard := createActionDashboard(t, manager)
	if dashboard.Visibility != VisibilityPrivate || dashboard.Public {
		t.Fatalf("new dashboard visibility=%q", dashboard.Visibility)
	}
	if _, err := manager.GetPublicDashboard(ctx, dashboard.Slug); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("private dashboard exposed: %v", err)
	}

	updated, key, err := manager.SetVisibility(ctx, dashboard.ID, VisibilityPublicOperate)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Visibility != VisibilityPublicOperate || !updated.Public || key == "" || !strings.HasPrefix(updated.AccessKeyHint, "dk_") {
		t.Fatalf("public_operate activation: %#v key=%q", updated, key)
	}
	// 已有 Key 时再次设置不重新生成。
	again, secondKey, err := manager.SetVisibility(ctx, dashboard.ID, VisibilityPublicOperate)
	if err != nil || secondKey != "" || again.AccessKeyHint != updated.AccessKeyHint {
		t.Fatalf("key unexpectedly rotated: hint=%q key=%q err=%v", again.AccessKeyHint, secondKey, err)
	}
	ok, err := manager.VerifyAccessKey(ctx, dashboard.ID, key)
	if err != nil || !ok {
		t.Fatalf("generated key did not verify: ok=%v err=%v", ok, err)
	}

	downgraded, key, err := manager.SetVisibility(ctx, dashboard.ID, VisibilityPublicRead)
	if err != nil || key != "" {
		t.Fatalf("downgrade: key=%q err=%v", key, err)
	}
	if downgraded.Visibility != VisibilityPublicRead || downgraded.AccessKeyHint != "" {
		t.Fatalf("downgrade kept key material: %#v", downgraded)
	}
	if ok, err := manager.VerifyAccessKey(ctx, dashboard.ID, key); err != nil || ok {
		t.Fatalf("revoked key still verifies: ok=%v err=%v", ok, err)
	}
	if _, err := manager.GetPublicDashboard(ctx, dashboard.Slug); err != nil {
		t.Fatalf("public_read dashboard not visible: %v", err)
	}

	private, _, err := manager.SetVisibility(ctx, dashboard.ID, VisibilityPrivate)
	if err != nil || private.Visibility != VisibilityPrivate || private.Public {
		t.Fatalf("private downgrade: %#v err=%v", private, err)
	}
	if _, err := manager.GetPublicDashboard(ctx, dashboard.Slug); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("private dashboard still exposed: %v", err)
	}
	if _, _, err := manager.SetVisibility(ctx, dashboard.ID, Visibility("friends")); err == nil {
		t.Fatal("invalid visibility accepted")
	}
	if _, _, err := manager.SetVisibility(ctx, "missing", VisibilityPublicRead); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing dashboard: %v", err)
	}
}

func TestUpdateDashboardPreservesOperateVisibilityThroughLegacySwitch(t *testing.T) {
	manager := testManager(t)
	ctx := context.Background()
	dashboard := createActionDashboard(t, manager)
	if _, _, err := manager.SetVisibility(ctx, dashboard.ID, VisibilityAnonymousOperate); err != nil {
		t.Fatal(err)
	}
	// 旧公开开关往返（Public=true）不得丢失可操作档位。
	updated, err := manager.UpdateDashboard(ctx, dashboard.ID, DashboardInput{Name: "操作面板", Slug: "actions", Public: true})
	if err != nil || updated.Visibility != VisibilityAnonymousOperate {
		t.Fatalf("legacy edit dropped visibility: %#v err=%v", updated, err)
	}
	updated, err = manager.UpdateDashboard(ctx, dashboard.ID, DashboardInput{Name: "操作面板", Slug: "actions", Public: false})
	if err != nil || updated.Visibility != VisibilityPrivate {
		t.Fatalf("legacy private switch: %#v err=%v", updated, err)
	}
}

func TestAccessKeyRotateRevokeAndHint(t *testing.T) {
	manager := testManager(t)
	ctx := context.Background()
	dashboard := createActionDashboard(t, manager)

	key, hint, err := manager.GenerateAccessKey(ctx, dashboard.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hint, "dk_") || len(hint) != len("dk_")+6 || strings.Contains(key, hint[3:]) {
		t.Fatalf("hint leaks key material: key=%q hint=%q", key, hint)
	}
	if ok, err := manager.VerifyAccessKey(ctx, dashboard.ID, key); err != nil || !ok {
		t.Fatalf("verify failed: ok=%v err=%v", ok, err)
	}
	if ok, err := manager.VerifyAccessKey(ctx, dashboard.ID, key+"x"); err != nil || ok {
		t.Fatalf("tampered key verified: ok=%v err=%v", ok, err)
	}

	rotated, rotatedHint, err := manager.RotateAccessKey(ctx, dashboard.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rotated == key || rotatedHint == hint {
		t.Fatal("rotation returned identical key material")
	}
	if ok, _ := manager.VerifyAccessKey(ctx, dashboard.ID, key); ok {
		t.Fatal("old key survived rotation")
	}
	if ok, _ := manager.VerifyAccessKey(ctx, dashboard.ID, rotated); !ok {
		t.Fatal("rotated key did not verify")
	}

	if err := manager.RevokeAccessKey(ctx, dashboard.ID); err != nil {
		t.Fatal(err)
	}
	if ok, _ := manager.VerifyAccessKey(ctx, dashboard.ID, rotated); ok {
		t.Fatal("revoked key still verifies")
	}
	if _, _, err := manager.GenerateAccessKey(ctx, "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing dashboard: %v", err)
	}
}

func TestAccessKeySealedToDashboardPurpose(t *testing.T) {
	manager := testManager(t)
	ctx := context.Background()
	dashboard := createActionDashboard(t, manager)
	other, err := manager.CreateDashboard(ctx, DashboardInput{Name: "其他", Slug: "other"})
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := manager.GenerateAccessKey(ctx, dashboard.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 用错误 purpose 密封的密文无法通过验证：把密文搬到另一面板。
	var ciphertext []byte
	if err := manager.db.QueryRow(`SELECT access_key_ciphertext FROM custom_dashboards WHERE id=?`, dashboard.ID).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.db.Exec(`UPDATE custom_dashboards SET access_key_ciphertext=? WHERE id=?`, ciphertext, other.ID); err != nil {
		t.Fatal(err)
	}
	if ok, err := manager.VerifyAccessKey(ctx, other.ID, key); err != nil || ok {
		t.Fatalf("key unsealed under wrong purpose: ok=%v err=%v", ok, err)
	}
	// 密文损坏同样视为验证失败而非报错。
	if _, err := manager.db.Exec(`UPDATE custom_dashboards SET access_key_ciphertext=X'00' WHERE id=?`, other.ID); err != nil {
		t.Fatal(err)
	}
	if ok, err := manager.VerifyAccessKey(ctx, other.ID, key); err != nil || ok {
		t.Fatalf("corrupt ciphertext verified: ok=%v err=%v", ok, err)
	}
}

func TestAccessKeyLockoutAfterTenFailures(t *testing.T) {
	manager := testManager(t)
	ctx := context.Background()
	dashboard := createActionDashboard(t, manager)
	key, _, err := manager.GenerateAccessKey(ctx, dashboard.ID)
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < accessKeyMaxFailures; attempt++ {
		if ok, err := manager.VerifyAccessKey(ctx, dashboard.ID, "wrong-"+strings.Repeat("a", 40)); err != nil || ok {
			t.Fatalf("attempt %d: ok=%v err=%v", attempt, ok, err)
		}
	}
	if _, err := manager.VerifyAccessKey(ctx, dashboard.ID, key); !errors.Is(err, ErrAccessKeyLocked) {
		t.Fatalf("correct key not locked out: %v", err)
	}
	// 锁定窗口过后自动恢复（内存计数，重启清零同理）。
	now := time.Now()
	manager.now = func() time.Time { return now.Add(accessKeyLockoutWindow + time.Minute) }
	if ok, err := manager.VerifyAccessKey(ctx, dashboard.ID, key); err != nil || !ok {
		t.Fatalf("lockout did not expire: ok=%v err=%v", ok, err)
	}
}

func TestPublicDashboardActionProjectionByVisibility(t *testing.T) {
	manager := testActionManager(t)
	ctx := context.Background()
	dashboard, err := manager.CreateDashboard(ctx, DashboardInput{Name: "操作面板", Slug: "actions", Public: true})
	if err != nil {
		t.Fatal(err)
	}
	card, err := manager.CreateCard(ctx, dashboard.ID, cardInputWithActions(`{"actions":[
		{"id":"act-private","label":"内部部署","kind":"quick_run","quickRunId":"qr-1"},
		{"id":"act-public-run","label":"公开部署","kind":"quick_run","quickRunId":"qr-1","publicAllowed":true,"style":"primary","confirm":true,"confirmText":"确认？"},
		{"id":"act-browser","label":"开灯","kind":"browser_http","method":"POST","url":"http://ha.local:8123/api/services/light/turn_on","visitorCredential":"Authorization: Bearer","publicAllowed":true}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	publicActions := func(t *testing.T) []CardAction {
		t.Helper()
		public, err := manager.GetPublicDashboard(ctx, dashboard.Slug)
		if err != nil {
			t.Fatal(err)
		}
		var config struct {
			Actions []CardAction `json:"actions"`
		}
		if err := json.Unmarshal(public.Cards[0].Config, &config); err != nil {
			t.Fatalf("public config: %s %v", public.Cards[0].Config, err)
		}
		return config.Actions
	}

	// public_read：完全不下发 actions。
	if actions := publicActions(t); actions != nil {
		t.Fatalf("public_read leaked actions: %#v", actions)
	}

	// public_operate：仅 publicAllowed，quick_run 不带 quickRunId。
	if _, _, err := manager.SetVisibility(ctx, dashboard.ID, VisibilityPublicOperate); err != nil {
		t.Fatal(err)
	}
	actions := publicActions(t)
	if len(actions) != 2 {
		t.Fatalf("public_operate actions: %#v", actions)
	}
	for _, action := range actions {
		if action.ID == "act-private" {
			t.Fatalf("private action exposed: %#v", action)
		}
		if action.QuickRunID != "" {
			t.Fatalf("quickRunId exposed: %#v", action)
		}
	}
	run := actions[0]
	if run.ID != "act-public-run" || run.Label != "公开部署" || run.Kind != "quick_run" || run.Style != "primary" || !run.Confirm || run.ConfirmText != "确认？" {
		t.Fatalf("quick_run projection missing safe fields: %#v", run)
	}
	browser := actions[1]
	if browser.Method != "POST" || browser.URL != "http://ha.local:8123/api/services/light/turn_on" || browser.VisitorCredential != "Authorization: Bearer" {
		t.Fatalf("browser_http projection missing fields: %#v", browser)
	}

	// anonymous_operate：仅 browser_http。
	if _, _, err := manager.SetVisibility(ctx, dashboard.ID, VisibilityAnonymousOperate); err != nil {
		t.Fatal(err)
	}
	actions = publicActions(t)
	if len(actions) != 1 || actions[0].ID != "act-browser" {
		t.Fatalf("anonymous_operate actions: %#v", actions)
	}

	// 管理视图保留完整配置。
	full, err := manager.GetCard(ctx, card.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(full.Config), "qr-1") || !strings.Contains(string(full.Config), "act-private") {
		t.Fatalf("manage view lost actions: %s", full.Config)
	}
}
