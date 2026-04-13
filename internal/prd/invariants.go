package prd

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// invariantsHeadingRegex matches a level-2 heading whose text starts with
// "Global Invariants" (case-insensitive). Trailing text after the phrase is
// allowed (e.g., "## Global Invariants (project rules)") so authors can
// annotate the heading without breaking the parser.
var invariantsHeadingRegex = regexp.MustCompile(`(?i)^##\s+global\s+invariants\b`)

// LoadInvariants reads the prd.md file at path and returns the body of the
// `## Global Invariants` section, trimmed of surrounding whitespace. The body
// runs from the line after the heading up to (but not including) the next
// `## ` (or higher) heading, or end of file.
//
// Returns an empty string with no error when the section is absent so that
// PRDs predating the invariants format continue to load cleanly. Returns an
// error only on read failure.
func LoadInvariants(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("failed to read PRD file: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// Allow long lines — PRDs can have wide tables, embedded JSON, etc.
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	return extractInvariants(scanner), nil
}

// LoadInvariantsFromString parses the invariants section out of an in-memory
// PRD body. Useful for tests and for callers that already hold the markdown.
func LoadInvariantsFromString(content string) string {
	return extractInvariants(bufio.NewScanner(strings.NewReader(content)))
}

func extractInvariants(scanner *bufio.Scanner) string {
	var (
		inSection bool
		body      strings.Builder
	)

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if !inSection {
			if invariantsHeadingRegex.MatchString(trimmed) {
				inSection = true
			}
			continue
		}

		// Stop at the next top-level (#) or section-level (##) heading.
		// Sub-headings (### and deeper) belong to the invariants section.
		if strings.HasPrefix(trimmed, "# ") || strings.HasPrefix(trimmed, "## ") {
			break
		}

		body.WriteString(line)
		body.WriteString("\n")
	}

	return strings.TrimSpace(body.String())
}
