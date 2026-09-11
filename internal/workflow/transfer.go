package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// Bundle is portable: custom references are local to this file, never destination IDs.
type Bundle struct {
	Format      string             `json:"format"`
	Version     int                `json:"version"`
	Workflow    Definition         `json:"workflow"`
	CustomNodes []CustomDefinition `json:"customNodes"`
}

func customKey(id string, revision int64) string { return fmt.Sprintf("%s@%d", id, revision) }
func (r Repository) Export(ctx context.Context, d Definition) (Bundle, error) {
	// Copy before stripping instance metadata; exporting must not modify the caller.
	raw, err := json.Marshal(d)
	if err != nil {
		return Bundle{}, err
	}
	var copied Definition
	if err = json.Unmarshal(raw, &copied); err != nil {
		return Bundle{}, err
	}
	d = copied
	b := Bundle{Format: "scriptboard.workflow", Version: 1, Workflow: d, CustomNodes: []CustomDefinition{}}
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return b, err
	}
	defer tx.Rollback()
	seen := map[string]bool{}
	for i, n := range b.Workflow.Graph.Nodes {
		b.Workflow.Graph.Nodes[i].Script = nil
		if n.Kind != "custom" {
			continue
		}
		key := customKey(n.CustomID, n.CustomRevision)
		if seen[key] {
			continue
		}
		seen[key] = true
		var body string
		if err = tx.QueryRowContext(ctx, "SELECT body FROM workflow_versions WHERE kind='custom' AND id=? AND revision=?", n.CustomID, n.CustomRevision).Scan(&body); err != nil {
			return b, fmt.Errorf("%w: custom version %s unavailable", ErrInvalid, key)
		}
		var c CustomDefinition
		if err = json.Unmarshal([]byte(body), &c); err != nil {
			return b, err
		}
		b.CustomNodes = append(b.CustomNodes, c)
	}
	b.Workflow.ID = ""
	b.Workflow.Revision = 0
	return b, nil
}
func (r Repository) Import(ctx context.Context, b Bundle, actor string) (Definition, error) {
	raw, err := json.Marshal(b)
	if err != nil {
		return Definition{}, err
	}
	var copied Bundle
	if err = json.Unmarshal(raw, &copied); err != nil {
		return Definition{}, err
	}
	b = copied
	d := b.Workflow
	invalid := func(message string) (Definition, error) { return d, fmt.Errorf("%w: %s", ErrInvalid, message) }
	if b.Format != "scriptboard.workflow" || b.Version != 1 {
		return invalid("unsupported workflow file format/version")
	}
	if len(d.Graph.Nodes) > 500 || len(b.CustomNodes) > 100 {
		return invalid("file exceeds 500 nodes or 100 custom definitions")
	}
	definitions := map[string]CustomDefinition{}
	for _, c := range b.CustomNodes {
		if c.ID == "" || c.Revision < 1 {
			return invalid("custom id and revision required")
		}
		key := customKey(c.ID, c.Revision)
		if _, ok := definitions[key]; ok {
			return invalid("duplicate custom reference " + key)
		}
		if err := c.Validate(); err != nil {
			return d, err
		}
		definitions[key] = c
	}
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return d, err
	}
	defer tx.Rollback()
	imported := map[string]CustomDefinition{}
	for i, n := range d.Graph.Nodes {
		d.Graph.Nodes[i].Script = nil
		if n.Kind != "custom" {
			continue
		}
		key := customKey(n.CustomID, n.CustomRevision)
		c, ok := imported[key]
		if !ok {
			c, ok = definitions[key]
			if !ok {
				return invalid("missing bundled custom definition " + key)
			}
			c.ID = newID()
			c.Revision = 1
			hash := sha256.Sum256([]byte(c.Code))
			c.Digest = hex.EncodeToString(hash[:])
			if c.Inputs == nil {
				c.Inputs = []Field{}
			}
			if c.Outputs == nil {
				c.Outputs = []Field{}
			}
			if err = saveDocument(ctx, tx, "custom", c.ID, 0, c, actor, true); err != nil {
				return d, err
			}
			imported[key] = c
		}
		d.Graph.Nodes[i].CustomID = c.ID
		d.Graph.Nodes[i].CustomRevision = c.Revision
	}
	d.ID = ""
	d.Revision = 0
	d, err = saveDraft(ctx, tx, d, actor)
	if err != nil {
		return d, err
	}
	if _, err = publish(ctx, tx, d.ID, d.Revision, actor); err != nil {
		return d, err
	}
	return d, tx.Commit()
}
