// Package skills discovers SKILL.md files in a project workspace and renders
// a manifest that can be injected into agent prompts. Skills encode coding
// standards and guidelines that the agent must load before working on a task.
//
// Skills are looked up in two well-known locations relative to the project
// root:
//
//	.claude/skills/<name>/SKILL.md
//	.agents/skills/<name>/SKILL.md
//
// Each SKILL.md is expected to begin with YAML frontmatter containing at
// least a `name` and `description` field, e.g.
//
//	---
//	name: prd-generator
//	description: Generate PRDs through a structured discovery interview.
//	---
package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// SkillRoots are the directories (relative to the project root) that are
// scanned for skills, in priority order. Earlier roots win on name conflicts.
var SkillRoots = []string{
	filepath.Join(".claude", "skills"),
	filepath.Join(".agents", "skills"),
}

// Skill is a discovered skill on disk.
type Skill struct {
	// Name is the value of the `name` field in the SKILL.md frontmatter.
	// Falls back to the parent directory name if missing.
	Name string
	// Description is the value of the `description` field in the SKILL.md
	// frontmatter. May be empty.
	Description string
	// Path is the absolute path to the SKILL.md file.
	Path string
	// Root is the absolute path to the skill root directory the skill was
	// discovered in (e.g. /repo/.claude/skills).
	Root string
}

type frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// Discover walks the well-known skill roots under workDir and returns every
// SKILL.md it finds. Missing roots are silently skipped. On a name collision
// the skill discovered in the earlier root (per SkillRoots) wins.
func Discover(workDir string) ([]Skill, error) {
	seen := map[string]Skill{}
	var order []string

	for _, rel := range SkillRoots {
		root := filepath.Join(workDir, rel)
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			continue
		}

		entries, err := os.ReadDir(root)
		if err != nil {
			return nil, fmt.Errorf("read skill root %s: %w", root, err)
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			skillPath := filepath.Join(root, entry.Name(), "SKILL.md")
			if _, err := os.Stat(skillPath); err != nil {
				continue
			}

			s, err := loadSkill(skillPath, root, entry.Name())
			if err != nil {
				// A malformed SKILL.md should not break the whole loop.
				// Surface it as a skill with a warning description so the
				// model still sees it.
				s = Skill{
					Name:        entry.Name(),
					Description: fmt.Sprintf("(failed to parse SKILL.md frontmatter: %v)", err),
					Path:        skillPath,
					Root:        root,
				}
			}
			if _, exists := seen[s.Name]; exists {
				continue
			}
			seen[s.Name] = s
			order = append(order, s.Name)
		}
	}

	out := make([]Skill, 0, len(order))
	for _, name := range order {
		out = append(out, seen[name])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func loadSkill(path, root, dirName string) (Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}

	fm, err := parseFrontmatter(data)
	if err != nil {
		return Skill{}, err
	}

	name := strings.TrimSpace(fm.Name)
	if name == "" {
		name = dirName
	}

	return Skill{
		Name:        name,
		Description: strings.TrimSpace(fm.Description),
		Path:        path,
		Root:        root,
	}, nil
}

func parseFrontmatter(data []byte) (frontmatter, error) {
	text := string(data)
	if !strings.HasPrefix(text, "---") {
		return frontmatter{}, fmt.Errorf("missing frontmatter")
	}
	rest := strings.TrimPrefix(text, "---")
	// Strip a leading newline if present.
	rest = strings.TrimLeft(rest, "\r\n")

	end := strings.Index(rest, "\n---")
	if end < 0 {
		return frontmatter{}, fmt.Errorf("unterminated frontmatter")
	}
	block := rest[:end]

	var fm frontmatter
	if err := yaml.Unmarshal([]byte(block), &fm); err != nil {
		return frontmatter{}, fmt.Errorf("yaml: %w", err)
	}
	return fm, nil
}

// RenderManifest produces the markdown block that gets inlined into the agent
// prompt. The wording is intentionally forceful: the model must read the
// SKILL.md files for any skill that could plausibly apply before doing any
// work.
//
// workDir is the project root used to render skill paths as repo-relative
// paths in the manifest, which keeps it stable across agents and machines.
func RenderManifest(skills []Skill, workDir string) string {
	var b strings.Builder

	b.WriteString("## Required Skills (READ BEFORE TOUCHING ANY CODE)\n\n")

	if len(skills) == 0 {
		b.WriteString("No skills are defined for this project. Proceed normally.\n")
		return b.String()
	}

	b.WriteString("This project has skills installed under `.claude/skills/` and/or `.agents/skills/`. ")
	b.WriteString("Skills encode the coding standards, conventions, and review rules that this codebase REQUIRES you to follow. ")
	b.WriteString("They are not optional context — they are the rules of the road.\n\n")

	b.WriteString("**MANDATORY PROCEDURE — do this BEFORE writing, editing, or planning any code:**\n\n")
	b.WriteString("1. Read the skill list below. For EVERY skill whose description could plausibly apply to the story you are about to work on, you MUST read its full `SKILL.md` file into context using your file-reading tool.\n")
	b.WriteString("2. If a skill's `SKILL.md` references additional files (e.g. a `references/` directory, templates, checklists), read those too.\n")
	b.WriteString("3. When in doubt, READ the skill. The cost of reading an irrelevant skill is trivial; the cost of skipping a relevant one is broken code, failed reviews, and wasted iterations.\n")
	b.WriteString("4. Apply every rule from the loaded skills to the code you write. Do not paraphrase, do not improvise — follow the skill.\n")
	b.WriteString("5. If two skills conflict, prefer the more specific one and note the conflict in your progress report.\n\n")

	b.WriteString("**Do NOT proceed to implementation until you have completed steps 1-2.** If you skip the skills, your work will be wrong.\n\n")

	b.WriteString("### Available skills\n\n")
	for _, s := range skills {
		rel, err := filepath.Rel(workDir, s.Path)
		if err != nil {
			rel = s.Path
		}
		desc := s.Description
		if desc == "" {
			desc = "(no description provided — read the SKILL.md to find out what it covers)"
		}
		fmt.Fprintf(&b, "- **%s** — %s\n  Read: `%s`\n", s.Name, desc, rel)
	}

	return b.String()
}
