package embed

import (
	"strings"
	"testing"
)

func TestGetPrompt(t *testing.T) {
	progressPath := "/path/to/progress.md"
	storyContext := `{"id":"US-001","title":"Test Story"}`
	prompt := GetPrompt(progressPath, storyContext, "US-001", "Test Story", "", "", "")

	// Verify all placeholders were substituted
	if strings.Contains(prompt, "{{PROGRESS_PATH}}") {
		t.Error("Expected {{PROGRESS_PATH}} to be substituted")
	}
	if strings.Contains(prompt, "{{STORY_CONTEXT}}") {
		t.Error("Expected {{STORY_CONTEXT}} to be substituted")
	}
	if strings.Contains(prompt, "{{STORY_ID}}") {
		t.Error("Expected {{STORY_ID}} to be substituted")
	}
	if strings.Contains(prompt, "{{STORY_TITLE}}") {
		t.Error("Expected {{STORY_TITLE}} to be substituted")
	}

	// Verify the commit message contains the exact story ID and title
	if !strings.Contains(prompt, "feat: US-001 - Test Story") {
		t.Error("Expected prompt to contain exact commit message 'feat: US-001 - Test Story'")
	}

	// Verify the progress path appears in the prompt
	if !strings.Contains(prompt, progressPath) {
		t.Errorf("Expected prompt to contain progress path %q", progressPath)
	}

	// Verify the story context is inlined in the prompt
	if !strings.Contains(prompt, storyContext) {
		t.Error("Expected prompt to contain inlined story context")
	}

	// Verify the prompt contains chief-done stop condition
	if !strings.Contains(prompt, "chief-done") {
		t.Error("Expected prompt to contain chief-done instruction")
	}
}

func TestGetPrompt_GlobalInvariantsRendered(t *testing.T) {
	invariants := "- All endpoints must reject cross-tenant ids."
	prompt := GetPrompt("/p", `{}`, "US-001", "Title", "", invariants, "")

	if strings.Contains(prompt, "{{GLOBAL_INVARIANTS}}") {
		t.Error("expected {{GLOBAL_INVARIANTS}} placeholder to be substituted")
	}
	if !strings.Contains(prompt, invariants) {
		t.Error("expected prompt to contain the invariants body")
	}
}

func TestGetPrompt_GlobalInvariantsEmptyFallback(t *testing.T) {
	prompt := GetPrompt("/p", `{}`, "US-001", "Title", "", "", "")

	if strings.Contains(prompt, "{{GLOBAL_INVARIANTS}}") {
		t.Error("expected {{GLOBAL_INVARIANTS}} placeholder to be substituted")
	}
	if !strings.Contains(prompt, "(none defined for this PRD)") {
		t.Error("expected fallback notice when no invariants are provided")
	}
}

func TestGetPrompt_ReviewFindingsRendered(t *testing.T) {
	findings := "- Missing cross-tenant test."
	prompt := GetPrompt("/p", `{}`, "US-001", "Title", "", "", findings)

	if strings.Contains(prompt, "{{REVIEW_FINDINGS}}") {
		t.Error("expected {{REVIEW_FINDINGS}} placeholder to be substituted")
	}
	if !strings.Contains(prompt, "Previous Review Findings") {
		t.Error("expected findings header to appear when findings provided")
	}
	if !strings.Contains(prompt, findings) {
		t.Error("expected prompt to contain the findings body")
	}
}

func TestGetPrompt_ReviewFindingsEmptyOmitsBlock(t *testing.T) {
	prompt := GetPrompt("/p", `{}`, "US-001", "Title", "", "", "")

	if strings.Contains(prompt, "{{REVIEW_FINDINGS}}") {
		t.Error("expected {{REVIEW_FINDINGS}} placeholder to be substituted")
	}
	if strings.Contains(prompt, "Previous Review Findings") {
		t.Error("expected findings header to be omitted when no findings provided")
	}
}

func TestGetPrompt_PreCommitAuditPresent(t *testing.T) {
	prompt := GetPrompt("/p", `{}`, "US-001", "Title", "", "", "")
	if !strings.Contains(prompt, "Pre-Commit Audit") {
		t.Error("expected prompt to contain Pre-Commit Audit section")
	}
	for _, item := range []string{"Deprecated path audit", "Adversarial test audit", "Observability audit", "Invariant audit"} {
		if !strings.Contains(prompt, item) {
			t.Errorf("expected Pre-Commit Audit to contain %q", item)
		}
	}
}

func TestGetPrompt_NoFileReadInstruction(t *testing.T) {
	prompt := GetPrompt("/path/progress.md", `{"id":"US-001"}`, "US-001", "Test Story", "", "", "")

	// The prompt should NOT instruct Claude to read the PRD file
	if strings.Contains(prompt, "Read the PRD") {
		t.Error("Expected prompt to NOT contain 'Read the PRD' file-read instruction")
	}
}

func TestPromptTemplateNotEmpty(t *testing.T) {
	if promptTemplate == "" {
		t.Error("Expected promptTemplate to be embedded and non-empty")
	}
}

func TestGetPrompt_ChiefExclusion(t *testing.T) {
	prompt := GetPrompt("/path/progress.md", `{"id":"US-001"}`, "US-001", "Test Story", "", "", "")

	// The prompt must instruct Claude to never stage or commit .chief/ files
	if !strings.Contains(prompt, ".chief/") {
		t.Error("Expected prompt to contain .chief/ exclusion instruction")
	}
	if !strings.Contains(prompt, "NEVER stage or commit") {
		t.Error("Expected prompt to explicitly say NEVER stage or commit .chief/ files")
	}
}

func TestGetInitPrompt(t *testing.T) {
	prdDir := "/path/to/.chief/prds/main"

	// Test with no context
	prompt := GetInitPrompt(prdDir, "")
	if !strings.Contains(prompt, "No additional context provided") {
		t.Error("Expected default context message")
	}

	// Verify PRD directory is substituted
	if !strings.Contains(prompt, prdDir) {
		t.Errorf("Expected prompt to contain PRD directory %q", prdDir)
	}
	if strings.Contains(prompt, "{{PRD_DIR}}") {
		t.Error("Expected {{PRD_DIR}} to be substituted")
	}

	// Test with context
	context := "Build a todo app"
	promptWithContext := GetInitPrompt(prdDir, context)
	if !strings.Contains(promptWithContext, context) {
		t.Error("Expected context to be substituted in prompt")
	}
}

func TestGetReviewPrompt(t *testing.T) {
	prompt := GetReviewPrompt(
		"/p/prd.md",
		"US-001",
		"Test Story",
		`{"id":"US-001"}`,
		"- Invariant one.",
		"## Required Skills\n\n- skill-a",
		"/p/progress.md",
		"/p/.review/US-001.md",
	)

	for _, placeholder := range []string{
		"{{PRD_PATH}}", "{{STORY_ID}}", "{{STORY_TITLE}}", "{{STORY_CONTEXT}}",
		"{{GLOBAL_INVARIANTS}}", "{{SKILLS_MANIFEST}}", "{{PROGRESS_PATH}}", "{{REVIEW_OUTPUT_PATH}}",
	} {
		if strings.Contains(prompt, placeholder) {
			t.Errorf("expected %s to be substituted", placeholder)
		}
	}

	for _, want := range []string{
		"/p/prd.md", "US-001", "Test Story", "- Invariant one.",
		"skill-a", "/p/progress.md", "/p/.review/US-001.md",
		"<chief-review-done/>", "# APPROVED", "# NEEDS REVISION",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("expected review prompt to contain %q", want)
		}
	}
}

func TestGetReviewPrompt_EmptyInvariantsFallback(t *testing.T) {
	prompt := GetReviewPrompt("/p", "US-001", "T", "{}", "", "", "/prog", "/out")
	if !strings.Contains(prompt, "(none defined for this PRD") {
		t.Error("expected fallback notice when no invariants provided")
	}
}

func TestGetEditPrompt(t *testing.T) {
	prompt := GetEditPrompt("/test/path/prds/main")
	if prompt == "" {
		t.Error("Expected GetEditPrompt() to return non-empty prompt")
	}
	if !strings.Contains(prompt, "/test/path/prds/main") {
		t.Error("Expected prompt to contain the PRD directory path")
	}
}
