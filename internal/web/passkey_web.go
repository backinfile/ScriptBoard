package web

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"scriptboard/internal/passkey"
)

const (
	passkeyCeremonyLifetime = 5 * time.Minute
	maxPasskeyCeremonies    = 1024
)

type passkeyCeremony struct {
	Kind      string
	UserID    string
	TokenHash string
	Origin    string
	Data      webauthn.SessionData
	ExpiresAt time.Time
}

type passkeyCeremonyStore struct {
	mu      sync.Mutex
	entries map[string]passkeyCeremony
}

func newPasskeyCeremonyStore() *passkeyCeremonyStore {
	return &passkeyCeremonyStore{entries: make(map[string]passkeyCeremony)}
}

func (store *passkeyCeremonyStore) put(value passkeyCeremony, now time.Time) (string, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for id, entry := range store.entries {
		if !now.Before(entry.ExpiresAt) {
			delete(store.entries, id)
		}
	}
	if len(store.entries) >= maxPasskeyCeremonies {
		return "", errors.New("too many passkey ceremonies")
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	id := fmt.Sprintf("%x", raw)
	value.ExpiresAt = now.Add(passkeyCeremonyLifetime)
	store.entries[id] = value
	return id, nil
}

func (store *passkeyCeremonyStore) take(id, kind, userID, tokenHash, origin string, now time.Time) (webauthn.SessionData, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	entry, ok := store.entries[id]
	delete(store.entries, id)
	if !ok || !now.Before(entry.ExpiresAt) || entry.Kind != kind || entry.UserID != userID ||
		entry.TokenHash != tokenHash || entry.Origin != origin {
		return webauthn.SessionData{}, false
	}
	return entry.Data, true
}

func (a *App) webAuthnForRequest(request *http.Request) (*webauthn.WebAuthn, string, error) {
	origin := strings.TrimSpace(a.canonicalExternalURL)
	if origin == "" {
		scheme := "http"
		if isSecureRequest(request) {
			scheme = "https"
		}
		origin = scheme + "://" + request.Host
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, "", errors.New("canonical external URL is invalid for WebAuthn")
	}
	origin = strings.TrimSuffix(parsed.String(), "/")
	provider, err := webauthn.New(&webauthn.Config{
		RPID:                  parsed.Hostname(),
		RPDisplayName:         "ScriptBoard",
		RPOrigins:             []string{origin},
		AttestationPreference: protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementPreferred,
			UserVerification: protocol.VerificationRequired,
		},
	})
	if err != nil {
		return nil, "", err
	}
	return provider, origin, nil
}

func (a *App) beginPasskeyLogin(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	if err := request.ParseForm(); err != nil {
		http.Error(response, "invalid passkey request", http.StatusBadRequest)
		return
	}
	csrfCookie, err := request.Cookie(loginCSRFCookieName)
	if err != nil || !subtleCompare(csrfCookie.Value, request.FormValue("csrf_token")) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	challengeID, challenge, ok := a.pendingLoginChallenge(request)
	if !ok || !challenge.PasskeyEnabled {
		http.Error(response, webText(resolveWebLocale(request), "login.verification_expired"), http.StatusUnauthorized)
		return
	}
	user, err := a.passkeys.User(challenge.UserID, challenge.Username)
	if err != nil || len(user.Credentials) == 0 {
		http.Error(response, webText(resolveWebLocale(request), "mfa.unavailable"), http.StatusServiceUnavailable)
		return
	}
	provider, origin, err := a.webAuthnForRequest(request)
	if err != nil {
		http.Error(response, "passkey is unavailable", http.StatusServiceUnavailable)
		return
	}
	options, sessionData, err := provider.BeginLogin(user, webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		http.Error(response, "passkey is unavailable", http.StatusServiceUnavailable)
		return
	}
	ceremonyID, err := a.passkeyCeremonies.put(passkeyCeremony{Kind: "login", UserID: challenge.UserID, TokenHash: challengeID, Origin: origin, Data: *sessionData}, time.Now().UTC())
	if err != nil {
		http.Error(response, "passkey is busy", http.StatusTooManyRequests)
		return
	}
	writePasskeyJSON(response, http.StatusOK, map[string]any{"ceremony_id": ceremonyID, "options": options})
}

func (a *App) beginPasskeyStepUp(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	if !validSessionCSRFValue(request, request.Header.Get("X-CSRF-Token")) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	a.beginAuthenticatedPasskeyAssertion(response, request, current, "step-up")
}

func (a *App) beginAuthenticatedPasskeyAssertion(response http.ResponseWriter, request *http.Request, current session, kind string) {
	user, err := a.passkeys.User(current.userID, current.username)
	if err != nil {
		http.Error(response, webText(resolveWebLocale(request), "mfa.unavailable"), http.StatusServiceUnavailable)
		return
	}
	if len(user.Credentials) == 0 {
		http.Error(response, "no passkey is registered", http.StatusConflict)
		return
	}
	provider, origin, err := a.webAuthnForRequest(request)
	if err != nil {
		http.Error(response, "passkey is unavailable", http.StatusServiceUnavailable)
		return
	}
	options, sessionData, err := provider.BeginLogin(user, webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		http.Error(response, "passkey is unavailable", http.StatusServiceUnavailable)
		return
	}
	id, err := a.passkeyCeremonies.put(passkeyCeremony{Kind: kind, UserID: current.userID, TokenHash: current.tokenHash, Origin: origin, Data: *sessionData}, time.Now().UTC())
	if err != nil {
		http.Error(response, "passkey is busy", http.StatusTooManyRequests)
		return
	}
	writePasskeyJSON(response, http.StatusOK, map[string]any{"ceremony_id": id, "options": options})
}

func (a *App) beginPasskeyRegistration(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	if !validSessionCSRFValue(request, request.Header.Get("X-CSRF-Token")) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	user, err := a.passkeys.User(current.userID, current.username)
	if err != nil {
		http.Error(response, webText(resolveWebLocale(request), "mfa.unavailable"), http.StatusServiceUnavailable)
		return
	}
	provider, origin, err := a.webAuthnForRequest(request)
	if err != nil {
		http.Error(response, "passkey is unavailable", http.StatusServiceUnavailable)
		return
	}
	options, sessionData, err := provider.BeginRegistration(user,
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementPreferred),
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{ResidentKey: protocol.ResidentKeyRequirementPreferred, UserVerification: protocol.VerificationRequired}),
	)
	if err != nil {
		http.Error(response, "passkey is unavailable", http.StatusServiceUnavailable)
		return
	}
	id, err := a.passkeyCeremonies.put(passkeyCeremony{Kind: "registration", UserID: current.userID, TokenHash: current.tokenHash, Origin: origin, Data: *sessionData}, time.Now().UTC())
	if err != nil {
		http.Error(response, "passkey is busy", http.StatusTooManyRequests)
		return
	}
	writePasskeyJSON(response, http.StatusOK, map[string]any{"ceremony_id": id, "options": options})
}

type passkeyFinishRequest struct {
	CeremonyID string          `json:"ceremony_id"`
	Name       string          `json:"name"`
	Credential json.RawMessage `json:"credential"`
}

func (a *App) finishPasskeyRegistration(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	if !validSessionCSRFValue(request, request.Header.Get("X-CSRF-Token")) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	var input passkeyFinishRequest
	if err := json.NewDecoder(request.Body).Decode(&input); err != nil || len(input.Credential) == 0 {
		http.Error(response, "invalid passkey response", http.StatusBadRequest)
		return
	}
	provider, origin, err := a.webAuthnForRequest(request)
	if err != nil {
		http.Error(response, "passkey is unavailable", http.StatusServiceUnavailable)
		return
	}
	sessionData, ok := a.passkeyCeremonies.take(input.CeremonyID, "registration", current.userID, current.tokenHash, origin, time.Now().UTC())
	if !ok {
		http.Error(response, "passkey ceremony expired", http.StatusConflict)
		return
	}
	user, err := a.passkeys.User(current.userID, current.username)
	if err != nil {
		http.Error(response, webText(resolveWebLocale(request), "mfa.unavailable"), http.StatusServiceUnavailable)
		return
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(input.Credential)
	if err != nil {
		a.recordAuditForRequest(request, "passkey_enrollment", current.username, "failed")
		http.Error(response, "invalid passkey response", http.StatusUnauthorized)
		return
	}
	credential, err := provider.CreateCredential(user, sessionData, parsed)
	if err != nil || addPasskeyWithContext(request.Context(), a.passkeys, current.userID, input.Name, *credential) != nil {
		a.recordAuditForRequest(request, "passkey_enrollment", current.username, "failed")
		http.Error(response, "passkey enrollment failed", http.StatusUnauthorized)
		return
	}
	if _, err := a.db.ExecContext(request.Context(), `DELETE FROM sessions WHERE user_id = ?`, current.userID); err != nil {
		_ = deletePasskeyWithContext(request.Context(), a.passkeys, current.userID, fmt.Sprintf("%x", credential.ID))
		http.Error(response, webText(resolveWebLocale(request), "mfa.unavailable"), http.StatusInternalServerError)
		return
	}
	a.recordAuditForRequest(request, "passkey_enrollment", current.username, "succeeded")
	a.cancelAuthenticatedRequests(current.userID)
	expireSessionCookie(response, request)
	writePasskeyJSON(response, http.StatusCreated, map[string]any{"reauthenticate": true})
}

func (a *App) deletePasskey(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	credentialID := request.PathValue("id")
	if _, err := a.db.ExecContext(request.Context(), `DELETE FROM sessions WHERE user_id = ?`, current.userID); err != nil {
		http.Error(response, "could not revoke sessions", http.StatusInternalServerError)
		return
	}
	if err := deletePasskeyWithContext(request.Context(), a.passkeys, current.userID, credentialID); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, passkey.ErrCredentialNotFound) {
			status = http.StatusNotFound
		}
		http.Error(response, "could not delete passkey", status)
		return
	}
	a.recordAuditForRequest(request, "passkey_delete", current.username, "succeeded")
	a.cancelAuthenticatedRequests(current.userID)
	expireSessionCookie(response, request)
	http.Redirect(response, request, "/login", http.StatusSeeOther)
}

func (a *App) verifyPasskeyAssertion(request *http.Request, userID, username, kind, tokenHash, ceremonyID, credentialJSON string) (bool, error) {
	provider, origin, err := a.webAuthnForRequest(request)
	if err != nil {
		return false, err
	}
	sessionData, ok := a.passkeyCeremonies.take(ceremonyID, kind, userID, tokenHash, origin, time.Now().UTC())
	if !ok {
		return false, nil
	}
	user, err := a.passkeys.User(userID, username)
	if err != nil {
		return false, err
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes([]byte(credentialJSON))
	if err != nil {
		return false, nil
	}
	credential, err := provider.ValidateLogin(user, sessionData, parsed)
	if err != nil {
		return false, nil
	}
	if err := a.passkeys.Update(userID, *credential); err != nil {
		return false, err
	}
	return true, nil
}

func writePasskeyJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func subtleCompare(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	var different byte
	for index := range len(left) {
		different |= left[index] ^ right[index]
	}
	return different == 0
}
