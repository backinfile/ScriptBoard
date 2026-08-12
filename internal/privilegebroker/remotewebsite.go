package privilegebroker

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// RemoteWebsite keeps remote ScriptBoard connection credentials behind the
// Broker and exposes only connection lifecycle and authenticated fetches.
type RemoteWebsite struct {
	client *Client
}

func NewRemoteWebsite(client *Client) *RemoteWebsite { return &RemoteWebsite{client: client} }

func (remote *RemoteWebsite) Store(ctx context.Context, id, endpoint, key string) error {
	_, err := remote.call(ctx, wireRequest{Operation: operationRemoteWebsiteStore, RemoteWebsiteID: id, RemoteWebsiteEndpoint: endpoint, RemoteWebsiteKey: key})
	return err
}

func (remote *RemoteWebsite) Fetch(ctx context.Context, id, locale string) (json.RawMessage, error) {
	response, err := remote.call(ctx, wireRequest{Operation: operationRemoteWebsiteFetch, RemoteWebsiteID: id, RemoteWebsiteLocale: locale})
	if err != nil {
		return nil, err
	}
	if len(response.RemoteWebsitePayload) == 0 || !json.Valid(response.RemoteWebsitePayload) {
		return nil, errors.New("privileged Broker returned an invalid remote website response")
	}
	return append(json.RawMessage(nil), response.RemoteWebsitePayload...), nil
}

func (remote *RemoteWebsite) Delete(ctx context.Context, id string) error {
	_, err := remote.call(ctx, wireRequest{Operation: operationRemoteWebsiteDelete, RemoteWebsiteID: id})
	return err
}

func (remote *RemoteWebsite) call(ctx context.Context, request wireRequest) (wireResponse, error) {
	if remote == nil || remote.client == nil {
		return wireResponse{}, errors.New("privileged Broker remote website service is unavailable")
	}
	authorization, ok := AuthorizationFromContext(ctx)
	if !ok {
		return wireResponse{}, errors.New("privileged Broker remote website authorization is missing")
	}
	request.Version, request.RequestID, request.SessionToken = ProtocolVersion, authorization.RequestID, authorization.SessionToken
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return remote.client.call(ctx, request)
}
