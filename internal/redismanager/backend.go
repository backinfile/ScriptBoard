package redismanager

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	redis "github.com/redis/go-redis/v9"
	"scriptboard/internal/secretstore"
)

type credentialRecord struct {
	Password       string
	Host           string
	Port, Database int
	Username       string
	TLSMode        TLSMode
	CAPath         string
}
type credentialStore struct {
	path  string
	vault *secretstore.Store
	mu    sync.Mutex
}

func newCredentialStore(root string, vault *secretstore.Store) *credentialStore {
	return &credentialStore{path: filepath.Join(root, "secrets", "redis-credentials.v1.enc"), vault: vault}
}
func (s *credentialStore) load() (map[string]credentialRecord, error) {
	body, e := os.ReadFile(s.path)
	if errors.Is(e, os.ErrNotExist) {
		return map[string]credentialRecord{}, nil
	}
	if e != nil {
		return nil, e
	}
	plain, e := s.vault.Unseal("redis-credentials-v1", body)
	if e != nil {
		return nil, e
	}
	v := map[string]credentialRecord{}
	e = json.Unmarshal(plain, &v)
	return v, e
}
func (s *credentialStore) write(v map[string]credentialRecord) error {
	plain, e := json.Marshal(v)
	if e != nil {
		return e
	}
	body, e := s.vault.Seal("redis-credentials-v1", plain)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(s.path), 0700); e != nil {
		return e
	}
	temporary, e := os.CreateTemp(filepath.Dir(s.path), ".redis-secret-*")
	if e != nil {
		return e
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if e = temporary.Chmod(0600); e == nil {
		_, e = temporary.Write(body)
	}
	if e == nil {
		e = temporary.Sync()
	}
	closeErr := temporary.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(temporaryPath, s.path)
}
func (s *credentialStore) set(i Instance, p string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, e := s.load()
	if e != nil {
		return e
	}
	v[i.ID] = credentialRecord{Password: p, Host: i.Host, Port: i.Port, Database: i.Database, Username: i.Username, TLSMode: i.TLSMode, CAPath: i.CAPath}
	return s.write(v)
}
func (s *credentialStore) delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, e := s.load()
	if e != nil {
		return e
	}
	delete(v, id)
	return s.write(v)
}
func (s *credentialStore) get(i Instance) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, e := s.load()
	if e != nil {
		return "", e
	}
	r, ok := v[i.ID]
	if !ok {
		return "", errors.New("Redis credential is unavailable")
	}
	if r.Host != i.Host || r.Port != i.Port || r.Database != i.Database || r.Username != i.Username || r.TLSMode != i.TLSMode || r.CAPath != i.CAPath {
		return "", errors.New("Redis credential binding does not match the requested instance")
	}
	return r.Password, nil
}

type localBackend struct{ credentials *credentialStore }

func (b *localBackend) StoreCredential(_ context.Context, i Instance, p string) error {
	return b.credentials.set(i, p)
}
func (b *localBackend) DeleteCredential(_ context.Context, id string) error {
	return b.credentials.delete(id)
}
func (b *localBackend) client(i Instance) (*redis.Client, error) {
	p, e := b.credentials.get(i)
	if e != nil {
		return nil, e
	}
	o := &redis.Options{Addr: fmt.Sprintf("%s:%d", i.Host, i.Port), Username: i.Username, Password: p, DB: i.Database, DialTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, Protocol: 2}
	if i.TLSMode != TLSDisabled {
		c := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: i.Host, InsecureSkipVerify: i.TLSMode == TLSInsecureSkipVerify}
		if i.CAPath != "" {
			body, e := os.ReadFile(i.CAPath)
			if e != nil {
				return nil, e
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(body) {
				return nil, errors.New("Redis CA file contains no certificate")
			}
			c.RootCAs = pool
		}
		o.TLSConfig = c
	}
	return redis.NewClient(o), nil
}
func (b *localBackend) Test(ctx context.Context, i Instance) (ConnectionTest, error) {
	c, e := b.client(i)
	if e != nil {
		return ConnectionTest{}, e
	}
	defer c.Close()
	start := time.Now()
	if e = c.Ping(ctx).Err(); e != nil {
		return ConnectionTest{Error: e.Error()}, e
	}
	r := ConnectionTest{OK: true, TLS: i.TLSMode != TLSDisabled, Latency: time.Since(start)}
	if info, e := c.Info(ctx, "server").Result(); e == nil {
		r.CanInfo = true
		m := parseInfo(info)
		r.Version = m["redis_version"]
		r.Mode = m["redis_mode"]
	}
	if role, e := c.Do(ctx, "ROLE").Result(); e == nil {
		if values, ok := role.([]any); ok && len(values) > 0 {
			r.Role = fmt.Sprint(values[0])
		}
	}
	if _, _, e := c.Scan(ctx, 0, "*", 1).Result(); e == nil {
		r.CanScan = true
	}
	return r, nil
}
func (b *localBackend) Overview(ctx context.Context, i Instance) (Overview, error) {
	c, e := b.client(i)
	if e != nil {
		return Overview{}, e
	}
	defer c.Close()
	raw, e := c.Info(ctx, "server", "clients", "memory", "stats", "persistence", "replication", "keyspace").Result()
	if e != nil {
		return Overview{}, e
	}
	m := parseInfo(raw)
	o := Overview{Version: m["redis_version"], Mode: m["redis_mode"], Role: m["role"], Persistence: persistenceLabel(m)}
	o.Uptime = time.Duration(parseUint(m["uptime_in_seconds"])) * time.Second
	o.ConnectedClients = parseUint(m["connected_clients"])
	o.BlockedClients = parseUint(m["blocked_clients"])
	o.UsedMemory = parseUint(m["used_memory"])
	o.MaxMemory = parseUint(m["maxmemory"])
	o.OperationsPerSecond = parseFloat(m["instantaneous_ops_per_sec"])
	hits, misses := parseFloat(m["keyspace_hits"]), parseFloat(m["keyspace_misses"])
	if hits+misses > 0 {
		o.HitRate = hits / (hits + misses) * 100
	}
	for _, part := range strings.Split(m[fmt.Sprintf("db%d", i.Database)], ",") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) == 2 {
			if kv[0] == "keys" {
				o.KeyCount = parseUint(kv[1])
			}
			if kv[0] == "expires" {
				o.ExpiringKeys = parseUint(kv[1])
			}
		}
	}
	return o, nil
}
func (b *localBackend) Scan(ctx context.Context, i Instance, r ScanRequest) (ScanPage, error) {
	c, e := b.client(i)
	if e != nil {
		return ScanPage{}, e
	}
	defer c.Close()
	keys, cursor, e := c.Scan(ctx, r.Cursor, defaultPattern(r.Pattern), r.Count).Result()
	if e != nil {
		return ScanPage{}, e
	}
	out := ScanPage{Cursor: cursor}
	for _, key := range keys {
		typ, e := c.Type(ctx, key).Result()
		if e != nil {
			continue
		}
		if r.Type != "" && r.Type != "all" && typ != r.Type {
			continue
		}
		ttl, _ := c.PTTL(ctx, key).Result()
		size, _ := c.MemoryUsage(ctx, key).Result()
		out.Keys = append(out.Keys, KeySummary{Name: key, Type: typ, TTL: ttl, Expires: ttl >= 0, SizeBytes: size})
	}
	return out, nil
}
func parseInfo(raw string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if p := strings.IndexByte(line, ':'); p > 0 {
			m[line[:p]] = line[p+1:]
		}
	}
	return m
}
func parseUint(v string) uint64   { n, _ := strconv.ParseUint(v, 10, 64); return n }
func parseFloat(v string) float64 { n, _ := strconv.ParseFloat(v, 64); return n }
func defaultPattern(v string) string {
	if strings.TrimSpace(v) == "" {
		return "*"
	}
	return v
}
func persistenceLabel(m map[string]string) string {
	aof := m["aof_enabled"] == "1"
	rdb := m["rdb_last_bgsave_status"] != ""
	if aof && rdb {
		return "AOF + RDB"
	}
	if aof {
		return "AOF"
	}
	if rdb {
		return "RDB"
	}
	return "disabled"
}

var _ Backend = (*localBackend)(nil)
