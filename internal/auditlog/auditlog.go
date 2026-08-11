package auditlog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"strconv"
	"sync"
	"time"

	"scriptboard/internal/secretredaction"
)

const (
	chainVersionV1 = "scriptboard-audit-chain-v1"
	chainVersionV2 = "scriptboard-audit-chain-v2"
)

type Event struct {
	OccurredAt, Action, Target, Result, SourceAddress string
	ActorUserID, ActorUsername, ActorRole             string
	RequestID, AuthenticationAssurance                string
}

type Verification struct {
	Count    int
	LastID   int64
	LastHash string
}

type exportMetadata struct {
	Type       string `json:"type"`
	ExportedAt string `json:"exported_at"`
	Events     int    `json:"events"`
	TailSHA256 string `json:"tail_sha256"`
}

type exportEvent struct {
	Type                    string `json:"type"`
	ID                      int64  `json:"id"`
	OccurredAt              int64  `json:"occurred_at"`
	Action                  string `json:"action"`
	Target                  string `json:"target"`
	Result                  string `json:"result"`
	SourceAddress           string `json:"source_address"`
	ActorUserID             string `json:"actor_user_id"`
	ActorUsername           string `json:"actor_username"`
	ActorRole               string `json:"actor_role"`
	RequestID               string `json:"request_id"`
	AuthenticationAssurance string `json:"authentication_assurance"`
	PreviousSHA256          string `json:"previous_sha256"`
	EventSHA256             string `json:"event_sha256"`
}

type Store struct {
	db *sql.DB
	mu sync.Mutex
}

type Transaction struct {
	store    *Store
	tx       *sql.Tx
	finished bool
}

func New(db *sql.DB) *Store { return &Store{db: db} }

func (store *Store) Append(ctx context.Context, event Event) (int64, error) {
	transaction, err := store.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer transaction.Rollback()
	id, err := transaction.Append(ctx, event)
	if err != nil {
		return 0, err
	}
	if err := transaction.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

func (store *Store) Begin(ctx context.Context) (*Transaction, error) {
	store.mu.Lock()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		store.mu.Unlock()
		return nil, err
	}
	return &Transaction{store: store, tx: tx}, nil
}

func (transaction *Transaction) SQL() *sql.Tx { return transaction.tx }

func (transaction *Transaction) Append(ctx context.Context, event Event) (int64, error) {
	if transaction == nil || transaction.finished {
		return 0, errors.New("audit transaction is closed")
	}
	event = redactEvent(event)
	previous, err := currentTail(ctx, transaction.tx)
	if err != nil {
		return 0, err
	}
	digest := eventDigest(previous, event)
	result, err := transaction.tx.ExecContext(ctx, `INSERT INTO audit_events
		(occurred_at, action, target, result, source_address, actor_user_id, actor_username, actor_role,
		 request_id, authentication_assurance, previous_hash, event_hash)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.OccurredAt, event.Action, event.Target, event.Result, event.SourceAddress,
		event.ActorUserID, event.ActorUsername, event.ActorRole, event.RequestID, event.AuthenticationAssurance, previous, digest)
	if err != nil {
		return 0, err
	}
	if _, err := transaction.tx.ExecContext(ctx, "UPDATE audit_chain_state SET tail_hash = ? WHERE id = 1", digest); err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func redactEvent(event Event) Event {
	event.Action = secretredaction.String(event.Action)
	event.Target = secretredaction.String(event.Target)
	event.Result = secretredaction.String(event.Result)
	event.SourceAddress = secretredaction.String(event.SourceAddress)
	event.ActorUserID = secretredaction.String(event.ActorUserID)
	event.ActorUsername = secretredaction.String(event.ActorUsername)
	event.ActorRole = secretredaction.String(event.ActorRole)
	event.RequestID = secretredaction.String(event.RequestID)
	event.AuthenticationAssurance = secretredaction.String(event.AuthenticationAssurance)
	return event
}

func (transaction *Transaction) Commit() error {
	if transaction == nil || transaction.finished {
		return errors.New("audit transaction is closed")
	}
	transaction.finished = true
	err := transaction.tx.Commit()
	transaction.store.mu.Unlock()
	return err
}

func (transaction *Transaction) Rollback() error {
	if transaction == nil || transaction.finished {
		return nil
	}
	transaction.finished = true
	err := transaction.tx.Rollback()
	transaction.store.mu.Unlock()
	return err
}

func (store *Store) Verify(ctx context.Context) (Verification, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Verification{}, err
	}
	defer tx.Rollback()
	return verifyTransaction(ctx, tx)
}

// VerifyWithCheckpoint validates the complete chain and a signed checkpoint
// against one read transaction so a concurrent writer cannot change the
// database between the two checks.
func (store *Store) VerifyWithCheckpoint(ctx context.Context, eventID int64, eventSHA256 string) (Verification, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Verification{}, err
	}
	defer tx.Rollback()
	verification, err := verifyTransaction(ctx, tx)
	if err != nil {
		return Verification{}, err
	}
	if err := verifyCheckpointTransaction(ctx, tx, eventID, eventSHA256); err != nil {
		return Verification{}, err
	}
	return verification, nil
}

// VerifyCheckpoint confirms that a previously signed event identity is still
// present in the local chain. Callers verify the full chain separately before
// trusting this membership check.
func (store *Store) VerifyCheckpoint(ctx context.Context, eventID int64, eventSHA256 string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return verifyCheckpointTransaction(ctx, tx, eventID, eventSHA256)
}

func verifyCheckpointTransaction(ctx context.Context, tx *sql.Tx, eventID int64, eventSHA256 string) error {
	if eventID < 0 || eventSHA256 == "" && eventID != 0 {
		return errors.New("audit checkpoint identity is invalid")
	}
	if eventID == 0 {
		var anchor string
		if err := tx.QueryRowContext(ctx, `SELECT anchor_hash FROM audit_chain_state WHERE id = 1`).Scan(&anchor); err != nil {
			return err
		}
		if anchor != eventSHA256 {
			return errors.New("audit checkpoint no longer matches the retained chain anchor")
		}
		return nil
	}
	var recorded string
	if err := tx.QueryRowContext(ctx, `SELECT event_hash FROM audit_events WHERE id = ?`, eventID).Scan(&recorded); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("audit checkpoint event %d is missing", eventID)
		}
		return err
	}
	if recorded != eventSHA256 {
		return fmt.Errorf("audit checkpoint event %d digest mismatch", eventID)
	}
	return nil
}

// Export writes a verified, snapshot-consistent JSON Lines evidence artifact.
// The first line describes the verified chain and every following line is an
// audit event including its recorded chain links. No output is written when
// verification fails.
func (store *Store) Export(ctx context.Context, destination io.Writer, exportedAt time.Time) (Verification, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Verification{}, err
	}
	defer tx.Rollback()
	verification, err := verifyTransaction(ctx, tx)
	if err != nil {
		return Verification{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, occurred_at, action, target, result, source_address,
		actor_user_id, actor_username, actor_role, request_id, authentication_assurance,
		previous_hash, event_hash FROM audit_events ORDER BY id`)
	if err != nil {
		return Verification{}, err
	}
	defer rows.Close()
	encoder := json.NewEncoder(destination)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(exportMetadata{
		Type: "scriptboard.audit-evidence.v1", ExportedAt: exportedAt.UTC().Format(time.RFC3339Nano),
		Events: verification.Count, TailSHA256: verification.LastHash,
	}); err != nil {
		return Verification{}, err
	}
	for rows.Next() {
		var event exportEvent
		event.Type = "scriptboard.audit-event.v1"
		if err := rows.Scan(&event.ID, &event.OccurredAt, &event.Action, &event.Target, &event.Result,
			&event.SourceAddress, &event.ActorUserID, &event.ActorUsername, &event.ActorRole,
			&event.RequestID, &event.AuthenticationAssurance, &event.PreviousSHA256, &event.EventSHA256); err != nil {
			return Verification{}, err
		}
		if err := encoder.Encode(event); err != nil {
			return Verification{}, err
		}
	}
	if err := rows.Err(); err != nil {
		return Verification{}, err
	}
	return verification, nil
}

func verifyTransaction(ctx context.Context, tx *sql.Tx) (Verification, error) {
	var anchor, tail string
	if err := tx.QueryRowContext(ctx, "SELECT anchor_hash, tail_hash FROM audit_chain_state WHERE id = 1").Scan(&anchor, &tail); err != nil {
		return Verification{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, occurred_at, action, target, result, source_address,
		actor_user_id, actor_username, actor_role, request_id, authentication_assurance,
		previous_hash, event_hash FROM audit_events ORDER BY id`)
	if err != nil {
		return Verification{}, err
	}
	defer rows.Close()
	previous := anchor
	verification := Verification{}
	for rows.Next() {
		var id int64
		var occurredAt int64
		var event Event
		var recordedPrevious, recordedHash string
		if err := rows.Scan(&id, &occurredAt, &event.Action, &event.Target, &event.Result, &event.SourceAddress,
			&event.ActorUserID, &event.ActorUsername, &event.ActorRole, &event.RequestID, &event.AuthenticationAssurance,
			&recordedPrevious, &recordedHash); err != nil {
			return Verification{}, err
		}
		event.OccurredAt = strconv.FormatInt(occurredAt, 10)
		if recordedPrevious != previous {
			return Verification{}, fmt.Errorf("audit chain link mismatch at event %d", id)
		}
		expected := eventDigest(recordedPrevious, event)
		if recordedHash == "" || recordedHash != expected {
			return Verification{}, fmt.Errorf("audit event %d digest mismatch", id)
		}
		previous = recordedHash
		verification.Count++
		verification.LastID = id
	}
	if err := rows.Err(); err != nil {
		return Verification{}, err
	}
	if previous != tail {
		return Verification{}, errors.New("audit chain tail mismatch")
	}
	verification.LastHash = tail
	return verification, nil
}

func BackfillTransaction(ctx context.Context, tx *sql.Tx) error {
	type row struct {
		id    int64
		event Event
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, occurred_at, action, target, result, source_address,
		actor_user_id, actor_username, actor_role FROM audit_events ORDER BY id`)
	if err != nil {
		return err
	}
	var events []row
	for rows.Next() {
		var item row
		var occurredAt int64
		if err := rows.Scan(&item.id, &occurredAt, &item.event.Action, &item.event.Target, &item.event.Result,
			&item.event.SourceAddress, &item.event.ActorUserID, &item.event.ActorUsername, &item.event.ActorRole); err != nil {
			_ = rows.Close()
			return err
		}
		item.event.OccurredAt = strconv.FormatInt(occurredAt, 10)
		events = append(events, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	previous := ""
	for _, item := range events {
		digest := eventDigest(previous, item.event)
		if _, err := tx.ExecContext(ctx, "UPDATE audit_events SET previous_hash = ?, event_hash = ? WHERE id = ?", previous, digest, item.id); err != nil {
			return err
		}
		previous = digest
	}
	_, err = tx.ExecContext(ctx, "UPDATE audit_chain_state SET anchor_hash = '', tail_hash = ? WHERE id = 1", previous)
	return err
}

func currentTail(ctx context.Context, tx *sql.Tx) (string, error) {
	var tail string
	if err := tx.QueryRowContext(ctx, "SELECT tail_hash FROM audit_chain_state WHERE id = 1").Scan(&tail); err != nil {
		return "", fmt.Errorf("read audit chain tail: %w", err)
	}
	return tail, nil
}

func eventDigest(previous string, event Event) string {
	digest := sha256.New()
	version := chainVersionV1
	values := []string{previous, event.OccurredAt, event.Action, event.Target, event.Result,
		event.SourceAddress, event.ActorUserID, event.ActorUsername, event.ActorRole}
	if event.RequestID != "" || event.AuthenticationAssurance != "" {
		version = chainVersionV2
		values = append(values, event.RequestID, event.AuthenticationAssurance)
	}
	for _, value := range append([]string{version}, values...) {
		writeField(digest, value)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func writeField(destination hash.Hash, value string) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = destination.Write(size[:])
	_, _ = destination.Write([]byte(value))
}
