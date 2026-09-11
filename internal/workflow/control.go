package workflow

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strings"
)

// Branch IDs belong to edges; labels and rule order can change without reconnecting.
type BranchRule struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Operator string          `json:"operator"`
	Value    json.RawMessage `json:"value,omitempty"`
	Upper    json.RawMessage `json:"upper,omitempty"`
}

func configString(n Node, key, fallback string) string {
	var v string
	if json.Unmarshal(n.Config[key], &v) != nil {
		return fallback
	}
	return v
}
func configInt(n Node, key string, fallback int) int {
	var v int
	if json.Unmarshal(n.Config[key], &v) != nil {
		return fallback
	}
	return v
}
func branchRules(n Node) ([]BranchRule, error) {
	var r []BranchRule
	err := json.Unmarshal(n.Config["rules"], &r)
	return r, err
}
func controlOutlets(n Node) map[string]bool {
	out := map[string]bool{"success": true, "failure": true, "always": true}
	if n.Kind == "condition" {
		out["true"] = true
		out["false"] = true
	}
	if n.Kind == "switch" {
		out["default"] = true
		rules, _ := branchRules(n)
		for _, r := range rules {
			out[r.ID] = true
		}
	}
	return out
}
func validateControl(n Node) error {
	for _, key := range []string{"retryAttempts", "retryDelayMs"} {
		if raw, ok := n.Config[key]; ok {
			var v int
			if json.Unmarshal(raw, &v) != nil {
				return fmt.Errorf("%s must be an integer", key)
			}
		}
	}
	attempts, delay := configInt(n, "retryAttempts", 1), configInt(n, "retryDelayMs", 1000)
	if attempts < 1 || attempts > 10 || delay < 0 || delay > 60000 {
		return fmt.Errorf("retry attempts must be 1..10 and delay 0..60000 ms")
	}
	if mode := configString(n, "retryBackoff", "fixed"); mode != "fixed" && mode != "exponential" {
		return fmt.Errorf("unknown retry backoff")
	}
	switch n.Kind {
	case "switch":
		rules, err := branchRules(n)
		if err != nil || len(rules) == 0 || len(rules) > 50 {
			return fmt.Errorf("switch requires 1..50 rules")
		}
		typ := ValueType(configString(n, "valueType", "string"))
		if _, err := fields([]Field{{Name: "value", Type: typ}}, true); err != nil {
			return err
		}
		mode := configString(n, "matchMode", "first")
		if mode != "first" && mode != "all" {
			return fmt.Errorf("unknown match mode")
		}
		seen := map[string]bool{"success": true, "failure": true, "always": true, "default": true}
		for _, r := range rules {
			if !fieldName.MatchString(r.ID) || seen[r.ID] {
				return fmt.Errorf("invalid or duplicate branch ID %s", r.ID)
			}
			seen[r.ID] = true
			if err := validateRule(r, typ); err != nil {
				return fmt.Errorf("branch %s: %w", r.Name, err)
			}
		}
	case "merge", "wait_for":
		mode := configString(n, "joinMode", "any")
		if mode != "any" && mode != "all" {
			return fmt.Errorf("unknown merge mode")
		}
	case "terminate":
		status := configString(n, "status", "failed")
		if status != "succeeded" && status != "failed" {
			return fmt.Errorf("terminate status must be succeeded or failed")
		}
	case "arithmetic":
		if !strings.Contains("|add|subtract|multiply|divide|modulo|power|min|max|abs|floor|ceil|round|", "|"+configString(n, "operation", "add")+"|") {
			return fmt.Errorf("unknown arithmetic operation")
		}
	case "logic":
		if !strings.Contains("|and|or|not|xor|", "|"+configString(n, "operation", "and")+"|") {
			return fmt.Errorf("unknown logical operation")
		}
	case "compare":
		if !strings.Contains("|eq|ne|gt|gte|lt|lte|contains|starts_with|matches|", "|"+configString(n, "operation", "eq")+"|") {
			return fmt.Errorf("unknown comparison")
		}
	}
	return nil
}
func validateRule(r BranchRule, typ ValueType) error {
	switch r.Operator {
	case "exists", "missing", "is_null", "not_null":
		return nil
	case "eq", "ne", "gt", "gte", "lt", "lte", "between", "contains", "starts_with", "matches":
	default:
		return fmt.Errorf("unknown operator %s", r.Operator)
	}
	if err := ValidateValue(typ, r.Value); err != nil {
		return err
	}
	if r.Operator == "between" {
		if typ != Number && typ != Integer {
			return fmt.Errorf("range requires a number")
		}
		if err := ValidateValue(typ, r.Upper); err != nil {
			return err
		}
		var a, b float64
		json.Unmarshal(r.Value, &a)
		json.Unmarshal(r.Upper, &b)
		if a > b {
			return fmt.Errorf("range lower exceeds upper")
		}
	}
	if strings.Contains("|gt|gte|lt|lte|", "|"+r.Operator+"|") && typ != Number && typ != Integer {
		return fmt.Errorf("ordered comparison requires a number")
	}
	if r.Operator == "contains" || r.Operator == "starts_with" || r.Operator == "matches" {
		if typ != String {
			return fmt.Errorf("text comparison requires a string")
		}
		if r.Operator == "matches" {
			var pattern string
			json.Unmarshal(r.Value, &pattern)
			_, err := regexp.Compile(pattern)
			return err
		}
	}
	return nil
}
func compareValue(raw json.RawMessage, found bool, r BranchRule) (bool, error) {
	switch r.Operator {
	case "exists":
		return found, nil
	case "missing":
		return !found, nil
	case "is_null":
		return found && strings.TrimSpace(string(raw)) == "null", nil
	case "not_null":
		return found && strings.TrimSpace(string(raw)) != "null", nil
	}
	if !found {
		return false, nil
	}
	var a, b any
	if err := json.Unmarshal(raw, &a); err != nil {
		return false, err
	}
	if err := json.Unmarshal(r.Value, &b); err != nil {
		return false, err
	}
	switch r.Operator {
	case "eq":
		return reflect.DeepEqual(a, b), nil
	case "ne":
		return !reflect.DeepEqual(a, b), nil
	case "gt", "gte", "lt", "lte", "between":
		x, ok := a.(float64)
		y, ok2 := b.(float64)
		if !ok || !ok2 {
			return false, fmt.Errorf("comparison requires numbers")
		}
		switch r.Operator {
		case "gt":
			return x > y, nil
		case "gte":
			return x >= y, nil
		case "lt":
			return x < y, nil
		case "lte":
			return x <= y, nil
		default:
			var upper float64
			if err := json.Unmarshal(r.Upper, &upper); err != nil {
				return false, err
			}
			return x >= y && x <= upper, nil
		}
	case "contains", "starts_with", "matches":
		x, ok := a.(string)
		y, ok2 := b.(string)
		if !ok || !ok2 {
			return false, fmt.Errorf("comparison requires strings")
		}
		switch r.Operator {
		case "contains":
			return strings.Contains(x, y), nil
		case "starts_with":
			return strings.HasPrefix(x, y), nil
		default:
			return regexp.MatchString(y, x)
		}
	}
	return false, fmt.Errorf("unknown comparison")
}
func runSwitch(n Node, inputs map[string]json.RawMessage) (map[string]json.RawMessage, []string, error) {
	raw, found := inputs["value"]
	if path := configString(n, "path", ""); path != "" && found {
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, nil, err
		}
		for _, key := range strings.Split(path, ".") {
			object, ok := v.(map[string]any)
			if !ok {
				found = false
				break
			}
			v, found = object[key]
			if !found {
				break
			}
		}
		if found {
			raw, _ = json.Marshal(v)
		}
	}
	typ := ValueType(configString(n, "valueType", "string"))
	if found {
		if err := ValidateValue(typ, raw); err != nil {
			return nil, nil, err
		}
	}
	rules, err := branchRules(n)
	if err != nil {
		return nil, nil, err
	}
	selected := []string{}
	for _, r := range rules {
		ok, err := compareValue(raw, found, r)
		if err != nil {
			return nil, nil, err
		}
		if ok {
			selected = append(selected, r.ID)
			if configString(n, "matchMode", "first") == "first" {
				break
			}
		}
	}
	if len(selected) == 0 {
		selected = []string{"default"}
	}
	matched, _ := json.Marshal(selected)
	out := map[string]json.RawMessage{"matched": matched}
	if found {
		out["value"] = raw
	}
	return out, selected, nil
}
func runCalculation(n Node, inputs map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	operation := configString(n, "operation", "add")
	var result any
	if n.Kind == "logic" {
		operation = configString(n, "operation", "and")
		if err := ValidateValue(Boolean, inputs["a"]); err != nil {
			return nil, err
		}
		var a, b bool
		if err := json.Unmarshal(inputs["a"], &a); err != nil {
			return nil, err
		}
		if operation != "not" {
			if err := ValidateValue(Boolean, inputs["b"]); err != nil {
				return nil, err
			}
			if err := json.Unmarshal(inputs["b"], &b); err != nil {
				return nil, err
			}
		}
		switch operation {
		case "and":
			result = a && b
		case "or":
			result = a || b
		case "not":
			result = !a
		case "xor":
			result = a != b
		default:
			return nil, fmt.Errorf("unknown logic operation")
		}
	} else if n.Kind == "compare" {
		matched, err := compareValue(inputs["a"], true, BranchRule{Operator: configString(n, "operation", "eq"), Value: inputs["b"]})
		if err != nil {
			return nil, err
		}
		result = matched
	} else {
		var a, b float64
		if err := ValidateValue(Number, inputs["a"]); err != nil {
			return nil, err
		}
		json.Unmarshal(inputs["a"], &a)
		unary := operation == "abs" || operation == "floor" || operation == "ceil" || operation == "round"
		if !unary {
			if err := ValidateValue(Number, inputs["b"]); err != nil {
				return nil, err
			}
			json.Unmarshal(inputs["b"], &b)
		}
		var v float64
		switch operation {
		case "add":
			v = a + b
		case "subtract":
			v = a - b
		case "multiply":
			v = a * b
		case "divide":
			if b == 0 {
				return nil, fmt.Errorf("division by zero")
			}
			v = a / b
		case "modulo":
			if b == 0 {
				return nil, fmt.Errorf("modulo by zero")
			}
			v = math.Mod(a, b)
		case "power":
			v = math.Pow(a, b)
		case "min":
			v = math.Min(a, b)
		case "max":
			v = math.Max(a, b)
		case "abs":
			v = math.Abs(a)
		case "floor":
			v = math.Floor(a)
		case "ceil":
			v = math.Ceil(a)
		case "round":
			v = math.Round(a)
		default:
			return nil, fmt.Errorf("unknown arithmetic operation")
		}
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, fmt.Errorf("arithmetic result is not finite")
		}
		result = v
	}
	raw, err := json.Marshal(result)
	return map[string]json.RawMessage{"result": raw}, err
}
