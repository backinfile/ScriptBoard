package mcpaccess

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

type OAuthHTTP struct {
	Store                *Store
	CanonicalExternalURL string
	Limiter              *Limiter
}

func (oauth *OAuthHTTP) allow(w http.ResponseWriter, r *http.Request) func() {
	if oauth.Limiter == nil {
		return func() {}
	}
	release, ok := oauth.Limiter.Acquire(SourceKey(r.RemoteAddr) + "\x00" + r.URL.Path)
	if !ok {
		w.Header().Set("Retry-After", "60")
		oauthError(w, http.StatusTooManyRequests, "slow_down")
		return nil
	}
	return release
}

func (oauth *OAuthHTTP) base(r *http.Request) string {
	value := strings.TrimRight(strings.TrimSpace(oauth.CanonicalExternalURL), "/")
	if value != "" {
		return value
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (oauth *OAuthHTTP) AuthorizationServerMetadata(w http.ResponseWriter, r *http.Request) {
	base := oauth.base(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer": base, "authorization_endpoint": base + "/oauth/authorize", "token_endpoint": base + "/oauth/token", "registration_endpoint": base + "/oauth/register", "revocation_endpoint": base + "/oauth/revoke",
		"response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"}, "code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"none"}, "scopes_supported": []string{ScopeObserve, ScopeExecute},
	})
}

type registrationRequest struct {
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method,omitempty"`
	GrantTypes              []string `json:"grant_types,omitempty"`
	ResponseTypes           []string `json:"response_types,omitempty"`
}

func (oauth *OAuthHTTP) Register(w http.ResponseWriter, r *http.Request) {
	release := oauth.allow(w, r)
	if release == nil {
		return
	}
	defer release()
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var input registrationRequest
	if decoder.Decode(&input) != nil {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata")
		return
	}
	if input.TokenEndpointAuthMethod != "" && input.TokenEndpointAuthMethod != "none" {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata")
		return
	}
	if !onlyValues(input.GrantTypes, "authorization_code", "refresh_token") || !onlyValues(input.ResponseTypes, "code") {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata")
		return
	}
	client, err := oauth.Store.RegisterClient(r.Context(), input.ClientName, input.RedirectURIs, "dcr", "")
	if err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"client_id": client.ClientID, "client_name": client.Name, "redirect_uris": client.RedirectURIs, "token_endpoint_auth_method": "none", "grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"}})
}

func (oauth *OAuthHTTP) Token(w http.ResponseWriter, r *http.Request) {
	release := oauth.allow(w, r)
	if release == nil {
		return
	}
	defer release()
	if r.Header.Get("Authorization") != "" {
		oauthError(w, http.StatusUnauthorized, "invalid_client")
		return
	}
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if r.Form.Get("client_secret") != "" {
		oauthError(w, http.StatusUnauthorized, "invalid_client")
		return
	}
	grant := r.Form.Get("grant_type")
	resource := r.Form.Get("resource")
	var set TokenSet
	var err error
	switch grant {
	case "authorization_code":
		set, err = oauth.Store.ExchangeCode(r.Context(), r.Form.Get("code"), r.Form.Get("client_id"), r.Form.Get("redirect_uri"), resource, r.Form.Get("code_verifier"))
	case "refresh_token":
		set, err = oauth.Store.Refresh(r.Context(), r.Form.Get("refresh_token"), r.Form.Get("client_id"), resource)
	default:
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type")
		return
	}
	if err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_grant")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"access_token": set.AccessToken, "token_type": "Bearer", "expires_in": set.ExpiresIn, "refresh_token": set.RefreshToken, "scope": set.Scope})
}

func (oauth *OAuthHTTP) Revoke(w http.ResponseWriter, r *http.Request) {
	release := oauth.allow(w, r)
	if release == nil {
		return
	}
	defer release()
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	_ = oauth.Store.Revoke(r.Context(), r.Form.Get("token"))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
}
func oauthError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

func ValidateAuthorizationRequest(store *Store, r *http.Request, resource string) (Client, string, []string, error) {
	challenge := r.URL.Query().Get("code_challenge")
	if r.URL.Query().Get("response_type") != "code" || r.URL.Query().Get("code_challenge_method") != "S256" || len(challenge) != 43 || r.URL.Query().Get("resource") != resource {
		return Client{}, "", nil, ErrInvalidGrant
	}
	client, err := store.Client(r.Context(), r.URL.Query().Get("client_id"))
	if err != nil {
		return Client{}, "", nil, err
	}
	redirect := r.URL.Query().Get("redirect_uri")
	matched := false
	for _, registered := range client.RedirectURIs {
		if ValidateRedirectURI(registered, redirect) {
			matched = true
			break
		}
	}
	if !matched {
		return Client{}, "", nil, ErrInvalidGrant
	}
	scopes := strings.Fields(r.URL.Query().Get("scope"))
	if len(scopes) == 0 {
		scopes = []string{ScopeObserve}
	}
	return client, redirect, scopes, nil
}

func onlyValues(values []string, allowed ...string) bool {
	if len(values) == 0 {
		return true
	}
	set := map[string]bool{}
	for _, value := range allowed {
		set[value] = true
	}
	for _, value := range values {
		if !set[value] {
			return false
		}
	}
	return true
}

func OAuthErrorRedirect(redirect, code, state string) string {
	if redirect == "" {
		return ""
	}
	u, err := url.Parse(redirect)
	if err != nil {
		return ""
	}
	q := u.Query()
	q.Set("error", code)
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	return u.String()
}
