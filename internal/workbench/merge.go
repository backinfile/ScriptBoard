package workbench

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
)

type Patch struct {
	Base State `json:"base"`
	Next State `json:"next"`
}
type PatchResult struct {
	State     State    `json:"state"`
	Conflicts []string `json:"conflicts"`
}

// ApplyPatch compares only changed modules and structural operations. Unrelated
// writes are retained; conflicting operations are returned without discarding edits.
func ApplyPatch(ctx context.Context, db *sql.DB, p Patch) (PatchResult, error) {
	capacity, err := ReadCapacity(ctx, db)
	if err != nil {
		return PatchResult{}, err
	}
	if err = validate(p.Base, capacity); err != nil {
		return PatchResult{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err = validate(p.Next, capacity); err != nil {
		return PatchResult{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	for attempt := 0; attempt < 8; attempt++ {
		current, err := Load(ctx, db)
		if err != nil {
			return PatchResult{}, err
		}
		merged, conflicts := merge(current, p.Base, p.Next)
		if reflect.DeepEqual(current.Boards, merged.Boards) {
			return PatchResult{current, conflicts}, nil
		}
		revision, err := Save(ctx, db, merged)
		if errors.Is(err, ErrConflict) {
			continue
		}
		if err != nil {
			return PatchResult{}, err
		}
		merged.Revision = revision
		// Save stamps module revisions in the merged slice before the atomic update.
		return PatchResult{merged, conflicts}, nil
	}
	return PatchResult{}, ErrConflict
}
func cloneState(s State) State {
	out := s
	out.Boards = append([]Board{}, s.Boards...)
	for i := range out.Boards {
		out.Boards[i].Items = append([]Item{}, s.Boards[i].Items...)
	}
	return out
}
func boardMap(bs []Board) map[string]Board {
	m := map[string]Board{}
	for _, b := range bs {
		m[b.ID] = b
	}
	return m
}
func itemMap(ws []Item) map[string]Item {
	m := map[string]Item{}
	for _, w := range ws {
		m[w.ID] = w
	}
	return m
}
func boardIDs(bs []Board) []string {
	ids := []string{}
	for _, b := range bs {
		ids = append(ids, b.ID)
	}
	return ids
}
func itemIDs(ws []Item) []string {
	ids := []string{}
	for _, w := range ws {
		ids = append(ids, w.ID)
	}
	return ids
}

// Reordering existing entries conflicts only with another reorder. Concurrent
// additions survive and concurrent deletions are never resurrected by ordering.
func mergeOrder(base, next, current []string) ([]string, bool) {
	common := map[string]bool{}
	for _, id := range base {
		if slices.Contains(next, id) && slices.Contains(current, id) {
			common[id] = true
		}
	}
	filter := func(ids []string) []string {
		r := []string{}
		for _, id := range ids {
			if common[id] {
				r = append(r, id)
			}
		}
		return r
	}
	before, desired, remote := filter(base), filter(next), filter(current)
	if slices.Equal(before, desired) {
		return current, false
	}
	if !slices.Equal(before, remote) && !slices.Equal(desired, remote) {
		return current, true
	}
	result := append([]string{}, current...)
	i := 0
	for j, id := range result {
		if common[id] {
			result[j] = desired[i]
			i++
		}
	}
	return result, false
}
func merge(current, base, next State) (State, []string) {
	result := cloneState(current)
	conflicts := []string{}
	before, desired := boardMap(base.Boards), boardMap(next.Boards)
	for _, b := range base.Boards {
		if _, keep := desired[b.ID]; !keep {
			index := slices.IndexFunc(result.Boards, func(x Board) bool { return x.ID == b.ID })
			if index < 0 {
				continue
			}
			if !reflect.DeepEqual(result.Boards[index], b) {
				conflicts = append(conflicts, "board:"+b.ID)
				continue
			}
			result.Boards = slices.Delete(result.Boards, index, index+1)
		}
	}
	for _, b := range next.Boards {
		old, existed := before[b.ID]
		index := slices.IndexFunc(result.Boards, func(x Board) bool { return x.ID == b.ID })
		// Lost creation responses can be retried after Save assigns server revisions.
		if !existed {
			if index < 0 {
				result.Boards = append(result.Boards, b)
			} else if !sameBoardContent(result.Boards[index], b) {
				conflicts = append(conflicts, "board:"+b.ID)
			}
			continue
		}
		if reflect.DeepEqual(old, b) {
			continue
		}
		if index < 0 {
			conflicts = append(conflicts, "board:"+b.ID)
			continue
		}
		remote := &result.Boards[index]
		if old.Name != b.Name {
			if remote.Revision != old.Revision && remote.Name != b.Name {
				conflicts = append(conflicts, "name:"+b.ID)
			} else {
				remote.Name = b.Name
			}
		}
		oldItems, wanted := itemMap(old.Items), itemMap(b.Items)
		for _, w := range old.Items {
			if _, keep := wanted[w.ID]; !keep {
				j := slices.IndexFunc(remote.Items, func(x Item) bool { return x.ID == w.ID })
				if j < 0 {
					continue
				}
				if remote.Items[j].Revision != w.Revision {
					conflicts = append(conflicts, "item:"+w.ID)
					continue
				}
				remote.Items = slices.Delete(remote.Items, j, j+1)
			}
		}
		for _, w := range b.Items {
			original, existed := oldItems[w.ID]
			j := slices.IndexFunc(remote.Items, func(x Item) bool { return x.ID == w.ID })
			if !existed {
				if j < 0 {
					remote.Items = append(remote.Items, w)
				} else if !sameItem(remote.Items[j], w) {
					conflicts = append(conflicts, "item:"+w.ID)
				}
				continue
			}
			if sameItem(original, w) {
				continue
			}
			if j < 0 {
				conflicts = append(conflicts, "item:"+w.ID)
				continue
			}
			if remote.Items[j].Revision != original.Revision && !sameItem(remote.Items[j], w) {
				conflicts = append(conflicts, "item:"+w.ID)
				continue
			}
			remote.Items[j] = w
		}
		order, conflict := mergeOrder(itemIDs(old.Items), itemIDs(b.Items), itemIDs(remote.Items))
		if conflict {
			conflicts = append(conflicts, "order:"+b.ID)
		} else {
			byID := itemMap(remote.Items)
			for j, id := range order {
				remote.Items[j] = byID[id]
			}
		}
	}
	order, conflict := mergeOrder(boardIDs(base.Boards), boardIDs(next.Boards), boardIDs(result.Boards))
	if conflict {
		conflicts = append(conflicts, "boards")
	} else {
		byID := boardMap(result.Boards)
		for i, id := range order {
			result.Boards[i] = byID[id]
		}
	}
	return result, conflicts
}

// Notifications contain no document data. Slow clients coalesce notifications
// and retrieve the newest state; subscriptions are removed on disconnect.
type Notifier struct {
	mu      sync.Mutex
	clients map[chan struct{}]bool
}

func (n *Notifier) Subscribe() (chan struct{}, func()) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.clients == nil {
		n.clients = map[chan struct{}]bool{}
	}
	c := make(chan struct{}, 1)
	n.clients[c] = true
	return c, func() { n.mu.Lock(); defer n.mu.Unlock(); delete(n.clients, c) }
}
func (n *Notifier) Publish() {
	n.mu.Lock()
	defer n.mu.Unlock()
	for c := range n.clients {
		select {
		case c <- struct{}{}:
		default:
		}
	}
}
