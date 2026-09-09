package registryconnection

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path"
	"sort"
	"strings"
	"time"

	"scriptboard/internal/registrymonitor"
)

func managementID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func (service *Service) writeManagement(state persistedState) error {
	// Budget the serialized state, retaining current operations before old history.
	for {
		body, err := json.Marshal(state)
		if err != nil {
			return err
		}
		if len(body) <= 512<<10 {
			return service.write(state)
		}
		if len(state.Events) > 1 {
			state.Events = state.Events[:len(state.Events)-1]
			continue
		}
		if len(state.Plans) > 1 {
			oldest := ""
			for id, p := range state.Plans {
				if oldest == "" || p.Created.Before(state.Plans[oldest].Created) {
					oldest = id
				}
			}
			delete(state.Plans, oldest)
			continue
		}
		return errors.New("cleanup records exceed storage budget; reduce the selection")
	}
}

// Manage keeps connection secrets, immutable deletion plans, and results in the
// Broker-owned sealed store. Plans expire and are consumed before remote writes.
func (service *Service) Manage(ctx context.Context, req registrymonitor.ManagementRequest) (registrymonitor.ManagementResponse, error) {
	service.managementMu.Lock()
	defer service.managementMu.Unlock()
	// Management history has its own store and lock so cleanup cannot block or
	// overwrite dashboard connection transactions.
	store := &Service{path: service.path + ".management", vault: service.vault, inspector: service.inspector}
	var out registrymonitor.ManagementResponse
	state, err := store.load()
	if err != nil {
		return out, err
	}
	if state.Plans == nil {
		state.Plans = map[string]registrymonitor.DeletePlan{}
	}
	for id, p := range state.Plans {
		if time.Since(p.Created) > 10*time.Minute {
			delete(state.Plans, id)
		}
	}
	record, exists := state.Active[req.ID]
	if req.Command == "list" {
		for id, r := range state.Active {
			if r.Managed {
				out.Connections = append(out.Connections, registrymonitor.ManagedConnection{ID: id, Name: r.Name, Config: r.Config})
			}
		}
		sort.Slice(out.Connections, func(i, j int) bool { return out.Connections[i].Name < out.Connections[j].Name })
		return out, nil
	}
	if req.Command == "save" {
		req.Name = strings.TrimSpace(req.Name)
		if req.Name == "" || len(req.Name) > 100 {
			return out, ErrInvalidConnection
		}
		if req.ID != "" && (!exists || !record.Managed) {
			return out, ErrNotFound
		}
		config := registrymonitor.NormalizeConfig(req.Config)
		config.Images = []string{"*"}
		if registrymonitor.ValidateConfig(config) != nil || !validCredential(req.Password, true) {
			return out, ErrInvalidConnection
		}
		password := req.Password
		if req.Preserve && password == "" {
			password = record.Password
		}
		if config.AuthMode == "anonymous" {
			password = ""
			config.Username = ""
		}
		if config.AuthMode == "basic" && password == "" {
			return out, ErrInvalidConnection
		}
		if req.ID == "" {
			if len(state.Active) >= maxConnections {
				return out, errors.New("connection limit reached")
			}
			req.ID = "managed-" + managementID()
		}
		state.Active[req.ID] = storedRecord{Name: req.Name, Managed: true, Revision: managementID(), Config: config, Password: password}
		for id, p := range state.Plans {
			if p.ConnectionID == req.ID {
				delete(state.Plans, id)
			}
		}
		out.ID = req.ID
		return out, store.writeManagement(state)
	}
	if !exists || !record.Managed {
		return out, ErrNotFound
	}
	config := record.Config
	config.Password = record.Password
	switch req.Command {
	case "remove":
		if req.Confirmation != req.ID {
			return out, errors.New("confirmation mismatch")
		}
		delete(state.Active, req.ID)
		for id, p := range state.Plans {
			if p.ConnectionID == req.ID {
				delete(state.Plans, id)
			}
		}
		return out, store.writeManagement(state)
	case "catalog":
		out.Repositories, err = service.inspector.Repositories(ctx, config)
	case "detail":
		out.Artifacts, err = service.inspector.Artifacts(ctx, config, req.Repository)
	case "history":
		for _, event := range state.Events {
			if event.ConnectionID == req.ID {
				out.Events = append(out.Events, event)
			}
		}
	case "preview":
		if len(state.Plans) >= 20 {
			return out, errors.New("too many active previews; wait for older previews to expire")
		}
		if req.Rule.OlderDays < 0 || req.Rule.OlderDays > 36500 || req.Rule.Keep < 0 || req.Rule.Keep > 1000 {
			return out, errors.New("invalid retention range")
		}
		repos := append([]string(nil), req.Repositories...)
		if req.Repository != "" {
			repos = []string{req.Repository}
		}
		if len(repos) == 0 {
			if strings.TrimSpace(req.Rule.Prefix) == "" {
				return out, errors.New("select repositories or enter an explicit prefix")
			}
			catalog, e := service.inspector.Repositories(ctx, config)
			if e != nil {
				return out, e
			}
			for _, repo := range catalog {
				if strings.HasPrefix(repo, req.Rule.Prefix) {
					repos = append(repos, repo)
				}
			}
		}
		if len(repos) > 100 {
			return out, errors.New("select at most 100 repositories per cleanup")
		}
		plan := registrymonitor.DeletePlan{ID: managementID(), ConnectionID: req.ID, Revision: record.Revision, Created: time.Now().UTC(), Confirmation: record.Name, Revisions: map[string]string{}}
		seen := map[string]bool{}
		for _, repo := range repos {
			if seen[repo] {
				continue
			}
			seen[repo] = true
			items, e := service.inspector.Artifacts(ctx, config, repo)
			if e != nil {
				return out, e
			}
			plan.Revisions[repo] = registrymonitor.ArtifactRevision(items)
			sort.Slice(items, func(i, j int) bool { return items[i].Created.After(items[j].Created) })
			groups := map[string][]registrymonitor.Artifact{}
			order := []string{}
			for _, item := range items {
				if _, ok := groups[item.Digest]; !ok {
					order = append(order, item.Digest)
				}
				groups[item.Digest] = append(groups[item.Digest], item)
			}
			for index, digest := range order {
				group := groups[digest]
				match := false
				protected := index < req.Rule.Keep
				for _, item := range group {
					if req.Rule.Keep > 0 && item.Created.IsZero() {
						protected = true
					}
					if req.Rule.Protect && (item.Tag == "latest" || item.Tag == "stable" || strings.HasPrefix(item.Tag, "release-")) {
						protected = true
					}
					if req.Rule.OlderDays > 0 && (item.Created.IsZero() || item.Created.After(time.Now().AddDate(0, 0, -req.Rule.OlderDays))) {
						protected = true
					}
					ok := true
					if req.Tag != "" {
						ok = item.Tag == req.Tag
					}
					if req.Rule.Pattern != "" {
						var e error
						ok, e = path.Match(req.Rule.Pattern, item.Tag)
						if e != nil {
							return out, e
						}
					}
					if ok {
						match = true
					} else if req.Rule.Pattern != "" {
						protected = true
					}
				}
				if match && !protected {
					target := registrymonitor.DeleteTarget{Repository: repo, Digest: digest}
					for _, item := range group {
						target.Tags = append(target.Tags, item.Tag)
					}
					sort.Strings(target.Tags)
					plan.Targets = append(plan.Targets, target)
				}
			}
			if len(plan.Targets) > 500 {
				return out, errors.New("select at most 500 manifests per cleanup")
			}
		}
		if len(plan.Targets) == 0 {
			return out, errors.New("no matching deletable manifests")
		}
		sort.Slice(plan.Targets, func(i, j int) bool {
			a, b := plan.Targets[i], plan.Targets[j]
			if a.Repository == b.Repository {
				return a.Digest < b.Digest
			}
			return a.Repository < b.Repository
		})
		state.Plans[plan.ID] = plan
		out.Plan = &plan
		err = store.writeManagement(state)
	case "plan":
		p, ok := state.Plans[req.PlanID]
		if !ok || p.ConnectionID != req.ID {
			return out, errors.New("preview expired; create a new preview")
		}
		out.Plan = &p
	case "execute":
		p, ok := state.Plans[req.PlanID]
		if !ok || p.ConnectionID != req.ID || p.Revision != record.Revision || req.Confirmation != p.Confirmation {
			return out, errors.New("preview expired, connection changed, or confirmation mismatch")
		}
		// Recheck every repository before the first deletion, then consume the plan.
		for repo, revision := range p.Revisions {
			items, e := service.inspector.Artifacts(ctx, config, repo)
			if e != nil {
				return out, e
			}
			if registrymonitor.ArtifactRevision(items) != revision {
				return out, errors.New("repository changed; create a new deletion preview")
			}
		}
		delete(state.Plans, req.PlanID)
		event := registrymonitor.ManagementEvent{Time: time.Now().UTC(), ConnectionID: req.ID, Summary: "Deletion started; inspect results before retrying"}
		state.Events = append([]registrymonitor.ManagementEvent{event}, state.Events...)
		if len(state.Events) > 100 {
			state.Events = state.Events[:100]
		}
		if e := store.writeManagement(state); e != nil {
			return out, e
		}
		for _, target := range p.Targets {
			result := registrymonitor.DeleteResult{Repository: target.Repository, Digest: target.Digest}
			if e := service.inspector.DeleteManifest(ctx, config, target); e != nil {
				result.Error = e.Error()
			}
			out.Results = append(out.Results, result)
			state.Events[0].Results = out.Results
			if e := store.writeManagement(state); e != nil {
				return out, e
			}
		}
		state.Events[0].Summary = "Deletion completed; check individual results"
		err = store.writeManagement(state)
	default:
		return out, errors.New("unsupported registry management command")
	}
	return out, err
}
