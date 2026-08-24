package privilegebroker

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"scriptboard/internal/redismanager"
)

type redisWireRequest struct {
	Instance redismanager.Instance    `json:"instance"`
	Password string                   `json:"password,omitempty"`
	Scan     redismanager.ScanRequest `json:"scan"`
}
type redisWireResponse struct {
	Test     *redismanager.ConnectionTest `json:"test,omitempty"`
	Overview *redismanager.Overview       `json:"overview,omitempty"`
	Scan     *redismanager.ScanPage       `json:"scan,omitempty"`
}

type brokerRedisService struct {
	db *sql.DB
	redismanager.Backend
}

func NewBrokerRedisService(db *sql.DB, backend redismanager.Backend) RedisService {
	return &brokerRedisService{db: db, Backend: backend}
}
func (s *brokerRedisService) ValidateInstance(ctx context.Context, requested redismanager.Instance) error {
	var actual redismanager.Instance
	var configured bool
	err := s.db.QueryRowContext(ctx, `SELECT id,name,environment,host,port,username,database_index,tls_mode,ca_path,credential_configured FROM redis_instances WHERE id=?`, requested.ID).Scan(&actual.ID, &actual.Name, &actual.Environment, &actual.Host, &actual.Port, &actual.Username, &actual.Database, &actual.TLSMode, &actual.CAPath, &configured)
	actual.CredentialConfigured = configured
	if err != nil {
		return err
	}
	requested.ConnectionState = ""
	requested.CreatedAt = time.Time{}
	requested.UpdatedAt = time.Time{}
	returnEqual := actual.ID == requested.ID && actual.Name == requested.Name && actual.Environment == requested.Environment && actual.Host == requested.Host && actual.Port == requested.Port && actual.Username == requested.Username && actual.Database == requested.Database && actual.TLSMode == requested.TLSMode && actual.CAPath == requested.CAPath && actual.CredentialConfigured == requested.CredentialConfigured
	if !returnEqual {
		return errors.New("Redis instance mismatch")
	}
	return nil
}

func (s *brokerRedisService) ValidateInstanceID(ctx context.Context, id string) error {
	var exists int
	return s.db.QueryRowContext(ctx, `SELECT 1 FROM redis_instances WHERE id=?`, id).Scan(&exists)
}

func (s *Server) redisOperation(ctx context.Context, request wireRequest) wireResponse {
	if s.redis == nil {
		return wireResponse{Status: statusError, ErrorCode: "redis_unavailable", Message: "Redis service is unavailable"}
	}
	payload := request.Redis
	auditPayload := *payload
	auditPayload.Password = ""
	body, _ := json.Marshal(auditPayload)
	action, recent := redisAction(request.Operation)
	mode := domainAuthorizationCurrentPrivileged
	if recent {
		mode = domainAuthorizationRecentPrivileged
	}
	actor, err := s.authorizeActor(ctx, AuthorizationRequest{SessionToken: request.SessionToken, RequestID: request.RequestID, Action: action, Resource: payload.Instance.ID, Revision: "redis-instance-v1", ParametersSHA256: parametersDigest(body)}, mode)
	if err != nil {
		return wireResponse{Status: statusError, ErrorCode: "redis_forbidden", Message: "Redis operation is not authorized"}
	}
	if request.Operation == operationRedisDelete {
		if err = s.redis.ValidateInstanceID(ctx, payload.Instance.ID); err != nil {
			return wireResponse{Status: statusError, ErrorCode: "redis_instance_mismatch", Message: "Redis instance does not match committed metadata"}
		}
	} else {
		if err = s.redis.ValidateInstance(ctx, payload.Instance); err != nil {
			return wireResponse{Status: statusError, ErrorCode: "redis_instance_mismatch", Message: "Redis instance does not match committed metadata"}
		}
	}
	response := wireResponse{Status: statusOK, Redis: &redisWireResponse{}}
	switch request.Operation {
	case operationRedisStore:
		err = s.redis.StoreCredential(ctx, payload.Instance, payload.Password)
	case operationRedisDelete:
		err = s.redis.DeleteCredential(ctx, payload.Instance.ID)
	case operationRedisTest:
		var v redismanager.ConnectionTest
		v, err = s.redis.Test(ctx, payload.Instance)
		response.Redis.Test = &v
	case operationRedisOverview:
		var v redismanager.Overview
		v, err = s.redis.Overview(ctx, payload.Instance)
		response.Redis.Overview = &v
	case operationRedisScan:
		var v redismanager.ScanPage
		v, err = s.redis.Scan(ctx, payload.Instance, payload.Scan)
		response.Redis.Scan = &v
	}
	result := "succeeded"
	if err != nil {
		result = "failed"
	}
	if action != ActionRedisRead && s.auditor != nil {
		auditErr := s.auditor.Record(context.Background(), AuditRecord{OccurredAt: s.now().UTC(), RequestID: request.RequestID,
			Actor: actor, Action: action, Resource: payload.Instance.ID, Revision: "redis-instance-v1", ParametersSHA256: parametersDigest(body), Result: result})
		if auditErr != nil && err == nil {
			return wireResponse{Status: statusError, ErrorCode: "audit_failed_after_execution", Message: "Redis operation completed but result audit failed"}
		}
	}
	if err != nil {
		return wireResponse{Status: statusError, ErrorCode: "redis_failed", Message: "Redis operation failed"}
	}
	return response
}

func redisAction(operation string) (Action, bool) {
	switch operation {
	case operationRedisStore:
		return ActionRedisStore, true
	case operationRedisDelete:
		return ActionRedisDelete, true
	default:
		return ActionRedisRead, false
	}
}

func isRedisOperation(operation string) bool {
	switch operation {
	case operationRedisStore, operationRedisDelete, operationRedisTest, operationRedisOverview, operationRedisScan:
		return true
	}
	return false
}
func validateRedisRequest(request wireRequest) error {
	if request.Redis == nil || request.Capability != "" || request.Action != "" || request.Resource != "" || request.Revision != "" || request.ParametersSHA256 != "" || len(request.Parameters) != 0 || request.MySQL != nil || request.HostFiles != nil || request.StateBackup != nil || request.Registry != nil || request.Kubeconfig != nil || hasMFAFields(request) || hasPasskeyFields(request) || hasProviderFields(request) || hasRemoteWebsiteFields(request) {
		return errors.New("Redis request contains unrelated fields")
	}
	p := request.Redis
	minimal := request.Operation == operationRedisDelete
	if !validCredentialSessionToken(request.SessionToken) || (!minimal && !validRedisInstance(p.Instance)) || (minimal && !validRemoteWebsiteID(p.Instance.ID)) || len(p.Password) > 8<<10 || len(p.Scan.Pattern) > 512 || len(p.Scan.Type) > 32 || strings.ContainsAny(p.Scan.Pattern+p.Scan.Type, "\r\n\x00") {
		return errors.New("Redis request is invalid")
	}
	if request.Operation != operationRedisStore && p.Password != "" {
		return errors.New("Redis password is unrelated")
	}
	if request.Operation != operationRedisScan && p.Scan != (redismanager.ScanRequest{}) {
		return errors.New("Redis scan is unrelated")
	}
	return nil
}

func validRedisInstance(instance redismanager.Instance) bool {
	validTLS := instance.TLSMode == redismanager.TLSDisabled || instance.TLSMode == redismanager.TLSVerifyIdentity || instance.TLSMode == redismanager.TLSInsecureSkipVerify
	validEnvironment := instance.Environment == redismanager.EnvironmentProduction || instance.Environment == redismanager.EnvironmentDevelopment || instance.Environment == redismanager.EnvironmentUnspecified
	return validRemoteWebsiteID(instance.ID) && instance.Name != "" && len(instance.Name) <= 160 && instance.Host != "" && len(instance.Host) <= 253 && instance.Port >= 1 && instance.Port <= 65535 && instance.Database >= 0 && instance.Database <= 1<<20 && len(instance.Username) <= 256 && len(instance.CAPath) <= 4096 && validTLS && validEnvironment && instance.CredentialConfigured && !strings.ContainsAny(instance.Name+instance.Host+instance.Username+instance.CAPath, "\r\n\x00")
}

type RedisBackend struct{ client *Client }

func NewRedisBackend(client *Client) *RedisBackend { return &RedisBackend{client: client} }
func (b *RedisBackend) call(ctx context.Context, op string, p redisWireRequest) (redisWireResponse, error) {
	if b == nil || b.client == nil {
		return redisWireResponse{}, errors.New("privileged Broker Redis service is unavailable")
	}
	auth, ok := AuthorizationFromContext(ctx)
	if !ok {
		return redisWireResponse{}, errors.New("privileged Broker Redis authorization is missing")
	}
	response, err := b.client.call(ctx, wireRequest{Version: ProtocolVersion, Operation: op, RequestID: auth.RequestID, SessionToken: auth.SessionToken, Redis: &p})
	if err != nil {
		return redisWireResponse{}, err
	}
	if response.Redis == nil {
		return redisWireResponse{}, errors.New("privileged Broker returned invalid Redis response")
	}
	return *response.Redis, nil
}
func (b *RedisBackend) StoreCredential(ctx context.Context, i redismanager.Instance, p string) error {
	_, e := b.call(ctx, operationRedisStore, redisWireRequest{Instance: i, Password: p})
	return e
}
func (b *RedisBackend) DeleteCredential(ctx context.Context, id string) error {
	_, e := b.call(ctx, operationRedisDelete, redisWireRequest{Instance: redismanager.Instance{ID: id}})
	return e
}
func (b *RedisBackend) Test(ctx context.Context, i redismanager.Instance) (redismanager.ConnectionTest, error) {
	v, e := b.call(ctx, operationRedisTest, redisWireRequest{Instance: i})
	if v.Test == nil {
		return redismanager.ConnectionTest{}, errors.Join(e, errors.New("missing Redis test"))
	}
	return *v.Test, e
}
func (b *RedisBackend) Overview(ctx context.Context, i redismanager.Instance) (redismanager.Overview, error) {
	v, e := b.call(ctx, operationRedisOverview, redisWireRequest{Instance: i})
	if v.Overview == nil {
		return redismanager.Overview{}, errors.Join(e, errors.New("missing Redis overview"))
	}
	return *v.Overview, e
}
func (b *RedisBackend) Scan(ctx context.Context, i redismanager.Instance, r redismanager.ScanRequest) (redismanager.ScanPage, error) {
	v, e := b.call(ctx, operationRedisScan, redisWireRequest{Instance: i, Scan: r})
	if v.Scan == nil {
		return redismanager.ScanPage{}, errors.Join(e, errors.New("missing Redis scan"))
	}
	return *v.Scan, e
}

var _ redismanager.Backend = (*RedisBackend)(nil)
