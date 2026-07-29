package ai

import "testing"

func TestBuiltInSkillsAreDiscoverableAndVersioned(t *testing.T) {
	catalog, err := BuiltInSkills()
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) != 5 {
		t.Fatalf("skill count = %d", len(catalog))
	}
	for _, skill := range catalog {
		if skill.ID == "" || skill.Description == "" || skill.Version == "" || len(skill.Hash) != 64 {
			t.Fatalf("invalid skill: %#v", skill)
		}
		if skill.Content == "" {
			t.Fatalf("empty skill content: %s", skill.ID)
		}
	}
}

func TestParseSkillAcceptsWindowsLineEndings(t *testing.T) {
	skill, err := parseSkill("---\r\nname: windows\r\ndescription: CRLF fixture\r\nversion: 1\r\n---\r\nBody\r\n")
	if err != nil {
		t.Fatalf("parse CRLF skill: %v", err)
	}
	if skill.ID != "windows" || skill.Description != "CRLF fixture" || skill.Version != "1" {
		t.Fatalf("skill = %#v", skill)
	}
}
