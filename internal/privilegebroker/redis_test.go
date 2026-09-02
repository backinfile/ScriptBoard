package privilegebroker

import (
	"strings"
	"testing"

	"scriptboard/internal/mysqlmanager"
	"scriptboard/internal/redismanager"
)

func TestRedisProtocolAcceptsExplicitTransportModesAndRejectsFieldSmuggling(t *testing.T) {
	for _, mode := range []redismanager.TLSMode{redismanager.TLSDisabled, redismanager.TLSVerifyIdentity, redismanager.TLSInsecureSkipVerify} {
		request := wireRequest{Version: ProtocolVersion, Operation: operationRedisStore, RequestID: "redis-protocol", SessionToken: strings.Repeat("s", 32), Redis: &redisWireRequest{Instance: redismanager.Instance{ID: "instance-one", Name: "cache", Environment: redismanager.EnvironmentProduction, Host: "redis.internal", Port: 6379, TLSMode: mode, CredentialConfigured: true}, Password: "secret"}}
		if err := validateRedisRequest(request); err != nil {
			t.Fatalf("mode %s rejected: %v", mode, err)
		}
		request.MySQL = &mysqlWireRequest{Instance: mysqlmanager.Instance{ID: "smuggled"}}
		if err := validateRedisRequest(request); err == nil {
			t.Fatalf("mode %s accepted unrelated MySQL fields", mode)
		}
	}
}

func TestRedisProtocolScopesDatabaseSelectionToReadOperations(t *testing.T) {
	instance := redismanager.Instance{ID: "instance-one", Name: "cache", Environment: redismanager.EnvironmentProduction, Host: "redis.internal", Port: 6379, TLSMode: redismanager.TLSDisabled, CredentialConfigured: true}
	overview := wireRequest{Version: ProtocolVersion, Operation: operationRedisOverview, RequestID: "redis-database", SessionToken: strings.Repeat("s", 32), Redis: &redisWireRequest{Instance: instance, Database: 7}}
	if err := validateRedisRequest(overview); err != nil {
		t.Fatalf("operation-scoped Redis database rejected: %v", err)
	}
	store := wireRequest{Version: ProtocolVersion, Operation: operationRedisStore, RequestID: "redis-store", SessionToken: strings.Repeat("s", 32), Redis: &redisWireRequest{Instance: instance, Password: "secret", Database: 7}}
	if err := validateRedisRequest(store); err == nil {
		t.Fatal("Redis connection storage accepted a database selection")
	}
}
