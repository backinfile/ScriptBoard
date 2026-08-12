package privilegebroker

import (
	"context"
	"errors"
	"sync"
	"time"

	"scriptboard/internal/providercredential"
)

// ProviderCredentials keeps Assistant provider credentials and their outbound
// proxy sessions behind the privileged Broker boundary.
type ProviderCredentials struct {
	client *Client
}

func NewProviderCredentials(client *Client) *ProviderCredentials {
	return &ProviderCredentials{client: client}
}

func (providers *ProviderCredentials) Store(ctx context.Context, record providercredential.Record, credential string) error {
	_, err := providers.callAuthorized(ctx, wireRequest{
		Operation: operationProviderStore, ProviderID: record.ID, ProviderName: record.Provider,
		ProviderModel: record.Model, ProviderEndpoint: record.Endpoint,
		ProviderCredential: credential, ProviderShared: record.Shared,
	})
	return err
}

func (providers *ProviderCredentials) Delete(ctx context.Context, id string) error {
	_, err := providers.callAuthorized(ctx, wireRequest{Operation: operationProviderDelete, ProviderID: id})
	return err
}

func (providers *ProviderCredentials) Start(ctx context.Context, id string) (*ProviderSession, error) {
	response, err := providers.callAuthorized(ctx, wireRequest{Operation: operationProviderStart, ProviderID: id})
	if err != nil {
		return nil, err
	}
	if response.ProviderProxyEndpoint == "" || response.ProviderCapability == "" || !validProviderSessionHandle(response.ProviderSessionHandle) {
		return nil, errors.New("privileged Broker returned an invalid provider proxy session")
	}
	return &ProviderSession{
		client: providers.client, endpoint: response.ProviderProxyEndpoint,
		capability: response.ProviderCapability, handle: response.ProviderSessionHandle,
	}, nil
}

func (providers *ProviderCredentials) callAuthorized(ctx context.Context, request wireRequest) (wireResponse, error) {
	if providers == nil || providers.client == nil {
		return wireResponse{}, errors.New("privileged Broker provider service is unavailable")
	}
	authorization, ok := AuthorizationFromContext(ctx)
	if !ok {
		return wireResponse{}, errors.New("privileged Broker provider authorization is missing")
	}
	request.Version, request.RequestID, request.SessionToken = ProtocolVersion, authorization.RequestID, authorization.SessionToken
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	return providers.client.call(ctx, request)
}

// ProviderSession is a revocable handle to a Broker-owned loopback proxy. It
// intentionally exposes only the capability needed by the isolated Pi Runtime.
type ProviderSession struct {
	client               *Client
	endpoint, capability string
	handle               string
	closeOnce            sync.Once
	closeErr             error
}

func (session *ProviderSession) Endpoint() string   { return session.endpoint }
func (session *ProviderSession) Capability() string { return session.capability }

func (session *ProviderSession) Close(ctx context.Context) error {
	if session == nil || session.client == nil {
		return nil
	}
	session.closeOnce.Do(func() {
		requestID := "provider-stop:" + session.handle[:20]
		_, session.closeErr = session.client.call(ctx, wireRequest{
			Version: ProtocolVersion, Operation: operationProviderStop, RequestID: requestID,
			ProviderSessionHandle: session.handle,
		})
	})
	return session.closeErr
}
