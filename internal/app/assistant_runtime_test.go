package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"scriptboard/internal/assistant"
)

func TestAssistantRuntimeReleasesWarmProcessAfterCompletedTurn(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the deterministic fake Pi executable")
	}
	stateRoot := t.TempDir()
	db, err := openDatabase(filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := assistant.New(db, assistant.Options{StateRoot: stateRoot})
	if err != nil {
		t.Fatal(err)
	}
	actor := assistant.Actor{UserID: "runtime-owner", Username: "operator"}
	model, err := store.SaveModel(context.Background(), actor, "", assistant.ModelInput{
		Name: "Fixture", Provider: assistant.ProviderOpenAICompatible, Model: "fixture-model",
		Endpoint: "http://127.0.0.1:11434/v1", APIKey: "fixture-key", MakeDefault: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateSettings(context.Background(), actor, assistant.SettingsInput{Enabled: true, MaxActiveConversations: 1}); err != nil {
		t.Fatal(err)
	}
	conversation, err := store.CreateConversation(context.Background(), actor, assistant.ConversationInput{ModelID: model.ID, InitialMessage: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := store.BeginAssistantReply(context.Background(), actor, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}

	installFakeAssistantRuntime(t, stateRoot)

	coordinator := newAssistantRuntimeCoordinator(stateRoot, store, 1)
	coordinator.warmDuration = 20 * time.Millisecond
	coordinator.maxTurnDuration = 2 * time.Second
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = coordinator.Close(ctx)
	})
	if err := coordinator.Execute(context.Background(), actor, conversation, "hello", reply); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		current, readErr := store.Conversation(context.Background(), actor, conversation.ID)
		if readErr == nil && current.Status == "idle" && coordinator.supervisor.Active() == 0 {
			messages, messageErr := store.Messages(context.Background(), actor, conversation.ID)
			if messageErr != nil {
				t.Fatal(messageErr)
			}
			if len(messages) != 2 || messages[1].Status != "complete" || messages[1].Body != "fixture response" {
				t.Fatalf("messages = %+v", messages)
			}
			if current.Telemetry.TotalTokens != 2300 || current.Telemetry.ContextPercent == nil || *current.Telemetry.ContextPercent != 25 {
				t.Fatalf("session telemetry = %#v", current.Telemetry)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	current, _ := store.Conversation(context.Background(), actor, conversation.ID)
	t.Fatalf("turn did not settle and release its warm process: status=%q active=%d", current.Status, coordinator.supervisor.Active())
}

func TestAssistantRuntimeWarmSessionsNeverConsumeAllConversationCapacity(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the deterministic fake Pi executable")
	}
	stateRoot := t.TempDir()
	db, err := openDatabase(filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := assistant.New(db, assistant.Options{StateRoot: stateRoot})
	if err != nil {
		t.Fatal(err)
	}
	actor := assistant.Actor{UserID: "capacity-owner", Username: "operator"}
	model, err := store.SaveModel(context.Background(), actor, "", assistant.ModelInput{
		Name: "Fixture", Provider: assistant.ProviderOpenAICompatible, Model: "fixture-model",
		Endpoint: "http://127.0.0.1:11434/v1", APIKey: "fixture-key", MakeDefault: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateSettings(context.Background(), actor, assistant.SettingsInput{Enabled: true, MaxActiveConversations: 2}); err != nil {
		t.Fatal(err)
	}
	installFakeAssistantRuntime(t, stateRoot)
	coordinator := newAssistantRuntimeCoordinator(stateRoot, store, 2)
	coordinator.warmDuration = time.Hour
	coordinator.maxTurnDuration = 2 * time.Second
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = coordinator.Close(ctx)
	})

	for index := 0; index < 3; index++ {
		conversation, createErr := store.CreateConversation(context.Background(), actor, assistant.ConversationInput{ModelID: model.ID, InitialMessage: "hello"})
		if createErr != nil {
			t.Fatal(createErr)
		}
		reply, replyErr := store.BeginAssistantReply(context.Background(), actor, conversation.ID)
		if replyErr != nil {
			t.Fatal(replyErr)
		}
		if executeErr := coordinator.Execute(context.Background(), actor, conversation, "hello", reply); executeErr != nil {
			t.Fatalf("conversation %d rejected by stale warm capacity: %v", index+1, executeErr)
		}
		waitForAssistantRuntimeState(t, coordinator, store, actor, conversation.ID, "idle", 1)
	}
}

func TestAssistantRuntimeResumesTheConversationSessionAfterWarmStop(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the deterministic fake Pi executable")
	}
	stateRoot := t.TempDir()
	db, err := openDatabase(filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := assistant.New(db, assistant.Options{StateRoot: stateRoot})
	if err != nil {
		t.Fatal(err)
	}
	actor := assistant.Actor{UserID: "resume-owner", Username: "operator"}
	model, err := store.SaveModel(context.Background(), actor, "", assistant.ModelInput{
		Name: "Fixture", Provider: assistant.ProviderOpenAICompatible, Model: "fixture-model",
		Endpoint: "http://127.0.0.1:11434/v1", APIKey: "fixture-key", MakeDefault: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateSettings(context.Background(), actor, assistant.SettingsInput{Enabled: true, MaxActiveConversations: 1}); err != nil {
		t.Fatal(err)
	}
	conversation, err := store.CreateConversation(context.Background(), actor, assistant.ConversationInput{ModelID: model.ID, InitialMessage: "first"})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := store.BeginAssistantReply(context.Background(), actor, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	installFakeAssistantRuntime(t, stateRoot)
	coordinator := newAssistantRuntimeCoordinator(stateRoot, store, 1)
	coordinator.warmDuration = 20 * time.Millisecond
	coordinator.maxTurnDuration = 2 * time.Second
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = coordinator.Close(ctx)
	})
	if err := coordinator.Execute(context.Background(), actor, conversation, "first", reply); err != nil {
		t.Fatal(err)
	}
	waitForAssistantRuntimeState(t, coordinator, store, actor, conversation.ID, "idle", 0)

	turn, err := store.BeginTurn(context.Background(), actor, conversation.ID, "second")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err = store.Conversation(context.Background(), actor, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Execute(context.Background(), actor, conversation, "second", turn.User, turn.Assistant); err != nil {
		t.Fatal(err)
	}
	waitForAssistantRuntimeState(t, coordinator, store, actor, conversation.ID, "idle", 0)
	messages, err := store.Messages(context.Background(), actor, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := messages[len(messages)-1].Body; got != "resumed fixture response" {
		t.Fatalf("second process response = %q, want resumed session evidence", got)
	}
}

func waitForAssistantRuntimeState(t *testing.T, coordinator *assistantRuntimeCoordinator, store *assistant.Service, actor assistant.Actor, conversationID, status string, active int) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		current, err := store.Conversation(context.Background(), actor, conversationID)
		if err == nil && current.Status == status && coordinator.supervisor.Active() == active {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	current, _ := store.Conversation(context.Background(), actor, conversationID)
	t.Fatalf("assistant runtime state=%q active=%d, want state=%q active=%d", current.Status, coordinator.supervisor.Active(), status, active)
}

func TestAssistantRuntimeBoundsTurnDurationAndStopsProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the deterministic fake Pi executable")
	}
	stateRoot := t.TempDir()
	db, err := openDatabase(filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := assistant.New(db, assistant.Options{StateRoot: stateRoot})
	if err != nil {
		t.Fatal(err)
	}
	actor := assistant.Actor{UserID: "timeout-owner", Username: "operator"}
	model, err := store.SaveModel(context.Background(), actor, "", assistant.ModelInput{
		Name: "Fixture", Provider: assistant.ProviderOpenAICompatible, Model: "fixture-model",
		Endpoint: "http://127.0.0.1:11434/v1", APIKey: "fixture-key", MakeDefault: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateSettings(context.Background(), actor, assistant.SettingsInput{Enabled: true, MaxActiveConversations: 1}); err != nil {
		t.Fatal(err)
	}
	conversation, err := store.CreateConversation(context.Background(), actor, assistant.ConversationInput{ModelID: model.ID, InitialMessage: "hang"})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := store.BeginAssistantReply(context.Background(), actor, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	installFakeAssistantRuntime(t, stateRoot)
	coordinator := newAssistantRuntimeCoordinator(stateRoot, store, 1)
	coordinator.maxTurnDuration = 50 * time.Millisecond
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = coordinator.Close(ctx)
	})
	if err := coordinator.Execute(context.Background(), actor, conversation, "hang", reply); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		current, readErr := store.Conversation(context.Background(), actor, conversation.ID)
		if readErr == nil && current.Status == "failed" && coordinator.supervisor.Active() == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	current, _ := store.Conversation(context.Background(), actor, conversation.ID)
	t.Fatalf("timed out turn did not fail and stop: status=%q active=%d", current.Status, coordinator.supervisor.Active())
}

func TestAssistantRuntimeMarksAsynchronousProviderFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the deterministic fake Pi executable")
	}
	stateRoot := t.TempDir()
	db, err := openDatabase(filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := assistant.New(db, assistant.Options{StateRoot: stateRoot})
	if err != nil {
		t.Fatal(err)
	}
	actor := assistant.Actor{UserID: "provider-failure-owner", Username: "operator"}
	model, err := store.SaveModel(context.Background(), actor, "", assistant.ModelInput{
		Name: "Fixture", Provider: assistant.ProviderOpenAICompatible, Model: "fixture-model",
		Endpoint: "http://127.0.0.1:11434/v1", APIKey: "fixture-key", MakeDefault: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateSettings(context.Background(), actor, assistant.SettingsInput{Enabled: true, MaxActiveConversations: 1}); err != nil {
		t.Fatal(err)
	}
	conversation, err := store.CreateConversation(context.Background(), actor, assistant.ConversationInput{ModelID: model.ID, InitialMessage: "provider-error"})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := store.BeginAssistantReply(context.Background(), actor, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	installFakeAssistantRuntime(t, stateRoot)
	coordinator := newAssistantRuntimeCoordinator(stateRoot, store, 1)
	coordinator.maxTurnDuration = 2 * time.Second
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = coordinator.Close(ctx)
	})
	if err := coordinator.Execute(context.Background(), actor, conversation, "provider-error", reply); err != nil {
		t.Fatal(err)
	}
	waitForAssistantRuntimeState(t, coordinator, store, actor, conversation.ID, "failed", 0)
	messages, err := store.Messages(context.Background(), actor, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := messages[len(messages)-1].Status; got != "error" {
		t.Fatalf("assistant message status = %q, want error", got)
	}
}

func TestAssistantRuntimeUsesIndependentBoundedBrowserStreamSlots(t *testing.T) {
	coordinator := newAssistantRuntimeCoordinator(t.TempDir(), nil, 1)
	for index := 0; index < 16; index++ {
		if !coordinator.AcquireBrowserStream() {
			t.Fatalf("browser stream slot %d was unexpectedly rejected", index)
		}
	}
	if coordinator.AcquireBrowserStream() {
		t.Fatal("seventeenth assistant browser stream was accepted")
	}
	coordinator.ReleaseBrowserStream()
	if !coordinator.AcquireBrowserStream() {
		t.Fatal("released assistant browser stream slot was not reusable")
	}
}

func TestAssistantRuntimeTestsProviderWithoutPersistingConversation(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the deterministic fake Pi executable")
	}
	stateRoot := t.TempDir()
	db, err := openDatabase(filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := assistant.New(db, assistant.Options{StateRoot: stateRoot})
	if err != nil {
		t.Fatal(err)
	}
	actor := assistant.Actor{UserID: "provider-owner", Username: "administrator"}
	model, err := store.SaveModel(context.Background(), actor, "", assistant.ModelInput{
		Name: "Fixture", Provider: assistant.ProviderOpenAICompatible, Model: "fixture-model",
		Endpoint: "http://127.0.0.1:11434/v1", APIKey: "fixture-key", MakeDefault: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	installFakeAssistantRuntime(t, stateRoot)
	coordinator := newAssistantRuntimeCoordinator(stateRoot, store, 1)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = coordinator.Close(ctx)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if err := coordinator.TestModel(ctx, actor, model.ID); err != nil {
		t.Fatalf("test model: %v", err)
	}
	if coordinator.ActiveProcesses() != 0 {
		t.Fatalf("provider test left %d Pi processes", coordinator.ActiveProcesses())
	}
	var conversations int
	if err := db.QueryRow("SELECT COUNT(*) FROM assistant_conversations").Scan(&conversations); err != nil || conversations != 0 {
		t.Fatalf("provider test conversations=%d err=%v", conversations, err)
	}
	for _, parent := range []string{"pi-home", "sessions", "workspaces"} {
		matches, err := filepath.Glob(filepath.Join(stateRoot, "assistant", parent, actor.UserID, "provider-test-*"))
		if err != nil || len(matches) != 0 {
			t.Fatalf("provider test retained private %s data: %v (err=%v)", parent, matches, err)
		}
	}
}

func TestAssistantEventReplayRequestsSnapshotForUnknownCursor(t *testing.T) {
	hub := &assistantEventHub{subscribers: make(map[chan assistantBrowserEvent]struct{})}
	subscription := hub.subscribe(7)
	defer subscription.unsubscribe()
	if !subscription.reset {
		t.Fatal("cursor ahead of a fresh event hub did not request a snapshot reset")
	}
}

func TestResolveAssistantThinkingLevelFallsBackWithoutBlockingTheTurn(t *testing.T) {
	tests := []struct {
		requested string
		available []string
		want      string
	}{
		{requested: "medium", available: []string{"off", "low", "medium"}, want: "medium"},
		{requested: "medium", available: []string{"off"}, want: "off"},
		{requested: "high", available: []string{"minimal", "low"}, want: "minimal"},
	}
	for _, test := range tests {
		if got := resolveAssistantThinkingLevel(test.requested, test.available); got != test.want {
			t.Fatalf("resolveAssistantThinkingLevel(%q, %v) = %q, want %q", test.requested, test.available, got, test.want)
		}
	}
}

func installFakeAssistantRuntime(t *testing.T, stateRoot string) {
	t.Helper()
	versionRoot := filepath.Join(stateRoot, "assistant", "runtime", "versions", "fixture")
	if err := os.MkdirAll(versionRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	executableName := "pi"
	if runtime.GOOS == "windows" {
		executableName = "pi.exe"
	}
	executable := filepath.Join(versionRoot, executableName)
	build := exec.Command("go", "build", "-o", executable, "../assistant/pirpc/testdata/fakepi")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fake Pi: %v\n%s", err, output)
	}
	if err := os.WriteFile(filepath.Join(stateRoot, "assistant", "runtime", "active.json"), []byte(`{"version":"fixture","rpcContract":1,"executable":"`+executableName+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
}
