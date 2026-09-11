package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

var ErrUncertain = errors.New("process supervision uncertain")

type Executor interface {
	Execute(context.Context, Run, Node, map[string]json.RawMessage, func(string) error) (map[string]json.RawMessage, error)
}
type Engine struct {
	Repository Repository
	Executor   Executor
	cancel     context.CancelFunc
	wg         sync.WaitGroup
}

func (e *Engine) Start(ctx context.Context) error {
	if err := e.Repository.Recover(ctx); err != nil {
		return err
	}
	ctx, e.cancel = context.WithCancel(ctx)
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				e.dispatch(ctx)
			}
		}
	}()
	return nil
}
func (e *Engine) Close() {
	if e.cancel != nil {
		e.cancel()
		e.wg.Wait()
	}
}
func (e *Engine) dispatch(ctx context.Context) {
	ids, err := e.Repository.Queued(ctx)
	if err != nil {
		return
	}
	for _, id := range ids {
		run, err := e.Repository.Run(ctx, id)
		if err != nil {
			continue
		}
		if run.Status != "queued" {
			continue
		}
		ok, err := e.Repository.Claim(ctx, run.ID)
		if err != nil || !ok {
			continue
		}
		e.wg.Add(1)
		go func() { defer e.wg.Done(); e.execute(ctx, run) }()
	}
}

// activation uses settled predecessors. Unselected routes are skipped instead of
// resolving their missing data or waiting forever at a mutually exclusive join.
func activation(g Graph, n Node, states map[string]string, routes map[string]map[string]bool) bool {
	count, matched := 0, 0
	for _, edge := range g.Dependencies {
		if edge.To != n.ID {
			continue
		}
		count++
		outlet := edge.Outlet
		if outlet == "" {
			outlet = "success"
		}
		if routes[edge.From][outlet] {
			matched++
		}
	}
	if count > 0 {
		defaultJoin := "all"
		if n.Kind == "merge" {
			defaultJoin = "any"
		}
		if (n.Kind == "merge" || n.Kind == "wait_for") && configString(n, "joinMode", defaultJoin) == "any" {
			if matched == 0 {
				return false
			}
		} else if matched != count {
			return false
		}
	}
	for _, edge := range g.Links {
		if edge.To.Node != n.ID {
			continue
		}
		status := states[edge.From.Node]
		if status != "succeeded" && !(status == "failed" && edge.From.Field == "_error") {
			return false
		}
	}
	return true
}
func (e *Engine) waitControl(ctx context.Context, runID string, duration time.Duration) error {
	deadline := time.Now().Add(duration)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		run, err := e.Repository.Run(ctx, runID)
		if err != nil {
			return err
		}
		if run.Status == "cancelling" {
			return fmt.Errorf("cancelled")
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil
		}
		if remaining > 100*time.Millisecond {
			remaining = 100 * time.Millisecond
		}
		timer := time.NewTimer(remaining)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
func (e *Engine) executeNode(ctx context.Context, run Run, node Node, inputs map[string]json.RawMessage, started func(string) error) (map[string]json.RawMessage, []string, error) {
	outlets := []string{"success", "always"}
	switch node.Kind {
	case "start":
		return run.Snapshot.Entry.Values, outlets, nil
	case "end", "merge", "wait_for", "terminate":
		return inputs, outlets, nil
	case "literal":
		return map[string]json.RawMessage{"value": node.Config["value"]}, outlets, nil
	case "lookup":
		value, err := lookupValue(run.Snapshot, configString(node, "path", ""))
		return map[string]json.RawMessage{"value": value}, outlets, err
	case "condition":
		if err := ValidateValue(Boolean, inputs["condition"]); err != nil {
			return nil, nil, err
		}
		var matched bool
		if err := json.Unmarshal(inputs["condition"], &matched); err != nil {
			return nil, nil, err
		}
		if matched {
			outlets = append(outlets, "true")
		} else {
			outlets = []string{"false", "always"}
		}
		return map[string]json.RawMessage{"matched": inputs["condition"]}, outlets, nil
	case "switch":
		output, selected, err := runSwitch(node, inputs)
		return output, append(selected, outlets...), err
	case "arithmetic", "compare", "logic":
		output, err := runCalculation(node, inputs)
		return output, outlets, err
	case "wait":
		var seconds float64
		if err := ValidateValue(Number, inputs["seconds"]); err != nil {
			return nil, nil, err
		}
		json.Unmarshal(inputs["seconds"], &seconds)
		if seconds < 0 || seconds > 86400 {
			return nil, nil, fmt.Errorf("wait seconds must be 0..86400")
		}
		err := e.waitControl(ctx, run.ID, time.Duration(seconds*float64(time.Second)))
		return map[string]json.RawMessage{}, outlets, err
	default:
		output, err := e.Executor.Execute(ctx, run, node, inputs, started)
		return output, outlets, err
	}
}
func (e *Engine) execute(ctx context.Context, run Run) {
	g := run.Snapshot.Workflow.Graph
	order, err := Compile(g)
	finish := func(status, message string, result map[string]json.RawMessage) {
		_ = e.Repository.Finish(context.Background(), run.ID, status, message, result)
	}
	if err != nil {
		finish("failed", err.Error(), nil)
		return
	}
	results := map[string]map[string]json.RawMessage{}
	states := map[string]string{}
	routes := map[string]map[string]bool{}
	var final map[string]json.RawMessage
	failure := ""
	for _, id := range order {
		current, err := e.Repository.Run(ctx, run.ID)
		if err != nil {
			if ctx.Err() != nil {
				finish("needs_attention", ctx.Err().Error(), nil)
			}
			return
		}
		if current.Status == "cancelling" {
			finish("cancelled", "cancelled", nil)
			return
		}
		var node Node
		for _, n := range g.Nodes {
			if n.ID == id {
				node = n
				break
			}
		}
		if !activation(g, node, states, routes) {
			states[id] = "skipped"
			if err := e.Repository.FinishStep(ctx, run.ID, id, "skipped", "inactive upstream route", nil); err != nil {
				return
			}
			continue
		}
		inputs, inputErr := ResolveInputs(g, id, results)
		if err = e.Repository.BeginStep(ctx, run.ID, id); err != nil {
			if latest, readErr := e.Repository.Run(context.Background(), run.ID); readErr == nil && latest.Status == "cancelling" {
				finish("cancelled", "cancelled", nil)
			}
			return
		}
		var output map[string]json.RawMessage
		var selected []string
		attempts := []Attempt{}
		maxAttempts := configInt(node, "retryAttempts", 1)
		for number := 1; number <= maxAttempts; number++ {
			if number > 1 {
				delay := configInt(node, "retryDelayMs", 1000)
				if configString(node, "retryBackoff", "fixed") == "exponential" {
					delay = delay * (1 << (number - 2))
					if delay > 60000 {
						delay = 60000
					}
				}
				if err = e.waitControl(ctx, run.ID, time.Duration(delay)*time.Millisecond); err != nil {
					break
				}
				if err = e.Repository.RetryStep(ctx, run.ID, id); err != nil {
					break
				}
			}
			attempts = append(attempts, Attempt{Number: number, Status: "running", StartedAt: time.Now().UnixNano()})
			index := len(attempts) - 1
			if err = e.Repository.StepRuntime(ctx, run.ID, id, attempts, nil); err != nil {
				finish("needs_attention", err.Error(), nil)
				return
			}
			err = inputErr
			if err == nil {
				output, selected, err = e.executeNode(ctx, run, node, inputs, func(process string) error {
					attempts[index].ProcessID = process
					if err := e.Repository.StepRuntime(ctx, run.ID, id, attempts, nil); err != nil {
						return fmt.Errorf("%w: %v", ErrUncertain, err)
					}
					return e.Repository.StepProcess(ctx, run.ID, id, process)
				})
			}
			if err == nil {
				for _, field := range node.Outputs {
					raw, exists := output[field.Name]
					if !exists {
						if field.Required {
							err = fmt.Errorf("missing output %s", field.Name)
							break
						}
						continue
					}
					if check := ValidateFieldValue(field, raw); check != nil {
						err = fmt.Errorf("output %s: %w", field.Name, check)
						break
					}
				}
			}
			// Honor cancellation even when the executor returns success during the request.
			if latest, readErr := e.Repository.Run(ctx, run.ID); readErr != nil {
				err = fmt.Errorf("%w: %v", ErrUncertain, readErr)
			} else if latest.Status == "cancelling" {
				err = fmt.Errorf("cancelled")
			}
			attempts[index].FinishedAt = time.Now().UnixNano()
			attempts[index].Status = "succeeded"
			if err != nil {
				attempts[index].Status = "failed"
				attempts[index].Error = err.Error()
			}
			if recordErr := e.Repository.StepRuntime(context.Background(), run.ID, id, attempts, nil); recordErr != nil {
				finish("needs_attention", recordErr.Error(), nil)
				return
			}
			if err == nil || inputErr != nil || ctx.Err() != nil || errors.Is(err, ErrUncertain) {
				break
			}
			latest, readErr := e.Repository.Run(ctx, run.ID)
			if readErr != nil || latest.Status == "cancelling" {
				break
			}
			if number < maxAttempts {
				_ = e.Repository.FinishStep(ctx, run.ID, id, "retrying", err.Error(), nil)
			}
		}
		if err != nil {
			status := "failed"
			if ctx.Err() != nil || errors.Is(err, ErrUncertain) {
				status = "needs_attention"
			} else if latest, readErr := e.Repository.Run(ctx, run.ID); readErr == nil && latest.Status == "cancelling" {
				status = "cancelled"
			}
			raw, _ := json.Marshal(map[string]any{"nodeId": id, "nodeName": node.Name, "message": err.Error(), "attempts": len(attempts)})
			output = map[string]json.RawMessage{"_error": raw}
			selected = []string{"failure", "always"}
			if status != "failed" {
				if len(attempts) > 0 {
					attempts[len(attempts)-1].Status = status
					_ = e.Repository.StepRuntime(context.Background(), run.ID, id, attempts, nil)
				}
				_ = e.Repository.FinishStep(context.Background(), run.ID, id, status, err.Error(), output)
				finish(status, err.Error(), nil)
				return
			}
			failure = err.Error()
			states[id] = "failed"
			if recordErr := e.Repository.FinishStep(ctx, run.ID, id, "failed", err.Error(), output); recordErr != nil {
				finish("needs_attention", recordErr.Error(), nil)
				return
			}
			handled := false
			for _, edge := range g.Dependencies {
				if edge.From == id && (edge.Outlet == "failure" || edge.Outlet == "always") {
					handled = true
				}
			}
			for _, edge := range g.Links {
				if edge.From.Node == id && edge.From.Field == "_error" {
					handled = true
				}
			}
			if !handled {
				_ = e.Repository.StepRuntime(ctx, run.ID, id, attempts, selected)
				finish("failed", err.Error(), nil)
				return
			}
		} else {
			states[id] = "succeeded"
			if err = e.Repository.FinishStep(ctx, run.ID, id, "succeeded", "", output); err != nil {
				finish("needs_attention", err.Error(), nil)
				return
			}
		}
		results[id] = output
		routes[id] = map[string]bool{}
		for _, outlet := range selected {
			routes[id][outlet] = true
		}
		if err = e.Repository.StepRuntime(ctx, run.ID, id, attempts, selected); err != nil {
			finish("needs_attention", err.Error(), nil)
			return
		}
		if node.Kind == "end" {
			final = output
		}
		if node.Kind == "terminate" && states[id] == "succeeded" {
			if err = e.Repository.SkipPending(ctx, run.ID); err != nil {
				finish("needs_attention", err.Error(), nil)
				return
			}
			finish(configString(node, "status", "failed"), configString(node, "message", "explicit workflow termination"), output)
			return
		}
	}
	if failure != "" {
		finish("failed", failure, final)
	} else {
		finish("succeeded", "", final)
	}
}
