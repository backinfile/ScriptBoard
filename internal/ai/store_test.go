package ai

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "ai.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := NewStore(db, t.TempDir())
	if err := store.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestStorePersistsProfilesConversationsAndMessages(t *testing.T) {
	store := openTestStore(t)
	profile := ModelProfile{
		ID: "profile-1", Name: "Primary", Protocol: ProtocolOpenAIResponses,
		BaseURL: "https://api.example.com/v1", Model: "model-1", AuthMode: AuthNone,
		ContextWindow: 128000, MaxOutputTokens: 4096,
		Permission:  Permission{Query: true, Execute: true, Modify: true},
		AutoApprove: true, DefaultRunTimeoutSec: 300,
	}
	if err := store.SaveProfile(context.Background(), profile); err != nil {
		t.Fatal(err)
	}
	conversation, err := store.CreateConversation(context.Background(), profile.ID, Permission{Execute: true}, "hello world")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendMessage(context.Background(), conversation.ID, ModelMessage{Role: "user", Text: "hello world"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendMessage(context.Background(), conversation.ID, ModelMessage{
		Role: "assistant", ToolCalls: []ToolCall{{ID: "call-1", Name: "list_files", Arguments: json.RawMessage(`{"path":""}`)}},
	}, &Usage{InputTokens: 10, OutputTokens: 4, TotalTokens: 14}); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.GetConversation(context.Background(), conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Permission.Query || !loaded.Permission.Execute || loaded.Permission.Modify {
		t.Fatalf("conversation permission = %#v", loaded.Permission)
	}
	messages, err := store.ListMessages(context.Background(), conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[1].Message.ToolCalls[0].Name != "list_files" {
		t.Fatalf("messages = %#v", messages)
	}
}

func TestStoreMarksActiveTurnsAndBatchesInterrupted(t *testing.T) {
	store := openTestStore(t)
	if err := store.SaveProfile(context.Background(), ModelProfile{
		ID: "p", Name: "P", Protocol: ProtocolOpenAIChat, BaseURL: "https://example.com/v1",
		Model: "m", AuthMode: AuthNone, Permission: Permission{Query: true},
	}); err != nil {
		t.Fatal(err)
	}
	conversation, err := store.CreateConversation(context.Background(), "p", Permission{Query: true}, "test")
	if err != nil {
		t.Fatal(err)
	}
	turn, err := store.StartTurn(context.Background(), conversation.ID, "p", "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	batch, err := store.SubmitBatch(context.Background(), conversation.ID, turn.ID, []Action{{
		Kind: "run.start", Risk: RiskExecute, Summary: "run script", Input: json.RawMessage(`{"path":"a.sh"}`),
	}}, time.Now().Add(30*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetBatchStatus(context.Background(), batch.ID, BatchRunning, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.RecoverInterrupted(context.Background()); err != nil {
		t.Fatal(err)
	}
	gotTurn, err := store.GetTurn(context.Background(), turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotTurn.Status != TurnInterrupted {
		t.Fatalf("turn status = %q", gotTurn.Status)
	}
	gotBatch, err := store.GetBatch(context.Background(), batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotBatch.Status != BatchInterrupted {
		t.Fatalf("batch status = %q", gotBatch.Status)
	}
}

func TestStoreConversationLifecycleAndEvents(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	if err := store.SaveProfile(ctx, ModelProfile{
		ID: "p", Name: "P", Protocol: ProtocolOpenAIChat, BaseURL: "https://example.com/v1",
		Model: "m", AuthMode: AuthNone, Permission: Permission{Query: true, Execute: true, Modify: true},
	}); err != nil {
		t.Fatal(err)
	}
	conversation, err := store.CreateConversation(ctx, "p", Permission{Query: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateConversation(ctx, conversation.ID, "Diagnose backup", "run", "run-1", "failed backup"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddEvent(ctx, conversation.ID, "", "", "text_delta", json.RawMessage(`{"text":"hello"}`)); err != nil {
		t.Fatal(err)
	}
	events, err := store.ListEvents(ctx, conversation.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != "text_delta" || events[0].ID == 0 {
		t.Fatalf("events = %#v", events)
	}
	loaded, err := store.GetConversation(ctx, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Title != "Diagnose backup" || loaded.ContextType != "run" || loaded.ContextID != "run-1" {
		t.Fatalf("conversation = %#v", loaded)
	}
	if err := store.DeleteConversation(ctx, conversation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetConversation(ctx, conversation.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetConversation after delete error = %v", err)
	}
}

func TestStoreSoftDeletesProfile(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	if err := store.SaveProfile(ctx, ModelProfile{
		ID: "p", Name: "P", Protocol: ProtocolOpenAIChat, BaseURL: "https://example.com/v1",
		Model: "m", AuthMode: AuthNone, Permission: Permission{Query: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.DisableProfile(ctx, "p"); err != nil {
		t.Fatal(err)
	}
	active, err := store.ListProfiles(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 {
		t.Fatalf("active profiles = %#v", active)
	}
	profile, err := store.GetProfile(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if !profile.Disabled {
		t.Fatal("profile was not disabled")
	}
}

func TestStoreStagesAndConsumesAttachment(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	if err := store.SaveProfile(ctx, ModelProfile{
		ID: "p", Name: "P", Protocol: ProtocolOpenAIChat, BaseURL: "https://example.com/v1",
		Model: "m", AuthMode: AuthNone, Permission: Permission{Query: true, Modify: true},
	}); err != nil {
		t.Fatal(err)
	}
	conversation, err := store.CreateConversation(ctx, "p", Permission{Query: true, Modify: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.CreateAttachment(ctx, conversation.ID, "notes.txt", "text/plain", strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if item.Size != 5 || item.SHA256 == "" {
		t.Fatalf("attachment = %#v", item)
	}
	items, err := store.ListAttachments(ctx, conversation.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("attachments=%#v err=%v", items, err)
	}
	if err := store.ConsumeAttachment(ctx, conversation.ID, item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetAttachment(ctx, conversation.ID, item.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetAttachment after consume error = %v", err)
	}
}
