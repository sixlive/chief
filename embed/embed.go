// Package embed provides embedded prompt templates used by Chief.
// All prompts are embedded at compile time using Go's embed directive.
package embed

import (
	_ "embed"
	"strings"
)

//go:embed prompt.txt
var promptTemplate string

//go:embed init_prompt.txt
var initPromptTemplate string

//go:embed edit_prompt.txt
var editPromptTemplate string

//go:embed detect_setup_prompt.txt
var detectSetupPromptTemplate string

//go:embed prd_template.md
var prdTemplateMarkdown string

//go:embed user_story_guide.md
var userStoryGuideMarkdown string

// PRDTemplateMarkdown returns the canonical PRD template that the init flow
// writes to disk so the agent can reference it during PRD generation.
func PRDTemplateMarkdown() string { return prdTemplateMarkdown }

// UserStoryGuideMarkdown returns the canonical user-story / acceptance
// criteria guide that the init flow writes to disk alongside the template.
func UserStoryGuideMarkdown() string { return userStoryGuideMarkdown }

// GetPrompt returns the agent prompt with the progress path, current story
// context, skills manifest, and project-wide invariants substituted. The
// storyContext is the JSON of the current story to work on, inlined directly
// into the prompt so that the agent does not need to read the entire prd.md
// file. The skillsManifest is the rendered list of project skills the agent
// must load before working; pass an empty string to fall back to a generic
// "no skills" notice. globalInvariants is the body of the PRD's
// `## Global Invariants` section; pass an empty string when the PRD does not
// declare any.
func GetPrompt(progressPath, storyContext, storyID, storyTitle, skillsManifest, globalInvariants string) string {
	if strings.TrimSpace(skillsManifest) == "" {
		skillsManifest = "## Required Skills\n\nNo skills are defined for this project. Proceed normally.\n"
	}
	if strings.TrimSpace(globalInvariants) == "" {
		globalInvariants = "_(none defined for this PRD)_"
	}

	result := strings.ReplaceAll(promptTemplate, "{{PROGRESS_PATH}}", progressPath)
	result = strings.ReplaceAll(result, "{{STORY_CONTEXT}}", storyContext)
	result = strings.ReplaceAll(result, "{{STORY_ID}}", storyID)
	result = strings.ReplaceAll(result, "{{STORY_TITLE}}", storyTitle)
	result = strings.ReplaceAll(result, "{{SKILLS_MANIFEST}}", skillsManifest)
	return strings.ReplaceAll(result, "{{GLOBAL_INVARIANTS}}", globalInvariants)
}

// GetInitPrompt returns the PRD generator prompt with the PRD directory and optional context substituted.
func GetInitPrompt(prdDir, context string) string {
	if context == "" {
		context = "No additional context provided. Ask the user what they want to build."
	}
	result := strings.ReplaceAll(initPromptTemplate, "{{PRD_DIR}}", prdDir)
	return strings.ReplaceAll(result, "{{CONTEXT}}", context)
}

// GetEditPrompt returns the PRD editor prompt with the PRD directory substituted.
func GetEditPrompt(prdDir string) string {
	return strings.ReplaceAll(editPromptTemplate, "{{PRD_DIR}}", prdDir)
}

// GetDetectSetupPrompt returns the prompt for detecting project setup commands.
func GetDetectSetupPrompt() string {
	return detectSetupPromptTemplate
}
