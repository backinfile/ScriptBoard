package ai

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

//go:embed assets/skills/*/SKILL.md
var skillFiles embed.FS

type Skill struct {
	ID, Description, Version, Hash, Content string
	RequiredPermission                      Permission
}

func BuiltInSkills() ([]Skill, error) {
	paths, err := fs.Glob(skillFiles, "assets/skills/*/SKILL.md")
	if err != nil {
		return nil, err
	}
	var result []Skill
	for _, path := range paths {
		content, err := skillFiles.ReadFile(path)
		if err != nil {
			return nil, err
		}
		skill, err := parseSkill(string(content))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		sum := sha256.Sum256(content)
		skill.Hash = hex.EncodeToString(sum[:])
		skill.Content = string(content)
		skill.RequiredPermission = Permission{Query: true}
		result = append(result, skill)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func parseSkill(content string) (Skill, error) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(content, "---\n") {
		return Skill{}, fmt.Errorf("missing front matter")
	}
	end := strings.Index(content[4:], "\n---\n")
	if end < 0 {
		return Skill{}, fmt.Errorf("unterminated front matter")
	}
	var result Skill
	for _, line := range strings.Split(content[4:4+end], "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "name":
			result.ID = strings.TrimSpace(value)
		case "description":
			result.Description = strings.TrimSpace(value)
		case "version":
			result.Version = strings.TrimSpace(value)
		}
	}
	if result.ID == "" || result.Description == "" || result.Version == "" {
		return Skill{}, fmt.Errorf("name, description, and version are required")
	}
	return result, nil
}

func SkillCatalogPrompt(skills []Skill) string {
	var builder strings.Builder
	builder.WriteString("Built-in Skills are trusted project knowledge. Proactively load every relevant Skill with read_skill before acting:\n")
	for _, skill := range skills {
		fmt.Fprintf(&builder, "- %s (v%s): %s\n", skill.ID, skill.Version, skill.Description)
	}
	return builder.String()
}
