package privilegebroker

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"scriptboard/internal/auditlog"
)

const (
	brokerRecentAuthenticationWindow = 10 * time.Minute
	brokerIdleSessionWindow          = 12 * time.Hour
)

type DatabaseSecurity struct {
	db     *sql.DB
	audit  *auditlog.Store
	now    func() time.Time
	anchor interface {
		Write(context.Context, *auditlog.Store, time.Time) error
	}
}

func (security *DatabaseSecurity) SetAuditAnchor(anchor interface {
	Write(context.Context, *auditlog.Store, time.Time) error
}) {
	security.anchor = anchor
}

func NewDatabaseSecurity(db *sql.DB, audit *auditlog.Store, now func() time.Time) (*DatabaseSecurity, error) {
	if db == nil || audit == nil {
		return nil, errors.New("privileged Broker database and audit store are required")
	}
	if now == nil {
		now = time.Now
	}
	return &DatabaseSecurity{db: db, audit: audit, now: now}, nil
}

func (security *DatabaseSecurity) Authorize(ctx context.Context, request AuthorizationRequest) (Actor, error) {
	actor, authVersion, userAuthVersion, assurance, reauthenticatedAt, _, err := security.sessionActor(ctx, request.SessionToken)
	if err != nil {
		return Actor{}, err
	}
	now := security.now().UTC()
	reauthenticated := time.Unix(reauthenticatedAt, 0)
	if authVersion != userAuthVersion || (actor.Role != "administrator" && actor.Role != "maintainer") ||
		assurance < 1 || reauthenticatedAt <= 0 || reauthenticated.After(now.Add(time.Minute)) || now.Sub(reauthenticated) > brokerRecentAuthenticationWindow {
		return Actor{}, errors.New("privileged Broker session is not authorized for a recent system mutation")
	}
	actor.AuthenticationAssurance = assurance
	actor.RecentAuthentication = true
	return actor, nil
}

func (security *DatabaseSecurity) AuthorizeSession(ctx context.Context, request AuthorizationRequest) (Actor, error) {
	actor, authVersion, userAuthVersion, assurance, _, _, err := security.sessionActor(ctx, request.SessionToken)
	if err != nil {
		return Actor{}, err
	}
	if authVersion != userAuthVersion || assurance < 1 {
		return Actor{}, errors.New("privileged Broker session is not authorized")
	}
	actor.AuthenticationAssurance = assurance
	return actor, nil
}

func (security *DatabaseSecurity) sessionActor(ctx context.Context, sessionToken string) (Actor, int64, int64, int, int64, int64, error) {
	digest := sha256.Sum256([]byte(sessionToken))
	tokenHash := hex.EncodeToString(digest[:])
	var actor Actor
	var authVersion, userAuthVersion int64
	var assurance int
	var reauthenticatedAt, lastSeenAt, expiresAt int64
	err := security.db.QueryRowContext(ctx, `SELECT users.id, users.username, users.role,
		sessions.auth_version, users.auth_version, sessions.authentication_assurance,
		sessions.reauthenticated_at, sessions.last_seen_at, sessions.expires_at
		FROM sessions JOIN users ON users.id = sessions.user_id
		WHERE sessions.token_hash = ? AND users.enabled = 1`, tokenHash).Scan(
		&actor.UserID, &actor.Username, &actor.Role, &authVersion, &userAuthVersion, &assurance,
		&reauthenticatedAt, &lastSeenAt, &expiresAt,
	)
	if err != nil {
		return Actor{}, 0, 0, 0, 0, 0, errors.New("privileged Broker session is invalid")
	}
	now := security.now().UTC()
	if now.Unix() >= expiresAt || now.Sub(time.Unix(lastSeenAt, 0)) >= brokerIdleSessionWindow {
		return Actor{}, 0, 0, 0, 0, 0, errors.New("privileged Broker session is expired")
	}
	return actor, authVersion, userAuthVersion, assurance, reauthenticatedAt, lastSeenAt, nil
}

func (security *DatabaseSecurity) Record(ctx context.Context, record AuditRecord) error {
	authenticationAssurance := "aal" + strconv.Itoa(record.Actor.AuthenticationAssurance)
	if record.Actor.RecentAuthentication {
		authenticationAssurance += "+step-up"
	}
	_, err := security.audit.Append(ctx, auditlog.Event{
		OccurredAt: strconv.FormatInt(record.OccurredAt.UTC().Unix(), 10),
		Action:     "privileged_broker." + string(record.Action), Target: record.Resource, Result: record.Result,
		SourceAddress: "local-privileged-broker", ActorUserID: record.Actor.UserID,
		ActorUsername: record.Actor.Username, ActorRole: record.Actor.Role, RequestID: record.RequestID,
		AuthenticationAssurance: authenticationAssurance,
		ResourceRevision:        record.Revision, ResourceDigestSHA256: record.ParametersSHA256,
	})
	if err != nil {
		return fmt.Errorf("append privileged Broker audit: %w", err)
	}
	if security.anchor != nil {
		if err := security.anchor.Write(ctx, security.audit, record.OccurredAt.UTC()); err != nil {
			return fmt.Errorf("refresh privileged Broker audit checkpoint: %w", err)
		}
	}
	return nil
}
