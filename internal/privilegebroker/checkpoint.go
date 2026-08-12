package privilegebroker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"scriptboard/internal/auditlog"
)

// RemoteCheckpoint keeps signing material inside the privileged Broker. The
// local audit store argument is intentionally unused: the Broker reopens and
// verifies the authoritative database before it signs or refreshes anything.
type RemoteCheckpoint struct {
	client  *Client
	mu      sync.Mutex
	eventID int64
}

func NewRemoteCheckpoint(client *Client) *RemoteCheckpoint {
	return &RemoteCheckpoint{client: client}
}

func (checkpoint *RemoteCheckpoint) VerifyOrBootstrap(ctx context.Context, _ *auditlog.Store, _ time.Time) error {
	return checkpoint.call(ctx, operationCheckpointVerify)
}

func (checkpoint *RemoteCheckpoint) Write(ctx context.Context, _ *auditlog.Store, _ time.Time) error {
	return checkpoint.call(ctx, operationCheckpointWrite)
}

func (checkpoint *RemoteCheckpoint) CheckpointEventID() int64 {
	checkpoint.mu.Lock()
	defer checkpoint.mu.Unlock()
	return checkpoint.eventID
}

func (checkpoint *RemoteCheckpoint) call(ctx context.Context, operation string) error {
	if checkpoint == nil || checkpoint.client == nil {
		return errors.New("privileged Broker audit checkpoint is unavailable")
	}
	requestID, err := checkpointRequestID()
	if err != nil {
		return err
	}
	response, err := checkpoint.client.call(ctx, wireRequest{
		Version: ProtocolVersion, Operation: operation, RequestID: requestID,
	})
	if err != nil {
		return err
	}
	if response.EventID < 0 {
		return errors.New("privileged Broker returned an invalid audit checkpoint event ID")
	}
	checkpoint.mu.Lock()
	if response.EventID >= checkpoint.eventID {
		checkpoint.eventID = response.EventID
	}
	checkpoint.mu.Unlock()
	return nil
}

func checkpointRequestID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return "checkpoint-" + hex.EncodeToString(value[:]), nil
}
