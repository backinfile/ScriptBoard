package mcpaccess

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"
	"time"

	"scriptboard/internal/identity"
)

var (
	ErrInvalidClient = errors.New("invalid OAuth client")
	ErrInvalidGrant  = errors.New("invalid OAuth grant")
	ErrInvalidToken  = errors.New("invalid OAuth token")
)

type Client struct {
	ID, ClientID, Name, RegistrationType, MetadataURI string
	RedirectURIs                                      []string
	Revoked                                           bool
}
type Principal struct {
	UserID, Username, ClientID, Role string
	Scopes                           map[string]bool
	AuthVersion                      int
}
type AuthorizationView struct {
	ID         string
	Username   string
	ClientID   string
	ClientName string
	Scopes     string
	UpdatedAt  time.Time
}
type ClientView struct {
	ID, ClientID, Name, RegistrationType string
	RedirectURIs                         []string
	Revoked                              bool
}
type TokenSet struct {
	AccessToken, RefreshToken, Scope string
	ExpiresIn                        int
}
type Store struct {
	db        *sql.DB
	now       func() time.Time
	lifecycle func(LifecycleEvent)
}
type LifecycleEvent struct{ Action, Target, Result, UserID, ClientID string }

func (store *Store) SetLifecycleObserver(observer func(LifecycleEvent)) { store.lifecycle = observer }
func (store *Store) observe(event LifecycleEvent) {
	if store.lifecycle != nil {
		store.lifecycle(event)
	}
}

func NewStore(db *sql.DB, now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{db: db, now: now}
}

func randomValue(bytes int) (string, error) {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func tokenHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func tokenHint(value string) string {
	if len(value) <= 8 {
		return value
	}
	return value[:4] + "…" + value[len(value)-4:]
}
func scopesString(scopes []string) string { sort.Strings(scopes); return strings.Join(scopes, " ") }
func parseScopes(value string) map[string]bool {
	result := map[string]bool{}
	for _, scope := range strings.Fields(value) {
		result[scope] = true
	}
	return result
}

func NormalizeScopes(role string, requested []string) ([]string, error) {
	wants := map[string]bool{}
	for _, scope := range requested {
		wants[scope] = true
	}
	if wants[ScopeExecute] {
		if role != string(identity.RoleOperator) && role != string(identity.RoleMaintainer) && role != string(identity.RoleAdministrator) {
			return nil, ErrInvalidGrant
		}
		wants[ScopeObserve] = true
	}
	if len(wants) == 0 {
		wants[ScopeObserve] = true
	}
	for scope := range wants {
		if scope != ScopeObserve && scope != ScopeExecute {
			return nil, ErrInvalidGrant
		}
	}
	result := make([]string, 0, len(wants))
	for scope := range wants {
		result = append(result, scope)
	}
	sort.Strings(result)
	return result, nil
}

func ValidateRedirectURI(registered, requested string) bool {
	r, err1 := url.Parse(registered)
	q, err2 := url.Parse(requested)
	if err1 != nil || err2 != nil || r.User != nil || q.User != nil || r.Fragment != "" || q.Fragment != "" {
		return false
	}
	registeredIP := net.ParseIP(r.Hostname())
	loopback := strings.EqualFold(r.Hostname(), "localhost") || registeredIP != nil && registeredIP.IsLoopback()
	if loopback && r.Scheme == "http" {
		return q.Scheme == r.Scheme && strings.EqualFold(q.Hostname(), r.Hostname()) && q.EscapedPath() == r.EscapedPath() && q.RawQuery == r.RawQuery
	}
	return r.Scheme == "https" && requested == registered
}

func (store *Store) RegisterClient(ctx context.Context, name string, redirects []string, registrationType, metadataURI string) (Client, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 128 || len(redirects) == 0 || len(redirects) > 20 {
		return Client{}, ErrInvalidClient
	}
	for _, redirect := range redirects {
		if !ValidateRedirectURI(redirect, redirect) {
			return Client{}, ErrInvalidClient
		}
	}
	id, err := randomValue(18)
	if err != nil {
		return Client{}, err
	}
	clientID := id
	if registrationType == "cimd" {
		clientID = metadataURI
	}
	body, _ := json.Marshal(redirects)
	now := store.now().UTC().UnixNano()
	_, err = store.db.ExecContext(ctx, `INSERT INTO mcp_oauth_clients(id,client_id,registration_type,name,redirect_uris_json,metadata_uri,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, id, clientID, registrationType, strings.TrimSpace(name), string(body), metadataURI, now, now)
	if err != nil {
		return Client{}, err
	}
	store.observe(LifecycleEvent{Action: "mcp_oauth_client_register", Target: clientID, Result: "succeeded", ClientID: clientID})
	return Client{ID: id, ClientID: clientID, Name: name, RegistrationType: registrationType, MetadataURI: metadataURI, RedirectURIs: redirects}, nil
}

func (store *Store) RegisterPredefinedClient(ctx context.Context, clientID, name string, redirects []string) (Client, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" || len(clientID) > 256 || len(redirects) == 0 || len(redirects) > 20 {
		return Client{}, ErrInvalidClient
	}
	for _, redirect := range redirects {
		if !ValidateRedirectURI(redirect, redirect) {
			return Client{}, ErrInvalidClient
		}
	}
	id, err := randomValue(18)
	if err != nil {
		return Client{}, err
	}
	body, _ := json.Marshal(redirects)
	now := store.now().UTC().UnixNano()
	if _, err := store.db.ExecContext(ctx, `INSERT INTO mcp_oauth_clients(id,client_id,registration_type,name,redirect_uris_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, id, clientID, "pre_registered", strings.TrimSpace(name), string(body), now, now); err != nil {
		return Client{}, err
	}
	store.observe(LifecycleEvent{Action: "mcp_oauth_client_register", Target: clientID, Result: "succeeded", ClientID: clientID})
	return Client{ID: id, ClientID: clientID, Name: name, RegistrationType: "pre_registered", RedirectURIs: redirects}, nil
}
func (store *Store) Clients(ctx context.Context) ([]ClientView, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT id,client_id,name,registration_type,redirect_uris_json,revoked_at IS NOT NULL FROM mcp_oauth_clients ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ClientView{}
	for rows.Next() {
		var item ClientView
		var raw string
		if err := rows.Scan(&item.ID, &item.ClientID, &item.Name, &item.RegistrationType, &raw, &item.Revoked); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(raw), &item.RedirectURIs)
		result = append(result, item)
	}
	return result, rows.Err()
}
func (store *Store) RevokeClient(ctx context.Context, id string) error {
	now := store.now().UTC().UnixNano()
	result, err := store.db.ExecContext(ctx, `UPDATE mcp_oauth_clients SET revoked_at=?,updated_at=? WHERE id=? AND revoked_at IS NULL`, now, now, id)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return ErrInvalidClient
	}
	_, _ = store.db.ExecContext(ctx, `UPDATE mcp_oauth_authorizations SET revoked_at=?,updated_at=? WHERE client_id=(SELECT client_id FROM mcp_oauth_clients WHERE id=?) AND revoked_at IS NULL`, now, now, id)
	return nil
}

func (store *Store) Client(ctx context.Context, clientID string) (Client, error) {
	client, err := store.clientFromDB(ctx, clientID)
	if err == nil {
		return client, nil
	}
	if errors.Is(err, sql.ErrNoRows) && strings.HasPrefix(clientID, "https://") {
		return store.resolveCIMD(ctx, clientID)
	}
	return Client{}, ErrInvalidClient
}

func (store *Store) clientFromDB(ctx context.Context, clientID string) (Client, error) {
	var c Client
	var redirects string
	var revoked sql.NullInt64
	err := store.db.QueryRowContext(ctx, `SELECT id,client_id,name,registration_type,metadata_uri,redirect_uris_json,revoked_at FROM mcp_oauth_clients WHERE client_id=?`, clientID).Scan(&c.ID, &c.ClientID, &c.Name, &c.RegistrationType, &c.MetadataURI, &redirects, &revoked)
	if err != nil {
		return Client{}, err
	}
	if revoked.Valid {
		return Client{}, ErrInvalidClient
	}
	if json.Unmarshal([]byte(redirects), &c.RedirectURIs) != nil {
		return Client{}, ErrInvalidClient
	}
	return c, nil
}

func (store *Store) IssueCode(ctx context.Context, userID, clientID, redirectURI, resource, challenge string, requested []string) (string, error) {
	client, err := store.Client(ctx, clientID)
	if err != nil {
		return "", err
	}
	matched := false
	for _, registered := range client.RedirectURIs {
		if ValidateRedirectURI(registered, redirectURI) {
			matched = true
			break
		}
	}
	if !matched || challenge == "" || resource == "" {
		return "", ErrInvalidGrant
	}
	var role string
	var authVersion int
	var enabled bool
	if err := store.db.QueryRowContext(ctx, `SELECT role,auth_version,enabled FROM users WHERE id=?`, userID).Scan(&role, &authVersion, &enabled); err != nil || !enabled {
		return "", ErrInvalidGrant
	}
	scopes, err := NormalizeScopes(role, requested)
	if err != nil {
		return "", err
	}
	scope := scopesString(scopes)
	now := store.now().UTC()
	authorizationID, _ := randomValue(18)
	_, err = store.db.ExecContext(ctx, `INSERT INTO mcp_oauth_authorizations(id,user_id,client_id,scopes,auth_version,created_at,updated_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(user_id,client_id) DO UPDATE SET scopes=excluded.scopes,auth_version=excluded.auth_version,updated_at=excluded.updated_at,revoked_at=NULL`, authorizationID, userID, clientID, scope, authVersion, now.UnixNano(), now.UnixNano())
	if err != nil {
		return "", err
	}
	if err := store.db.QueryRowContext(ctx, `SELECT id FROM mcp_oauth_authorizations WHERE user_id=? AND client_id=?`, userID, clientID).Scan(&authorizationID); err != nil {
		return "", err
	}
	code, _ := randomValue(32)
	_, err = store.db.ExecContext(ctx, `INSERT INTO mcp_oauth_codes(token_hash,authorization_id,client_id,redirect_uri,resource,code_challenge,scopes,expires_at) VALUES(?,?,?,?,?,?,?,?)`, tokenHash(code), authorizationID, clientID, redirectURI, resource, challenge, scope, now.Add(5*time.Minute).UnixNano())
	if err == nil {
		store.observe(LifecycleEvent{Action: "mcp_oauth_code_issue", Target: clientID, Result: "succeeded", UserID: userID, ClientID: clientID})
	}
	return code, err
}

func verifyPKCE(verifier, challenge string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	actual := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(actual), []byte(challenge)) == 1
}

func (store *Store) ExchangeCode(ctx context.Context, code, clientID, redirectURI, resource, verifier string) (TokenSet, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return TokenSet{}, err
	}
	defer tx.Rollback()
	now := store.now().UTC()
	var authorizationID, storedClient, storedRedirect, storedResource, challenge, scope string
	var expires int64
	var consumed sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT authorization_id,client_id,redirect_uri,resource,code_challenge,scopes,expires_at,consumed_at FROM mcp_oauth_codes WHERE token_hash=?`, tokenHash(code)).Scan(&authorizationID, &storedClient, &storedRedirect, &storedResource, &challenge, &scope, &expires, &consumed)
	if err != nil || consumed.Valid || now.UnixNano() >= expires || storedClient != clientID || storedRedirect != redirectURI || storedResource != resource || !verifyPKCE(verifier, challenge) {
		return TokenSet{}, ErrInvalidGrant
	}
	if result, err := tx.ExecContext(ctx, `UPDATE mcp_oauth_codes SET consumed_at=? WHERE token_hash=? AND consumed_at IS NULL`, now.UnixNano(), tokenHash(code)); err != nil {
		return TokenSet{}, err
	} else if n, _ := result.RowsAffected(); n != 1 {
		return TokenSet{}, ErrInvalidGrant
	}
	var userID, role string
	var authVersion, currentVersion int
	var enabled bool
	if err := tx.QueryRowContext(ctx, `SELECT a.user_id,a.auth_version,u.auth_version,u.role,u.enabled FROM mcp_oauth_authorizations a JOIN users u ON u.id=a.user_id JOIN mcp_oauth_clients c ON c.client_id=a.client_id WHERE a.id=? AND a.revoked_at IS NULL AND c.revoked_at IS NULL`, authorizationID).Scan(&userID, &authVersion, &currentVersion, &role, &enabled); err != nil || !enabled || authVersion != currentVersion {
		return TokenSet{}, ErrInvalidGrant
	}
	if _, err := NormalizeScopes(role, strings.Fields(scope)); err != nil {
		return TokenSet{}, ErrInvalidGrant
	}
	set, err := issueTokens(ctx, tx, now, authorizationID, userID, clientID, scope, resource, authVersion, "")
	if err != nil {
		return TokenSet{}, err
	}
	if err := tx.Commit(); err != nil {
		return TokenSet{}, err
	}
	store.observe(LifecycleEvent{Action: "mcp_oauth_token_exchange", Target: clientID, Result: "succeeded", UserID: userID, ClientID: clientID})
	return set, nil
}

func issueTokens(ctx context.Context, tx *sql.Tx, now time.Time, authorizationID, userID, clientID, scope, resource string, authVersion int, familyID string) (TokenSet, error) {
	if familyID == "" {
		familyID, _ = randomValue(18)
		_, err := tx.ExecContext(ctx, `INSERT INTO mcp_oauth_token_families(id,authorization_id,client_id,absolute_expires_at,created_at) VALUES(?,?,?,?,?)`, familyID, authorizationID, clientID, now.Add(30*24*time.Hour).UnixNano(), now.UnixNano())
		if err != nil {
			return TokenSet{}, err
		}
	}
	access, _ := randomValue(32)
	refresh, _ := randomValue(40)
	accessExpiry := now.Add(10 * time.Minute)
	refreshExpiry := now.Add(30 * 24 * time.Hour)
	if _, err := tx.ExecContext(ctx, `INSERT INTO mcp_oauth_access_tokens(token_hash,token_hint,family_id,user_id,client_id,scopes,resource,auth_version,expires_at,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, tokenHash(access), tokenHint(access), familyID, userID, clientID, scope, resource, authVersion, accessExpiry.UnixNano(), now.UnixNano()); err != nil {
		return TokenSet{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO mcp_oauth_refresh_tokens(token_hash,token_hint,family_id,user_id,client_id,scopes,resource,auth_version,expires_at,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, tokenHash(refresh), tokenHint(refresh), familyID, userID, clientID, scope, resource, authVersion, refreshExpiry.UnixNano(), now.UnixNano()); err != nil {
		return TokenSet{}, err
	}
	return TokenSet{AccessToken: access, RefreshToken: refresh, Scope: scope, ExpiresIn: 600}, nil
}

func (store *Store) Authenticate(ctx context.Context, token, resource string) (Principal, error) {
	now := store.now().UTC().UnixNano()
	var p Principal
	var scope, storedResource string
	var tokenAuth, userAuth int
	var expires int64
	var tokenRevoked, familyRevoked, authRevoked, clientRevoked sql.NullInt64
	var enabled bool
	err := store.db.QueryRowContext(ctx, `SELECT t.user_id,u.username,t.client_id,u.role,t.scopes,t.resource,t.auth_version,u.auth_version,t.expires_at,t.revoked_at,f.revoked_at,a.revoked_at,c.revoked_at,u.enabled FROM mcp_oauth_access_tokens t JOIN mcp_oauth_token_families f ON f.id=t.family_id JOIN mcp_oauth_authorizations a ON a.id=f.authorization_id JOIN mcp_oauth_clients c ON c.client_id=t.client_id JOIN users u ON u.id=t.user_id WHERE t.token_hash=?`, tokenHash(token)).Scan(&p.UserID, &p.Username, &p.ClientID, &p.Role, &scope, &storedResource, &tokenAuth, &userAuth, &expires, &tokenRevoked, &familyRevoked, &authRevoked, &clientRevoked, &enabled)
	if err != nil || !enabled || now >= expires || resource != storedResource || tokenAuth != userAuth || tokenRevoked.Valid || familyRevoked.Valid || authRevoked.Valid || clientRevoked.Valid {
		return Principal{}, ErrInvalidToken
	}
	p.AuthVersion = userAuth
	p.Scopes = parseScopes(scope)
	normalized, err := NormalizeScopes(p.Role, strings.Fields(scope))
	if err != nil {
		return Principal{}, ErrInvalidToken
	}
	p.Scopes = parseScopes(scopesString(normalized))
	return p, nil
}

func (store *Store) Refresh(ctx context.Context, refresh, clientID, resource string) (TokenSet, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return TokenSet{}, err
	}
	defer tx.Rollback()
	now := store.now().UTC()
	var familyID, userID, storedClient, scope, storedResource string
	var authVersion, expires, absolute int64
	var consumed, familyRevoked sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT r.family_id,r.user_id,r.client_id,r.scopes,r.resource,r.auth_version,r.expires_at,r.consumed_at,f.absolute_expires_at,f.revoked_at FROM mcp_oauth_refresh_tokens r JOIN mcp_oauth_token_families f ON f.id=r.family_id WHERE r.token_hash=?`, tokenHash(refresh)).Scan(&familyID, &userID, &storedClient, &scope, &storedResource, &authVersion, &expires, &consumed, &absolute, &familyRevoked)
	if err != nil {
		return TokenSet{}, ErrInvalidGrant
	}
	if consumed.Valid {
		_, _ = tx.ExecContext(ctx, `UPDATE mcp_oauth_token_families SET revoked_at=? WHERE id=?`, now.UnixNano(), familyID)
		_ = tx.Commit()
		store.observe(LifecycleEvent{Action: "mcp_oauth_refresh_reuse", Target: storedClient, Result: "blocked", UserID: userID, ClientID: storedClient})
		return TokenSet{}, ErrInvalidGrant
	}
	if familyRevoked.Valid || now.UnixNano() >= expires || now.UnixNano() >= absolute || storedClient != clientID || storedResource != resource {
		return TokenSet{}, ErrInvalidGrant
	}
	var activeGrant int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM mcp_oauth_token_families f JOIN mcp_oauth_authorizations a ON a.id=f.authorization_id JOIN mcp_oauth_clients c ON c.client_id=f.client_id WHERE f.id=? AND f.revoked_at IS NULL AND a.revoked_at IS NULL AND c.revoked_at IS NULL`, familyID).Scan(&activeGrant); err != nil || activeGrant != 1 {
		return TokenSet{}, ErrInvalidGrant
	}
	var currentVersion int
	var role string
	var enabled bool
	if err := tx.QueryRowContext(ctx, `SELECT auth_version,role,enabled FROM users WHERE id=?`, userID).Scan(&currentVersion, &role, &enabled); err != nil || !enabled || int64(currentVersion) != authVersion {
		return TokenSet{}, ErrInvalidGrant
	}
	if _, err := NormalizeScopes(role, strings.Fields(scope)); err != nil {
		return TokenSet{}, ErrInvalidGrant
	}
	if _, err := tx.ExecContext(ctx, `UPDATE mcp_oauth_refresh_tokens SET consumed_at=? WHERE token_hash=?`, now.UnixNano(), tokenHash(refresh)); err != nil {
		return TokenSet{}, err
	}
	var authorizationID string
	if err := tx.QueryRowContext(ctx, `SELECT authorization_id FROM mcp_oauth_token_families WHERE id=?`, familyID).Scan(&authorizationID); err != nil {
		return TokenSet{}, err
	}
	set, err := issueTokens(ctx, tx, now, authorizationID, userID, clientID, scope, resource, currentVersion, familyID)
	if err != nil {
		return TokenSet{}, err
	}
	if err := tx.Commit(); err != nil {
		return TokenSet{}, err
	}
	store.observe(LifecycleEvent{Action: "mcp_oauth_refresh", Target: clientID, Result: "succeeded", UserID: userID, ClientID: clientID})
	return set, nil
}

func (store *Store) Revoke(ctx context.Context, token string) error {
	now := store.now().UTC().UnixNano()
	hash := tokenHash(token)
	_, _ = store.db.ExecContext(ctx, `UPDATE mcp_oauth_access_tokens SET revoked_at=? WHERE token_hash=?`, now, hash)
	_, _ = store.db.ExecContext(ctx, `UPDATE mcp_oauth_token_families SET revoked_at=? WHERE id IN (SELECT family_id FROM mcp_oauth_refresh_tokens WHERE token_hash=?)`, now, hash)
	return nil
}

func (store *Store) RecordInvocation(ctx context.Context, p Principal, tool, target, digest, result, requestID string) error {
	_, err := store.db.ExecContext(ctx, `INSERT INTO mcp_invocations(occurred_at,user_id,client_id,tool_name,target,parameter_digest,result,request_id) VALUES(?,?,?,?,?,?,?,?)`, store.now().UTC().UnixNano(), p.UserID, p.ClientID, tool, target, digest, result, requestID)
	return err
}

func (store *Store) Authorizations(ctx context.Context, userID string) ([]AuthorizationView, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT a.id,a.client_id,c.name,a.scopes,a.updated_at FROM mcp_oauth_authorizations a JOIN mcp_oauth_clients c ON c.client_id=a.client_id WHERE a.user_id=? AND a.revoked_at IS NULL AND c.revoked_at IS NULL ORDER BY a.updated_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []AuthorizationView{}
	for rows.Next() {
		var item AuthorizationView
		var updated int64
		if err := rows.Scan(&item.ID, &item.ClientID, &item.ClientName, &item.Scopes, &updated); err != nil {
			return nil, err
		}
		item.UpdatedAt = time.Unix(0, updated).UTC()
		result = append(result, item)
	}
	return result, rows.Err()
}
func (store *Store) RevokeAuthorization(ctx context.Context, userID, id string) error {
	now := store.now().UTC().UnixNano()
	result, err := store.db.ExecContext(ctx, `UPDATE mcp_oauth_authorizations SET revoked_at=?,updated_at=? WHERE id=? AND user_id=? AND revoked_at IS NULL`, now, now, id, userID)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return ErrInvalidGrant
	}
	_, _ = store.db.ExecContext(ctx, `UPDATE mcp_oauth_token_families SET revoked_at=? WHERE authorization_id=?`, now, id)
	return nil
}

func (store *Store) AllAuthorizations(ctx context.Context) ([]AuthorizationView, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT a.id,u.username,a.client_id,c.name,a.scopes,a.updated_at FROM mcp_oauth_authorizations a JOIN users u ON u.id=a.user_id JOIN mcp_oauth_clients c ON c.client_id=a.client_id WHERE a.revoked_at IS NULL ORDER BY a.updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []AuthorizationView{}
	for rows.Next() {
		var item AuthorizationView
		var updated int64
		if err := rows.Scan(&item.ID, &item.Username, &item.ClientID, &item.ClientName, &item.Scopes, &updated); err != nil {
			return nil, err
		}
		item.UpdatedAt = time.Unix(0, updated).UTC()
		result = append(result, item)
	}
	return result, rows.Err()
}

func (store *Store) RevokeAuthorizationByID(ctx context.Context, id string) error {
	now := store.now().UTC().UnixNano()
	result, err := store.db.ExecContext(ctx, `UPDATE mcp_oauth_authorizations SET revoked_at=?,updated_at=? WHERE id=? AND revoked_at IS NULL`, now, now, id)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return ErrInvalidGrant
	}
	_, _ = store.db.ExecContext(ctx, `UPDATE mcp_oauth_token_families SET revoked_at=? WHERE authorization_id=?`, now, id)
	return nil
}

func ParameterDigest(value []byte) string {
	sum := sha256.Sum256(value)
	return fmt.Sprintf("sha256:%x", sum[:])
}
