package web

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"scriptboard/internal/hostfiles"
)

func TestParsePermissionChangeKeepsCheckedWindowsInheritanceEnabled(t *testing.T) {
	request := httptest.NewRequest("POST", "/resources/files/permissions", strings.NewReader(url.Values{
		"inheritance_enabled": {"0", "1"},
	}.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	change, err := parsePermissionChange(request, hostfiles.Permissions{Platform: "windows"})
	if err != nil {
		t.Fatal(err)
	}
	if change.InheritanceEnabled == nil || !*change.InheritanceEnabled {
		t.Fatalf("inheritance enabled = %v, want true", change.InheritanceEnabled)
	}
}

func TestWindowsPermissionEditorPreservesExactRuleChildScope(t *testing.T) {
	view := newFilePermissionsView(hostfiles.Permissions{
		Platform: "windows",
		Rules: []hostfiles.AccessRule{
			{Principal: hostfiles.Principal{ID: "S-1-5-21-child"}, Kind: "allow", AppliesTo: "children"},
			{Principal: hostfiles.Principal{ID: "S-1-5-21-item"}, Kind: "allow", AppliesTo: "this_item"},
		},
	})

	if view.WindowsRules[0].AppliesTo != "children" || view.WindowsRules[1].AppliesTo != "this_item" {
		t.Fatalf("view rule scopes = (%q, %q)", view.WindowsRules[0].AppliesTo, view.WindowsRules[1].AppliesTo)
	}

	request := httptest.NewRequest("POST", "/resources/files/permissions", strings.NewReader(url.Values{
		"principal":              {"S-1-5-21-child"},
		"read":                   {"1"},
		"apply_rule_to_children": {"1"},
		"rule_applies_to":        {"files"},
	}.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	change, err := parsePermissionChange(request, hostfiles.Permissions{Platform: "windows", Directory: true})
	if err != nil {
		t.Fatal(err)
	}
	if !change.ApplyRuleToChildren || change.RuleAppliesTo != "files" {
		t.Fatalf("parsed child scope = (%t, %q)", change.ApplyRuleToChildren, change.RuleAppliesTo)
	}
}
