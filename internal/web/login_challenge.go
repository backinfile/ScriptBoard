package web

import (
	"crypto/rand"
	"errors"
	"fmt"
	"scriptboard/internal/identity"
	"sync"
	"time"
)

const (
	loginChallengeLifetime = 5 * time.Minute
	maxLoginChallenges     = 1024
)

type loginChallenge struct {
	UserID         string
	Username       string
	Role           identity.Role
	AuthVersion    int64
	RemoteHost     string
	MFAEnabled     bool
	PasskeyEnabled bool
	ExpiresAt      time.Time
}

type loginChallengeStore struct {
	mu      sync.Mutex
	entries map[string]loginChallenge
}

func newLoginChallengeStore() *loginChallengeStore {
	return &loginChallengeStore{entries: make(map[string]loginChallenge)}
}

func (store *loginChallengeStore) put(value loginChallenge, now time.Time) (string, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.prune(now)
	if len(store.entries) >= maxLoginChallenges {
		return "", errors.New("too many pending login challenges")
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	id := fmt.Sprintf("%x", raw)
	value.ExpiresAt = now.Add(loginChallengeLifetime)
	store.entries[id] = value
	return id, nil
}

func (store *loginChallengeStore) get(id, remoteHost string, now time.Time) (loginChallenge, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.prune(now)
	value, ok := store.entries[id]
	if !ok || value.RemoteHost != remoteHost {
		return loginChallenge{}, false
	}
	return value, true
}

func (store *loginChallengeStore) take(id, remoteHost string, now time.Time) (loginChallenge, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.prune(now)
	value, ok := store.entries[id]
	if !ok || value.RemoteHost != remoteHost {
		return loginChallenge{}, false
	}
	delete(store.entries, id)
	return value, true
}

func (store *loginChallengeStore) delete(id string) {
	store.mu.Lock()
	defer store.mu.Unlock()
	delete(store.entries, id)
}

func (store *loginChallengeStore) prune(now time.Time) {
	for id, value := range store.entries {
		if !now.Before(value.ExpiresAt) {
			delete(store.entries, id)
		}
	}
}
