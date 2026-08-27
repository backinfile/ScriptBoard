package mcpaccess

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"scriptboard/internal/outboundpolicy"
)

type cimdDocument struct {
	ClientID                string   `json:"client_id"`
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method,omitempty"`
}

func (store *Store) resolveCIMD(ctx context.Context, rawURL string) (Client, error) {
	target, err := url.Parse(rawURL)
	if err != nil || target.Scheme != "https" || target.Host == "" || target.User != nil || target.Fragment != "" {
		return Client{}, ErrInvalidClient
	}
	if port := target.Port(); port != "" && port != "443" {
		return Client{}, ErrInvalidClient
	}
	host := target.Hostname()
	if strings.EqualFold(host, "localhost") {
		return Client{}, ErrInvalidClient
	}
	// 修复 CIMD 独立地址分类遗漏保留网段：复用全产品唯一的出站解析与固定拨号策略。
	transport := outboundpolicy.Policy{Resolver: store.cimdResolver}.Transport()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("CIMD redirects are disabled") }}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return Client{}, ErrInvalidClient
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return Client{}, ErrInvalidClient
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "application/json") {
		return Client{}, ErrInvalidClient
	}
	limited := io.LimitReader(response.Body, (64<<10)+1)
	body, err := io.ReadAll(limited)
	if err != nil || len(body) > 64<<10 {
		return Client{}, ErrInvalidClient
	}
	var document cimdDocument
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	if decoder.Decode(&document) != nil || (document.ClientID != "" && document.ClientID != rawURL) || (document.TokenEndpointAuthMethod != "" && document.TokenEndpointAuthMethod != "none") {
		return Client{}, ErrInvalidClient
	}
	if strings.TrimSpace(document.ClientName) == "" {
		document.ClientName = host
	}
	clientRecord, err := store.RegisterClient(ctx, document.ClientName, document.RedirectURIs, "cimd", rawURL)
	if err != nil {
		if existing, loadErr := store.clientFromDB(ctx, rawURL); loadErr == nil {
			return existing, nil
		}
		return Client{}, fmt.Errorf("register CIMD: %w", err)
	}
	return clientRecord, nil
}
