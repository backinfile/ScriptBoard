package workflow

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrConflict = errors.New("workflow revision conflict")
var ErrLocked = errors.New("entry is locked")
var ErrInvalid = errors.New("invalid workflow document")

var SchemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS workflow_step_runtime (run_id TEXT NOT NULL, node_id TEXT NOT NULL, attempts TEXT NOT NULL DEFAULT '[]', outlets TEXT NOT NULL DEFAULT '[]', PRIMARY KEY(run_id,node_id))`,
	`CREATE TABLE IF NOT EXISTS workflow_documents (
 kind TEXT NOT NULL, id TEXT NOT NULL, revision INTEGER NOT NULL,
 body TEXT NOT NULL, author TEXT NOT NULL, updated_at INTEGER NOT NULL,
 PRIMARY KEY(kind,id))`,
	`CREATE TABLE IF NOT EXISTS workflow_versions (
 kind TEXT NOT NULL, id TEXT NOT NULL, revision INTEGER NOT NULL,
 body TEXT NOT NULL, author TEXT NOT NULL, created_at INTEGER NOT NULL,
 PRIMARY KEY(kind,id,revision))`,
	`CREATE TABLE IF NOT EXISTS workflow_runs (
 id TEXT PRIMARY KEY, entry_id TEXT NOT NULL, status TEXT NOT NULL,
 snapshot TEXT NOT NULL, result TEXT NOT NULL DEFAULT '{}',
 error TEXT NOT NULL DEFAULT '', actor TEXT NOT NULL,
 created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS workflow_run_locks (
 run_id TEXT NOT NULL REFERENCES workflow_runs(id) ON DELETE CASCADE,
 label TEXT NOT NULL, PRIMARY KEY(run_id,label))`,
	`CREATE TABLE IF NOT EXISTS workflow_resource_locks (
 label TEXT PRIMARY KEY, run_id TEXT NOT NULL REFERENCES workflow_runs(id))`,
	`CREATE TABLE IF NOT EXISTS workflow_steps (
 run_id TEXT NOT NULL REFERENCES workflow_runs(id) ON DELETE CASCADE,
 node_id TEXT NOT NULL, status TEXT NOT NULL, process_id TEXT NOT NULL DEFAULT '',
 result TEXT NOT NULL DEFAULT '{}', error TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(run_id,node_id))`,
}

type CustomDefinition struct {
	Digest      string  `json:"digest"`
	ID          string  `json:"id"`
	Revision    int64   `json:"revision"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Language    string  `json:"language"`
	Code        string  `json:"code"`
	Inputs      []Field `json:"inputs"`
	Outputs     []Field `json:"outputs"`
}
type Definition struct {
	ID         string  `json:"id"`
	Revision   int64   `json:"revision"`
	Name       string  `json:"name"`
	Confirm    bool    `json:"confirm"`
	Parameters []Field `json:"parameters"`
	Graph      Graph   `json:"graph"`
}
type Entry struct {
	Confirm          *bool                      `json:"confirm,omitempty"`
	ID               string                     `json:"id"`
	Revision         int64                      `json:"revision"`
	Name             string                     `json:"name"`
	Group            string                     `json:"group"`
	Order            int                        `json:"order"`
	WorkflowID       string                     `json:"workflowId"`
	WorkflowRevision int64                      `json:"workflowRevision"`
	Values           map[string]json.RawMessage `json:"values"`
	Locks            []string                   `json:"locks"`
	Locked           bool                       `json:"locked"`
}
type Snapshot struct {
	Variables map[string]json.RawMessage `json:"variables,omitempty"`
	Entry     Entry                      `json:"entry"`
	Workflow  Definition                 `json:"workflow"`
}
type Repository struct{ DB *sql.DB }

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func validDocument(name string) error {
	if strings.TrimSpace(name) == "" || len(name) > 200 {
		return fmt.Errorf("%w: name required (max 200 bytes)", ErrInvalid)
	}
	return nil
}
func (d CustomDefinition) Validate() error {
	if err := validDocument(d.Name); err != nil {
		return err
	}
	if strings.TrimSpace(d.Code) == "" || len(d.Code) > 1<<20 {
		return fmt.Errorf("%w: script must contain 1..1048576 bytes", ErrInvalid)
	}
	switch d.Language {
	case "python", "javascript", "shell", "powershell":
	default:
		return fmt.Errorf("%w: unsupported script language", ErrInvalid)
	}
	if _, err := fields(d.Inputs, true); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if _, err := fields(d.Outputs, false); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return nil
}
func (r Repository) List(ctx context.Context, kind string) ([]json.RawMessage, error) {
	rows, err := r.DB.QueryContext(ctx, "SELECT body FROM workflow_documents WHERE kind=? ORDER BY updated_at DESC,id", kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []json.RawMessage{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		result = append(result, json.RawMessage(s))
	}
	return result, rows.Err()
}
func (r Repository) Load(ctx context.Context, kind, id string, v any) error {
	var s string
	if err := r.DB.QueryRowContext(ctx, "SELECT body FROM workflow_documents WHERE kind=? AND id=?", kind, id).Scan(&s); err != nil {
		return err
	}
	return json.Unmarshal([]byte(s), v)
}
func (r Repository) Version(ctx context.Context, kind, id string, rev int64, v any) error {
	var s string
	if err := r.DB.QueryRowContext(ctx, "SELECT body FROM workflow_versions WHERE kind=? AND id=? AND revision=?", kind, id, rev).Scan(&s); err != nil {
		return err
	}
	return json.Unmarshal([]byte(s), v)
}
func saveDocument(ctx context.Context, tx *sql.Tx, kind, id string, expected int64, body any, actor string, version bool) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	if len(raw) > 4<<20 {
		return fmt.Errorf("%w: document too large", ErrInvalid)
	}
	now := time.Now().UnixNano()
	if expected == 0 {
		result, err := tx.ExecContext(ctx, "INSERT INTO workflow_documents(kind,id,revision,body,author,updated_at) VALUES(?,?,1,?,?,?) ON CONFLICT(kind,id) DO NOTHING", kind, id, string(raw), actor, now)
		if err != nil {
			return err
		}
		n, _ := result.RowsAffected()
		if n == 0 {
			return ErrConflict
		}
	} else {
		result, err := tx.ExecContext(ctx, "UPDATE workflow_documents SET revision=?,body=?,author=?,updated_at=? WHERE kind=? AND id=? AND revision=?", expected+1, string(raw), actor, now, kind, id, expected)
		if err != nil {
			return err
		}
		n, _ := result.RowsAffected()
		if n == 0 {
			return ErrConflict
		}
	}
	if version {
		_, err = tx.ExecContext(ctx, "INSERT INTO workflow_versions(kind,id,revision,body,author,created_at) VALUES(?,?,?,?,?,?)", kind, id, expected+1, string(raw), actor, now)
	}
	return err
}
func (r Repository) SaveCustom(ctx context.Context, d CustomDefinition, actor string) (CustomDefinition, error) {
	if d.Inputs == nil {
		d.Inputs = []Field{}
	}
	if d.Outputs == nil {
		d.Outputs = []Field{}
	}
	if err := d.Validate(); err != nil {
		return d, err
	}
	if d.ID == "" {
		d.ID = newID()
	}
	hash := sha256.Sum256([]byte(d.Code))
	d.Digest = hex.EncodeToString(hash[:])
	expected := d.Revision
	d.Revision++
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return d, err
	}
	defer tx.Rollback()
	err = saveDocument(ctx, tx, "custom", d.ID, expected, d, actor, true)
	if err != nil {
		return d, err
	}
	return d, tx.Commit()
}
func (r Repository) SaveDraft(ctx context.Context, d Definition, actor string) (Definition, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return d, err
	}
	defer tx.Rollback()
	d, err = saveDraft(ctx, tx, d, actor)
	if err != nil {
		return d, err
	}
	return d, tx.Commit()
}

// Save validates and records the editable document and runnable version atomically.
func (r Repository) Save(ctx context.Context, d Definition, actor string) (Definition, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return d, err
	}
	defer tx.Rollback()
	d, err = saveDraft(ctx, tx, d, actor)
	if err != nil {
		return d, err
	}
	if _, err = publish(ctx, tx, d.ID, d.Revision, actor); err != nil {
		return d, err
	}
	return d, tx.Commit()
}
func saveDraft(ctx context.Context, tx *sql.Tx, d Definition, actor string) (Definition, error) {
	if d.Parameters == nil {
		d.Parameters = []Field{}
	}
	if d.Graph.Nodes == nil {
		d.Graph.Nodes = []Node{}
	}
	if d.Graph.Links == nil {
		d.Graph.Links = []DataLink{}
	}
	if d.Graph.Dependencies == nil {
		d.Graph.Dependencies = []Dependency{}
	}
	if err := validDocument(d.Name); err != nil {
		return d, err
	}
	if _, err := fields(d.Parameters, true); err != nil {
		return d, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if d.ID == "" {
		d.ID = newID()
	}
	expected := d.Revision
	d.Revision++
	if err := saveDocument(ctx, tx, "draft", d.ID, expected, d, actor, false); err != nil {
		return d, err
	}
	return d, nil
}
func (r Repository) Publish(ctx context.Context, id string, revision int64, actor string) (Definition, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return Definition{}, err
	}
	defer tx.Rollback()
	d, err := publish(ctx, tx, id, revision, actor)
	if err != nil {
		return d, err
	}
	return d, tx.Commit()
}
func publish(ctx context.Context, tx *sql.Tx, id string, draftRevision int64, actor string) (Definition, error) {
	var err error
	var raw string
	var d Definition
	if err = tx.QueryRowContext(ctx, "SELECT body FROM workflow_documents WHERE kind='draft' AND id=?", id).Scan(&raw); err != nil {
		return d, err
	}
	if err = json.Unmarshal([]byte(raw), &d); err != nil {
		return d, err
	}
	if d.Revision != draftRevision {
		return d, ErrConflict
	}
	if _, err = Compile(d.Graph); err != nil {
		return d, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err = validateNodeKinds(d); err != nil {
		return d, err
	}
	starts, ends := 0, 0
	for i, n := range d.Graph.Nodes {
		if n.Kind == "start" {
			starts++
		}
		if n.Kind == "end" {
			ends++
		}
		if n.Kind == "custom" {
			if n.CustomID == "" || n.CustomRevision < 1 {
				return d, fmt.Errorf("%w: custom version required", ErrInvalid)
			}
			var body string
			if err = tx.QueryRowContext(ctx, "SELECT body FROM workflow_versions WHERE kind='custom' AND id=? AND revision=?", n.CustomID, n.CustomRevision).Scan(&body); err != nil {
				return d, err
			}
			var custom CustomDefinition
			if err = json.Unmarshal([]byte(body), &custom); err != nil {
				return d, err
			}
			a, _ := json.Marshal(n.Inputs)
			b, _ := json.Marshal(custom.Inputs)
			c, _ := json.Marshal(n.Outputs)
			e, _ := json.Marshal(custom.Outputs)
			if string(a) != string(b) || string(c) != string(e) {
				return d, fmt.Errorf("%w: custom ports differ from version", ErrInvalid)
			}
			d.Graph.Nodes[i].Script = &custom
		}
	}
	if starts != 1 || ends != 1 {
		return d, fmt.Errorf("%w: exactly one start and end required", ErrInvalid)
	}
	var expected int64
	err = tx.QueryRowContext(ctx, "SELECT revision FROM workflow_documents WHERE kind='published' AND id=?", id).Scan(&expected)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return d, err
	}
	d.Revision = expected + 1
	if err = saveDocument(ctx, tx, "published", id, expected, d, actor, true); err != nil {
		return d, err
	}
	return d, nil
}
func validateEntry(e Entry, d Definition) error {
	if err := validDocument(e.Name); err != nil {
		return err
	}
	g := Graph{Nodes: []Node{{ID: "entry", Kind: "entry", Inputs: d.Parameters, Values: e.Values}}}
	if _, err := ResolveInputs(g, "entry", nil); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if len(e.Locks) > 32 {
		return fmt.Errorf("%w: too many lock labels", ErrInvalid)
	}
	seen := map[string]bool{}
	for _, l := range e.Locks {
		if strings.TrimSpace(l) == "" || len(l) > 200 || seen[l] {
			return fmt.Errorf("%w: invalid lock label", ErrInvalid)
		}
		seen[l] = true
	}
	return nil
}
func (r Repository) SaveEntry(ctx context.Context, e Entry, actor string) (Entry, error) {
	if e.Locks == nil {
		e.Locks = []string{}
	}
	if e.Values == nil {
		e.Values = map[string]json.RawMessage{}
	}
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return e, err
	}
	defer tx.Rollback()
	if e.ID == "" {
		e.ID = newID()
	}
	var raw string
	if e.Revision > 0 {
		if err = tx.QueryRowContext(ctx, "SELECT body FROM workflow_documents WHERE kind='entry' AND id=?", e.ID).Scan(&raw); err != nil {
			return e, err
		}
		var old Entry
		if err = json.Unmarshal([]byte(raw), &old); err != nil {
			return e, err
		}
		if old.Locked {
			return e, ErrLocked
		}
	}
	if err = tx.QueryRowContext(ctx, "SELECT body FROM workflow_documents WHERE kind='published' AND id=? AND revision=?", e.WorkflowID, e.WorkflowRevision).Scan(&raw); err != nil {
		return e, err
	}
	var d Definition
	if err = json.Unmarshal([]byte(raw), &d); err != nil {
		return e, err
	}
	// Legacy entries inherit their workflow choice once; subsequent choices belong to the Entry.
	if e.Confirm == nil {
		v := d.Confirm
		e.Confirm = &v
	}
	if err = validateEntry(e, d); err != nil {
		return e, err
	}
	expected := e.Revision
	e.Revision++
	if err = saveDocument(ctx, tx, "entry", e.ID, expected, e, actor, false); err != nil {
		return e, err
	}
	return e, tx.Commit()
}
func (r Repository) SetEntryLock(ctx context.Context, id string, revision int64, locked bool, actor string) (Entry, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return Entry{}, err
	}
	defer tx.Rollback()
	var raw string
	var e Entry
	if err = tx.QueryRowContext(ctx, "SELECT body FROM workflow_documents WHERE kind='entry' AND id=?", id).Scan(&raw); err != nil {
		return e, err
	}
	if err = json.Unmarshal([]byte(raw), &e); err != nil {
		return e, err
	}
	if e.Revision != revision {
		return e, ErrConflict
	}
	e.Locked = locked
	e.Revision++
	if err = saveDocument(ctx, tx, "entry", id, revision, e, actor, false); err != nil {
		return e, err
	}
	return e, tx.Commit()
}

func (r Repository) Delete(ctx context.Context, kind, id string, revision int64) error {
	if kind != "entry" && kind != "custom" && kind != "draft" {
		return ErrInvalid
	}
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw string
	var actual int64
	if err = tx.QueryRowContext(ctx, "SELECT revision,body FROM workflow_documents WHERE kind=? AND id=?", kind, id).Scan(&actual, &raw); err != nil {
		return err
	}
	if actual != revision {
		return ErrConflict
	}
	if kind == "entry" {
		var e Entry
		if err = json.Unmarshal([]byte(raw), &e); err != nil {
			return err
		}
		if e.Locked {
			return ErrLocked
		}
	}
	if kind == "draft" {
		var references int
		if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM workflow_documents WHERE kind='entry' AND json_extract(body,'$.workflowId')=?", id).Scan(&references); err != nil {
			return err
		}
		if references > 0 {
			return fmt.Errorf("%w: entries still reference this workflow", ErrInvalid)
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM workflow_documents WHERE kind='published' AND id=?", id); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM workflow_documents WHERE kind=? AND id=? AND revision=?", kind, id, revision); err != nil {
		return err
	}
	return tx.Commit()
}
