package privilegebroker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"scriptboard/internal/mfa"
)

// RemoteMFA exposes only MFA domain operations. It never accepts plaintext to
// seal or ciphertext to unseal, so a Web compromise cannot turn this boundary
// into a generic oracle for other credential domains.
type RemoteMFA struct {
	client *Client
}

func NewRemoteMFA(client *Client) *RemoteMFA { return &RemoteMFA{client: client} }

func (remote *RemoteMFA) Status(userID string) (mfa.Status, error) {
	response, err := remote.call(operationMFAStatus, userID, "", "")
	return mfa.Status{Enabled: response.MFAEnabled, RecoveryCodes: response.MFARecoveryCodes}, err
}

func (remote *RemoteMFA) Begin(userID, account string) (mfa.Enrollment, error) {
	response, err := remote.call(operationMFABegin, userID, account, "")
	if err != nil {
		return mfa.Enrollment{}, err
	}
	if response.MFAEnrollment == nil || response.MFAEnrollment.Secret == "" || response.MFAEnrollment.URI == "" {
		return mfa.Enrollment{}, errors.New("privileged Broker returned an invalid MFA enrollment")
	}
	return *response.MFAEnrollment, nil
}

func (remote *RemoteMFA) Confirm(userID, code string) ([]string, error) {
	response, err := remote.call(operationMFAConfirm, userID, "", code)
	if err != nil {
		return nil, err
	}
	if len(response.MFARecoveryValues) == 0 {
		return nil, errors.New("privileged Broker returned no MFA recovery codes")
	}
	return response.MFARecoveryValues, nil
}

func (remote *RemoteMFA) Verify(userID, code string) (bool, error) {
	response, err := remote.call(operationMFAVerify, userID, "", code)
	return response.MFAVerified, err
}

func (remote *RemoteMFA) Reset(userID string) error {
	_, err := remote.call(operationMFAReset, userID, "", "")
	return err
}

func (remote *RemoteMFA) call(operation, userID, account, code string) (wireResponse, error) {
	if remote == nil || remote.client == nil {
		return wireResponse{}, errors.New("privileged Broker MFA service is unavailable")
	}
	requestID, err := mfaRequestID()
	if err != nil {
		return wireResponse{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	response, err := remote.client.call(ctx, wireRequest{
		Version: ProtocolVersion, Operation: operation, RequestID: requestID,
		MFAUserID: userID, MFAAccount: account, MFACode: code,
	})
	if err == nil {
		return response, nil
	}
	switch response.ErrorCode {
	case "mfa_already_enabled":
		return response, mfa.ErrAlreadyEnabled
	case "mfa_enrollment_absent":
		return response, mfa.ErrEnrollmentAbsent
	case "mfa_invalid_code":
		return response, mfa.ErrInvalidCode
	default:
		return response, err
	}
}

func mfaRequestID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return "mfa-" + hex.EncodeToString(value[:]), nil
}
