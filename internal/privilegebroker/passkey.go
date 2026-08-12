package privilegebroker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"scriptboard/internal/passkey"
)

// RemotePasskey moves encrypted WebAuthn public-credential state behind the
// privileged Broker without exposing a generic ciphertext or unseal API.
// Authenticator private keys never enter ScriptBoard.
type RemotePasskey struct {
	client *Client
}

func NewRemotePasskey(client *Client) *RemotePasskey { return &RemotePasskey{client: client} }

func (remote *RemotePasskey) User(userID, username string) (passkey.User, error) {
	response, err := remote.call(context.Background(), wireRequest{Operation: operationPasskeyUser, PasskeyUserID: userID, PasskeyUsername: username}, false)
	if err != nil {
		return passkey.User{}, err
	}
	if response.PasskeyUser == nil || response.PasskeyUser.ID != userID || response.PasskeyUser.Name != username {
		return passkey.User{}, errors.New("privileged Broker returned an invalid passkey user")
	}
	return *response.PasskeyUser, nil
}

func (remote *RemotePasskey) List(userID string) ([]passkey.CredentialView, error) {
	response, err := remote.call(context.Background(), wireRequest{Operation: operationPasskeyList, PasskeyUserID: userID}, false)
	return response.PasskeyCredentials, err
}

func (remote *RemotePasskey) Add(userID, name string, credential webauthn.Credential) error {
	return remote.AddContext(context.Background(), userID, name, credential)
}

func (remote *RemotePasskey) AddContext(ctx context.Context, userID, name string, credential webauthn.Credential) error {
	_, err := remote.call(ctx, wireRequest{Operation: operationPasskeyAdd, PasskeyUserID: userID, PasskeyName: name, PasskeyCredential: &credential}, true)
	return err
}

func (remote *RemotePasskey) Update(userID string, credential webauthn.Credential) error {
	_, err := remote.call(context.Background(), wireRequest{Operation: operationPasskeyUpdate, PasskeyUserID: userID, PasskeyCredential: &credential}, false)
	return err
}

func (remote *RemotePasskey) Delete(userID, credentialID string) error {
	return remote.DeleteContext(context.Background(), userID, credentialID)
}

func (remote *RemotePasskey) DeleteContext(ctx context.Context, userID, credentialID string) error {
	_, err := remote.call(ctx, wireRequest{Operation: operationPasskeyDelete, PasskeyUserID: userID, PasskeyCredentialID: credentialID}, true)
	return err
}

func (remote *RemotePasskey) Reset(userID string) error {
	return remote.ResetContext(context.Background(), userID)
}

func (remote *RemotePasskey) ResetContext(ctx context.Context, userID string) error {
	_, err := remote.call(ctx, wireRequest{Operation: operationPasskeyReset, PasskeyUserID: userID}, true)
	return err
}

func (remote *RemotePasskey) call(ctx context.Context, request wireRequest, authorized bool) (wireResponse, error) {
	if remote == nil || remote.client == nil {
		return wireResponse{}, errors.New("privileged Broker passkey service is unavailable")
	}
	requestID, sessionToken := "", ""
	if authorized {
		authorization, ok := AuthorizationFromContext(ctx)
		if !ok {
			return wireResponse{}, errors.New("privileged Broker passkey authorization is missing")
		}
		requestID, sessionToken = authorization.RequestID, authorization.SessionToken
	} else {
		var err error
		requestID, err = passkeyRequestID()
		if err != nil {
			return wireResponse{}, err
		}
	}
	request.Version, request.RequestID, request.SessionToken = ProtocolVersion, requestID, sessionToken
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	response, err := remote.client.call(ctx, request)
	if err == nil {
		return response, nil
	}
	switch response.ErrorCode {
	case "passkey_duplicate":
		return response, passkey.ErrDuplicateCredential
	case "passkey_limit":
		return response, passkey.ErrCredentialLimit
	case "passkey_not_found":
		return response, passkey.ErrCredentialNotFound
	case "passkey_identity_mismatch":
		return response, passkey.ErrCredentialIdentityMismatch
	case "passkey_too_large":
		return response, passkey.ErrCredentialTooLarge
	default:
		return response, err
	}
}

func passkeyRequestID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return "passkey-" + hex.EncodeToString(value[:]), nil
}
