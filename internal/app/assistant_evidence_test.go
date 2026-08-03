package app

import (
	"errors"
	"testing"
	"time"
)

func TestAssistantEvidenceCursorIsBoundAndExpires(t *testing.T) {
	key := [32]byte{1, 2, 3}
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	binding := assistantEvidenceCursor{UserID: "user", ConversationID: "conversation", Tool: "search_run_log", Target: "run", QueryDigest: assistantEvidenceQueryDigest("failed")}
	value := binding
	value.Offset = 12
	value.ExpiresAt = now.Add(5 * time.Minute).Unix()
	encoded, err := encodeAssistantEvidenceCursor(key, value)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeAssistantEvidenceCursor(key, encoded, binding, now)
	if err != nil || decoded.Offset != 12 {
		t.Fatalf("decoded = %#v, error = %v", decoded, err)
	}
	changed := binding
	changed.ConversationID = "other"
	if _, err := decodeAssistantEvidenceCursor(key, encoded, changed, now); !errors.Is(err, errAssistantEvidenceCursor) {
		t.Fatalf("cross-conversation cursor error = %v", err)
	}
	if _, err := decodeAssistantEvidenceCursor(key, encoded, binding, now.Add(6*time.Minute)); !errors.Is(err, errAssistantEvidenceCursor) {
		t.Fatalf("expired cursor error = %v", err)
	}
}
