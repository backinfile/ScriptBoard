package app

import (
	"testing"
	"time"
)

func TestLoginChallengeIsBoundExpiresAndCanOnlyBeConsumedOnce(t *testing.T) {
	store := newLoginChallengeStore()
	now := time.Date(2026, 8, 13, 8, 0, 0, 0, time.UTC)
	id, err := store.put(loginChallenge{UserID: "user-1", RemoteHost: "127.0.0.1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := store.get(id, "127.0.0.2", now); ok {
		t.Fatal("login challenge was accepted from a different remote host")
	}
	if _, ok := store.get(id, "127.0.0.1", now.Add(loginChallengeLifetime)); ok {
		t.Fatal("expired login challenge was accepted")
	}

	id, err = store.put(loginChallenge{UserID: "user-1", RemoteHost: "127.0.0.1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := store.take(id, "127.0.0.1", now); !ok {
		t.Fatal("valid login challenge was rejected")
	}
	if _, ok := store.take(id, "127.0.0.1", now); ok {
		t.Fatal("login challenge was consumed more than once")
	}
}
