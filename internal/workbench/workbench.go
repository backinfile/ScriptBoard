// Package workbench stores a private, versioned workspace for each authenticated user.
package workbench

import (
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
var SchemaStatements = []string{`CREATE TABLE IF NOT EXISTS personal_workbenches (
 user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 revision INTEGER NOT NULL CHECK(revision > 0), content TEXT NOT NULL
)`}

type State struct {
	Revision int64   `json:"revision"`
	Boards   []Board `json:"boards"`
}
type Board struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Items []Item `json:"items"`
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
	Color  string  `json:"color"`
	Points []Point `json:"points"`
}
type Item struct {
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

func Load(ctx context.Context, db *sql.DB, user string) (State, error) {
	s := State{Boards: []Board{}}
	var content string
	err := db.QueryRowContext(ctx, "SELECT revision,content FROM personal_workbenches WHERE user_id=?", user).Scan(&s.Revision, &content)
	if errors.Is(err, sql.ErrNoRows) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	err = json.Unmarshal([]byte(content), &s.Boards)
	return s, err
}
func Save(ctx context.Context, db *sql.DB, user string, s State) (int64, error) {
	if err := Validate(s); err != nil {
		return 0, err
	}
	data, err := json.Marshal(s.Boards)
	if err != nil {
		return 0, err
	}
	if len(data) > MaxBytes {
		return 0, fmt.Errorf("workspace exceeds 4 MiB")
	}
	// Compare-and-swap keeps a stale browser from overwriting another session's edits.
	var result sql.Result
	if s.Revision == 0 {
		result, err = db.ExecContext(ctx, "INSERT INTO personal_workbenches(user_id,revision,content) VALUES (?,1,?) ON CONFLICT(user_id) DO NOTHING", user, string(data))
	} else {
		result, err = db.ExecContext(ctx, "UPDATE personal_workbenches SET revision=revision+1,content=? WHERE user_id=? AND revision=?", string(data), user, s.Revision)
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
func Validate(s State) error {
	bad := func() error { return fmt.Errorf("invalid workspace content or limit exceeded") }
	if s.Revision < 0 || len(s.Boards) > 32 {
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
				if stroke.Color != "#3b5bfd" && stroke.Color != "#343b49" {
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
	if total > 300 || points > 60000 {
		return bad()
	}
	return nil
}
