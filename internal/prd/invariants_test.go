package prd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadInvariantsFromString(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name: "present with bullets",
			content: `# PRD: Test

## Overview
Some prose.

## Global Invariants

- Every endpoint accepting a thread_id must reject ids the user does not own.
- Every chat broadcast event includes threadId in payload.

## User Stories

### T-001: First story
`,
			want: "- Every endpoint accepting a thread_id must reject ids the user does not own.\n- Every chat broadcast event includes threadId in payload.",
		},
		{
			name: "absent",
			content: `# PRD: Test

## Overview
No invariants here.

## User Stories

### T-001: First story
`,
			want: "",
		},
		{
			name: "case-insensitive heading with annotation",
			content: `# PRD

## GLOBAL INVARIANTS (project-wide rules)

- Rule one.

## Next Section
`,
			want: "- Rule one.",
		},
		{
			name: "stops at next ## heading, allows ### subheadings",
			content: `## Global Invariants

- Top rule.

### Subgroup
- Nested rule.

## Stories

### T-001
`,
			want: "- Top rule.\n\n### Subgroup\n- Nested rule.",
		},
		{
			name: "trailing whitespace stripped",
			content: `## Global Invariants


- Rule.


`,
			want: "- Rule.",
		},
		{
			name: "section at end of file",
			content: `# PRD

## Global Invariants
- Final rule.
`,
			want: "- Final rule.",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := LoadInvariantsFromString(tc.content)
			if got != tc.want {
				t.Errorf("LoadInvariantsFromString() mismatch\n  want: %q\n   got: %q", tc.want, got)
			}
		})
	}
}

func TestLoadInvariants_FromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prd.md")
	body := "# PRD\n\n## Global Invariants\n\n- A rule.\n\n## Stories\n\n### T-001: First\n"
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := LoadInvariants(path)
	if err != nil {
		t.Fatalf("LoadInvariants() error: %v", err)
	}
	if !strings.Contains(got, "A rule.") {
		t.Errorf("expected loaded invariants to contain rule, got %q", got)
	}
}

func TestLoadInvariants_MissingFile(t *testing.T) {
	_, err := LoadInvariants("/nonexistent/prd.md")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoadInvariants_NoSectionReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prd.md")
	body := "# PRD\n\n## Stories\n\n### T-001: First\n"
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := LoadInvariants(path)
	if err != nil {
		t.Fatalf("LoadInvariants() error: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty string when section absent, got %q", got)
	}
}
