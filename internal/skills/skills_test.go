package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkill(t *testing.T, root, name, body string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscover_BothRoots(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, ".claude", "skills")
	agentsRoot := filepath.Join(tmp, ".agents", "skills")

	writeSkill(t, claudeRoot, "go-style", `---
name: go-style
description: Go style rules.
---

body
`)
	writeSkill(t, agentsRoot, "testing", `---
name: testing
description: How to write tests.
---
`)

	got, err := Discover(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 skills, got %d: %+v", len(got), got)
	}
	names := []string{got[0].Name, got[1].Name}
	if names[0] != "go-style" || names[1] != "testing" {
		t.Errorf("unexpected skill order: %v", names)
	}
	if got[0].Description != "Go style rules." {
		t.Errorf("description not parsed: %q", got[0].Description)
	}
}

func TestDiscover_ClaudeWinsOnConflict(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, ".claude", "skills")
	agentsRoot := filepath.Join(tmp, ".agents", "skills")

	writeSkill(t, claudeRoot, "shared", `---
name: shared
description: claude version
---
`)
	writeSkill(t, agentsRoot, "shared", `---
name: shared
description: agents version
---
`)

	got, err := Discover(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(got))
	}
	if got[0].Description != "claude version" {
		t.Errorf("expected claude version to win, got %q", got[0].Description)
	}
}

func TestDiscover_NoSkills(t *testing.T) {
	tmp := t.TempDir()
	got, err := Discover(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 skills, got %d", len(got))
	}
}

func TestDiscover_MalformedFrontmatterStillSurfaces(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, ".claude", "skills")
	writeSkill(t, root, "broken", "no frontmatter here\n")

	got, err := Discover(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 skill (the broken one), got %d", len(got))
	}
	if got[0].Name != "broken" {
		t.Errorf("expected fallback to dir name, got %q", got[0].Name)
	}
	if !strings.Contains(got[0].Description, "failed to parse") {
		t.Errorf("expected parse-failure description, got %q", got[0].Description)
	}
}

func TestRenderManifest_Empty(t *testing.T) {
	out := RenderManifest(nil, "/repo")
	if !strings.Contains(out, "No skills are defined") {
		t.Errorf("expected empty manifest message, got: %q", out)
	}
}

func TestRenderManifest_WithSkills(t *testing.T) {
	skills := []Skill{
		{Name: "go-style", Description: "Go style rules.", Path: "/repo/.claude/skills/go-style/SKILL.md"},
		{Name: "testing", Description: "", Path: "/repo/.agents/skills/testing/SKILL.md"},
	}
	out := RenderManifest(skills, "/repo")

	for _, want := range []string{
		"MANDATORY",
		"BEFORE",
		"go-style",
		"Go style rules.",
		".claude/skills/go-style/SKILL.md",
		"testing",
		".agents/skills/testing/SKILL.md",
		"no description provided",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("manifest missing %q\n---\n%s", want, out)
		}
	}
}
