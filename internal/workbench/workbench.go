// Package workbench stores the versioned inspiration space shared by authenticated users.
package workbench

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"
)

const MaxBytes = 4 << 20

var ErrConflict = errors.New("workbench revision conflict")
var ErrInvalid = errors.New("invalid workspace")
var SchemaStatements = []string{`CREATE TABLE IF NOT EXISTS personal_workbenches (
 user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 revision INTEGER NOT NULL CHECK(revision > 0), content TEXT NOT NULL
)`, `CREATE TABLE IF NOT EXISTS shared_workbench (
 id INTEGER PRIMARY KEY CHECK(id = 1),
 revision INTEGER NOT NULL CHECK(revision > 0), content TEXT NOT NULL, capacity TEXT NOT NULL DEFAULT '{}'
)`}

type Capacity struct {
	Boards int `json:"boards"`
	Items  int `json:"items"`
	Points int `json:"points"`
	Bytes  int `json:"bytes"`
}

func defaultCapacity() Capacity { return Capacity{32, 300, 60000, MaxBytes} }
func ReadCapacity(ctx context.Context, db *sql.DB) (Capacity, error) {
	c := defaultCapacity()
	var raw string
	err := db.QueryRowContext(ctx, "SELECT capacity FROM shared_workbench WHERE id=1").Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	err = json.Unmarshal([]byte(raw), &c)
	return c, err
}

type State struct {
	Capacity *Capacity `json:"capacity,omitempty"`
	Revision int64     `json:"revision"`
	Boards   []Board   `json:"boards"`
}
type Board struct {
	Revision int64  `json:"revision,omitempty"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	Items    []Item `json:"items"`
}
type Task struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
}
type Link struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}
type Point [2]float64
type Pan struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}
type Stroke struct {
	Color    string  `json:"color"`
	Points   []Point `json:"points"`
	Kind     string  `json:"kind,omitempty"`
	Text     string  `json:"text,omitempty"`
	FontSize int     `json:"fontSize,omitempty"`
}
type Item struct {
	Revision  int64    `json:"revision,omitempty"`
	ID        string   `json:"id"`
	Type      string   `json:"type"`
	Title     string   `json:"title"`
	Text      string   `json:"text,omitempty"`
	Tasks     []Task   `json:"tasks,omitempty"`
	Links     []Link   `json:"links,omitempty"`
	Mode      string   `json:"mode,omitempty"`
	Remaining int64    `json:"remaining"`
	Running   bool     `json:"running,omitempty"`
	Started   int64    `json:"started,omitempty"`
	Size      string   `json:"size,omitempty"`
	Strokes   []Stroke `json:"strokes,omitempty"`
	Pan       Pan      `json:"pan"`
}

func Load(ctx context.Context, db *sql.DB) (State, error) {
	s := State{Boards: []Board{}}
	var content string
	err := db.QueryRowContext(ctx, "SELECT revision,content FROM shared_workbench WHERE id=1").Scan(&s.Revision, &content)
	if errors.Is(err, sql.ErrNoRows) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	err = json.Unmarshal([]byte(content), &s.Boards)
	if err != nil {
		return s, err
	}
	capacity, err := ReadCapacity(ctx, db)
	s.Capacity = &capacity
	return s, err
}
func Save(ctx context.Context, db *sql.DB, s State) (int64, error) {
	current, err := Load(ctx, db)
	if err != nil {
		return 0, err
	}
	if current.Revision != s.Revision {
		return 0, ErrConflict
	}
	stamp(current, &s)
	capacity, err := ReadCapacity(ctx, db)
	if err != nil {
		return 0, err
	}
	if err := validate(s, capacity); err != nil {
		return 0, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	data, err := json.Marshal(s.Boards)
	if err != nil {
		return 0, err
	}
	if len(data) > capacity.Bytes {
		return 0, fmt.Errorf("%w: storage capacity exceeded", ErrInvalid)
	}
	// Compare-and-swap keeps a stale browser from overwriting another session's edits.
	var result sql.Result
	if s.Revision == 0 {
		result, err = db.ExecContext(ctx, "INSERT INTO shared_workbench(id,revision,content) VALUES (1,1,?) ON CONFLICT(id) DO NOTHING", string(data))
	} else {
		result, err = db.ExecContext(ctx, "UPDATE shared_workbench SET revision=revision+1,content=? WHERE id=1 AND revision=?", string(data), s.Revision)
	}
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n != 1 {
		return 0, ErrConflict
	}
	return s.Revision + 1, nil
}
func Validate(s State) error { return validate(s, defaultCapacity()) }
func validate(s State, capacity Capacity) error {
	bad := func() error { return fmt.Errorf("invalid workspace content or limit exceeded") }
	if s.Revision < 0 || len(s.Boards) > capacity.Boards {
		return bad()
	}
	seen := map[string]bool{}
	total := 0
	points := 0
	id := func(v string) bool {
		if v == "" || len(v) > 80 || seen[v] {
			return false
		}
		for _, c := range v {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
				return false
			}
		}
		seen[v] = true
		return true
	}
	coord := func(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && math.Abs(v) <= 1000000 }
	for _, b := range s.Boards {
		if !id(b.ID) || strings.TrimSpace(b.Name) == "" || len(b.Name) > 240 || len(b.Items) > 100 {
			return bad()
		}
		total += len(b.Items)
		for _, w := range b.Items {
			if !id(w.ID) || len(w.Title) > 240 || len(w.Text) > 100000 || len(w.Tasks) > 200 || len(w.Links) > 200 {
				return bad()
			}
			switch w.Type {
			case "note", "todo", "links", "timer", "draw":
			default:
				return bad()
			}
			for _, t := range w.Tasks {
				if len(t.Text) > 4000 {
					return bad()
				}
			}
			for _, l := range w.Links {
				u, e := url.Parse(l.URL)
				if e != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || len(l.URL) > 8192 || len(l.Title) > 500 {
					return bad()
				}
			}
			if w.Type == "timer" {
				if w.Mode != "0" && w.Mode != "5" && w.Mode != "25" {
					return bad()
				}
				if w.Remaining < 0 || w.Remaining > 31536000 || w.Started < 0 || w.Started > 32503680000000 || w.Running && w.Started == 0 {
					return bad()
				}
			}
			if w.Type == "draw" {
				switch w.Size {
				case "", "small", "medium", "large", "xlarge":
				default:
					return bad()
				}
			}
			if !coord(w.Pan.X) || !coord(w.Pan.Y) {
				return bad()
			}
			for _, stroke := range w.Strokes {
				if !validDrawingColor(stroke.Color) {
					return bad()
				}
				switch stroke.Kind {
				case "", "erase":
					if stroke.Text != "" || stroke.FontSize != 0 {
						return bad()
					}
				case "text":
					if len(stroke.Points) != 1 || strings.TrimSpace(stroke.Text) == "" || len([]rune(stroke.Text)) > 2000 || stroke.FontSize < 12 || stroke.FontSize > 72 {
						return bad()
					}
				default:
					return bad()
				}
				points += len(stroke.Points)
				for _, p := range stroke.Points {
					if !coord(p[0]) || !coord(p[1]) {
						return bad()
					}
				}
			}
		}
	}
	if total > capacity.Items || points > capacity.Points {
		return bad()
	}
	return nil
}

func validDrawingColor(color string) bool {
	switch color {
	case "#3b5bfd", "#343b49", "#dc2626", "#ea580c", "#ca8a04", "#16a34a", "#0d9488", "#0284c7", "#9333ea", "#db2777":
		return true
	}
	return false
}

// MigrateShared preserves every existing board in one space and leaves the legacy
// rows intact. A revision above all legacy revisions rejects pre-upgrade browsers.
func MigrateShared(tx *sql.Tx) error {
	var exists int
	if err := tx.QueryRow("SELECT count(*) FROM shared_workbench").Scan(&exists); err != nil {
		return err
	}
	if exists != 0 {
		return nil
	}
	rows, err := tx.Query("SELECT revision, content FROM personal_workbenches ORDER BY user_id")
	if err != nil {
		return err
	}
	state := State{Revision: 1, Boards: []Board{}}
	found := false
	for rows.Next() {
		var revision int64
		var content string
		if err = rows.Scan(&revision, &content); err != nil {
			rows.Close()
			return err
		}
		var boards []Board
		if err = json.Unmarshal([]byte(content), &boards); err != nil {
			rows.Close()
			return err
		}
		found = true
		if revision >= state.Revision {
			state.Revision = revision + 1
		}
		state.Boards = append(state.Boards, boards...)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || !found {
		return err
	}
	// Independent personal spaces could use the same IDs; remap collisions only.
	reserved, seen := map[string]bool{}, map[string]bool{}
	for _, b := range state.Boards {
		reserved[b.ID] = true
		for _, item := range b.Items {
			reserved[item.ID] = true
		}
	}
	counter := 0
	unique := func(id string) string {
		if !seen[id] {
			seen[id] = true
			return id
		}
		for {
			counter++
			next := fmt.Sprintf("shared-migrated-%d", counter)
			if !reserved[next] {
				reserved[next] = true
				seen[next] = true
				return next
			}
		}
	}
	for i := range state.Boards {
		b := &state.Boards[i]
		b.ID = unique(b.ID)
		for j := range b.Items {
			b.Items[j].ID = unique(b.Items[j].ID)
		}
	}
	capacity := defaultCapacity()
	capacity.Boards = max(capacity.Boards, len(state.Boards))
	total, points := 0, 0
	for _, b := range state.Boards {
		total += len(b.Items)
		for _, item := range b.Items {
			for _, stroke := range item.Strokes {
				points += len(stroke.Points)
			}
		}
	}
	capacity.Items = max(capacity.Items, total)
	capacity.Points = max(capacity.Points, points)
	if err = validate(state, capacity); err != nil {
		return fmt.Errorf("combined boards exceed shared space limits; original data retained: %w", err)
	}
	data, err := json.Marshal(state.Boards)
	if err != nil {
		return err
	}
	capacity.Bytes = max(capacity.Bytes, len(data)+65536)
	encodedCapacity, _ := json.Marshal(capacity)
	_, err = tx.Exec("INSERT INTO shared_workbench(id,revision,content,capacity) VALUES(1,?,?,?)", state.Revision, string(data), string(encodedCapacity))
	return err
}

// Compare the wire representation so absent and empty optional lists share one version.
func sameItem(a, b Item) bool {
	a.Revision = 0
	b.Revision = 0
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return bytes.Equal(left, right)
}
func stamp(current State, next *State) {
	oldBoards := map[string]Board{}
	for _, b := range current.Boards {
		oldBoards[b.ID] = b
	}
	for i := range next.Boards {
		b := &next.Boards[i]
		old, exists := oldBoards[b.ID]
		b.Revision = old.Revision
		if !exists || old.Name != b.Name {
			b.Revision = current.Revision + 1
		}
		oldItems := map[string]Item{}
		for _, w := range old.Items {
			oldItems[w.ID] = w
		}
		for j := range b.Items {
			w := &b.Items[j]
			old, exists := oldItems[w.ID]
			w.Revision = old.Revision
			if !exists || !sameItem(old, *w) {
				w.Revision = current.Revision + 1
			}
		}
	}
}
