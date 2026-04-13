package loop

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/minicodemonkey/chief/internal/prd"
)

func TestParseVerdictBytes_Approved(t *testing.T) {
	verdict := parseVerdictBytes([]byte("# APPROVED\n\nLooks good. (Suggestion: rename foo.)"), "/tmp/v.md")
	if !verdict.Approved {
		t.Error("expected approved")
	}
	if verdict.Body != "Looks good. (Suggestion: rename foo.)" {
		t.Errorf("unexpected body: %q", verdict.Body)
	}
	if verdict.Path != "/tmp/v.md" {
		t.Errorf("unexpected path: %q", verdict.Path)
	}
}

func TestParseVerdictBytes_NeedsRevision(t *testing.T) {
	verdict := parseVerdictBytes([]byte("# NEEDS REVISION\n\n- Cross-tenant hole in ChatRequest.\n- Missing test."), "")
	if verdict.Approved {
		t.Error("expected not approved")
	}
	if verdict.Body == "" {
		t.Error("expected non-empty findings body")
	}
}

func TestParseVerdictBytes_LeadingBlankLines(t *testing.T) {
	verdict := parseVerdictBytes([]byte("\n\n\n# APPROVED\n"), "")
	if !verdict.Approved {
		t.Error("expected approved with leading blank lines")
	}
}

func TestParseVerdictBytes_Empty(t *testing.T) {
	verdict := parseVerdictBytes([]byte(""), "")
	if verdict.Approved {
		t.Error("empty verdict must NOT be approved (fail-closed)")
	}
}

func TestParseVerdictBytes_MalformedFailsClosed(t *testing.T) {
	verdict := parseVerdictBytes([]byte("just some prose without a header"), "")
	if verdict.Approved {
		t.Error("malformed verdict must NOT be approved (fail-closed)")
	}
}

func TestLoadPendingReviewFindings_NoFile(t *testing.T) {
	dir := t.TempDir()
	prdPath := filepath.Join(dir, "prd.md")
	if got := loadPendingReviewFindings(prdPath, "T-001"); got != "" {
		t.Errorf("expected empty findings when file absent, got %q", got)
	}
}

func TestLoadPendingReviewFindings_ApprovedReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	prdPath := filepath.Join(dir, "prd.md")
	if err := os.MkdirAll(reviewDir(prdPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reviewFilePath(prdPath, "T-001"), []byte("# APPROVED\n\nLGTM"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := loadPendingReviewFindings(prdPath, "T-001"); got != "" {
		t.Errorf("approved verdicts must NOT produce findings; got %q", got)
	}
}

func TestLoadPendingReviewFindings_NeedsRevisionReturnsBody(t *testing.T) {
	dir := t.TempDir()
	prdPath := filepath.Join(dir, "prd.md")
	if err := os.MkdirAll(reviewDir(prdPath), 0755); err != nil {
		t.Fatal(err)
	}
	body := "- Missing cross-tenant test.\n- Old path still callable."
	if err := os.WriteFile(reviewFilePath(prdPath, "T-001"), []byte("# NEEDS REVISION\n\n"+body), 0644); err != nil {
		t.Fatal(err)
	}
	got := loadPendingReviewFindings(prdPath, "T-001")
	if got != body {
		t.Errorf("expected findings body, got %q", got)
	}
}

func TestArchiveApprovedVerdict(t *testing.T) {
	dir := t.TempDir()
	prdPath := filepath.Join(dir, "prd.md")
	if err := os.MkdirAll(reviewDir(prdPath), 0755); err != nil {
		t.Fatal(err)
	}

	src := reviewFilePath(prdPath, "T-001")
	if err := os.WriteFile(src, []byte("# APPROVED\nbody"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := archiveApprovedVerdict(prdPath, "T-001"); err != nil {
		t.Fatalf("archive: %v", err)
	}

	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("expected source verdict to be removed after archive")
	}
	dst := approvedArchivePath(prdPath, "T-001")
	if _, err := os.Stat(dst); err != nil {
		t.Errorf("expected archived verdict at %s: %v", dst, err)
	}

	// Subsequent archive call must rotate cleanly even when src is gone.
	if err := archiveApprovedVerdict(prdPath, "T-001"); err != nil {
		t.Errorf("idempotent archive should not error, got %v", err)
	}
}

func TestArchiveApprovedVerdict_OverwritesPriorArchive(t *testing.T) {
	dir := t.TempDir()
	prdPath := filepath.Join(dir, "prd.md")
	if err := os.MkdirAll(reviewDir(prdPath), 0755); err != nil {
		t.Fatal(err)
	}

	// Pre-existing archive from a prior approval.
	if err := os.WriteFile(approvedArchivePath(prdPath, "T-001"), []byte("OLD"), 0644); err != nil {
		t.Fatal(err)
	}
	// New verdict to archive.
	if err := os.WriteFile(reviewFilePath(prdPath, "T-001"), []byte("NEW"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := archiveApprovedVerdict(prdPath, "T-001"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(approvedArchivePath(prdPath, "T-001"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "NEW" {
		t.Errorf("expected archive to be replaced with NEW verdict, got %q", string(data))
	}
}

func TestReviewDir_Path(t *testing.T) {
	got := reviewDir("/a/b/prd.md")
	want := filepath.Join("/a/b", ".review")
	if got != want {
		t.Errorf("reviewDir = %q, want %q", got, want)
	}
}

// reviewTestPRD writes a minimal in-progress PRD to dir and returns its path.
// The returned PRD has a single in-progress story with the given ID.
func reviewTestPRD(t *testing.T, dir, storyID string) string {
	t.Helper()
	body := "# PRD\n\n## Stories\n\n### " + storyID + ": Test Story\n**Status:** in-progress\n**Priority:** 1\n\n- [ ] Acceptance one\n"
	path := filepath.Join(dir, "prd.md")
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

// drainEvents collects every event currently buffered on the loop's events
// channel without blocking. Tests use it after triggering state transitions.
func drainEvents(l *Loop) []Event {
	out := []Event{}
	for {
		select {
		case e := <-l.events:
			out = append(out, e)
		default:
			return out
		}
	}
}

func TestHandleReviewVerdict_ApprovedMarksDoneAndArchives(t *testing.T) {
	dir := t.TempDir()
	prdPath := reviewTestPRD(t, dir, "T-001")

	if err := os.MkdirAll(reviewDir(prdPath), 0755); err != nil {
		t.Fatal(err)
	}
	verdictPath := reviewFilePath(prdPath, "T-001")
	if err := os.WriteFile(verdictPath, []byte("# APPROVED\nlgtm"), 0644); err != nil {
		t.Fatal(err)
	}

	l := NewLoop(prdPath, "test", 5, testProvider)
	// Pre-load a non-zero round counter to confirm reset on approval.
	l.reviewRoundsByStory["T-001"] = 1

	verdict := &ReviewVerdict{Approved: true, Body: "lgtm", Path: verdictPath}
	escalated := l.handleReviewVerdict(1, "T-001", verdict)

	if escalated {
		t.Error("approval should not escalate")
	}

	if got := l.reviewRoundsByStory["T-001"]; got != 0 {
		t.Errorf("expected round counter reset to 0 on approval, got %d", got)
	}

	// Story should now be marked done in the PRD.
	p, err := prd.LoadPRD(prdPath)
	if err != nil {
		t.Fatalf("load PRD: %v", err)
	}
	story := findStory(p, "T-001")
	if story == nil {
		t.Fatal("story T-001 not found in PRD")
	}
	if !story.Passes {
		t.Error("expected story to be marked done after approval")
	}

	// Active verdict file should be rotated to the archive name.
	if _, err := os.Stat(verdictPath); !os.IsNotExist(err) {
		t.Error("expected active verdict file to be removed (rotated to archive)")
	}
	if _, err := os.Stat(approvedArchivePath(prdPath, "T-001")); err != nil {
		t.Errorf("expected archived verdict file to exist: %v", err)
	}

	events := drainEvents(l)
	if !containsEvent(events, EventReviewApproved) {
		t.Error("expected EventReviewApproved to be emitted")
	}
}

func TestHandleReviewVerdict_FirstRejectionKeepsInProgress(t *testing.T) {
	dir := t.TempDir()
	prdPath := reviewTestPRD(t, dir, "T-001")

	l := NewLoop(prdPath, "test", 5, testProvider)
	verdict := &ReviewVerdict{Approved: false, Body: "- Missing test.", Path: ""}

	escalated := l.handleReviewVerdict(1, "T-001", verdict)

	if escalated {
		t.Error("first rejection must NOT escalate (cap is 2 rounds)")
	}
	if got := l.reviewRoundsByStory["T-001"]; got != 1 {
		t.Errorf("expected round counter == 1 after first rejection, got %d", got)
	}
	if l.IsPaused() {
		t.Error("loop must NOT pause after first rejection")
	}

	// Story remains in-progress (not done).
	p, _ := prd.LoadPRD(prdPath)
	story := findStory(p, "T-001")
	if story.Passes {
		t.Error("story must NOT be marked done after rejection")
	}

	events := drainEvents(l)
	if !containsEvent(events, EventReviewNeedsRevision) {
		t.Error("expected EventReviewNeedsRevision to be emitted")
	}
}

func TestHandleReviewVerdict_SecondRejectionEscalatesAndPauses(t *testing.T) {
	dir := t.TempDir()
	prdPath := reviewTestPRD(t, dir, "T-001")

	l := NewLoop(prdPath, "test", 5, testProvider)
	verdict := &ReviewVerdict{Approved: false, Body: "- Still broken."}

	// First rejection.
	if escalated := l.handleReviewVerdict(1, "T-001", verdict); escalated {
		t.Fatal("first rejection should not escalate")
	}
	// Drain events between rounds so we can isolate the second batch.
	drainEvents(l)

	// Second rejection — should escalate.
	escalated := l.handleReviewVerdict(2, "T-001", verdict)
	if !escalated {
		t.Error("second rejection must escalate")
	}
	if !l.IsPaused() {
		t.Error("loop must be paused after escalation")
	}
	if got := l.reviewRoundsByStory["T-001"]; got != MaxReviewRounds {
		t.Errorf("expected round counter == %d, got %d", MaxReviewRounds, got)
	}

	// Story still in-progress, not done.
	p, _ := prd.LoadPRD(prdPath)
	story := findStory(p, "T-001")
	if story.Passes {
		t.Error("story must NOT be marked done on escalation")
	}

	events := drainEvents(l)
	if !containsEvent(events, EventReviewEscalated) {
		t.Error("expected EventReviewEscalated to be emitted")
	}
}

func TestHandleReviewVerdict_RoundCounterResetsAfterApproval(t *testing.T) {
	dir := t.TempDir()
	prdPath := reviewTestPRD(t, dir, "T-001")

	if err := os.MkdirAll(reviewDir(prdPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reviewFilePath(prdPath, "T-001"), []byte("# APPROVED\n"), 0644); err != nil {
		t.Fatal(err)
	}

	l := NewLoop(prdPath, "test", 5, testProvider)

	// Round 1: rejected.
	l.handleReviewVerdict(1, "T-001", &ReviewVerdict{Approved: false, Body: "- broken"})
	if l.reviewRoundsByStory["T-001"] != 1 {
		t.Fatalf("expected counter 1 after rejection, got %d", l.reviewRoundsByStory["T-001"])
	}

	// Round 2: implementer fixes it, approved on second attempt.
	l.handleReviewVerdict(2, "T-001", &ReviewVerdict{Approved: true})
	if l.reviewRoundsByStory["T-001"] != 0 {
		t.Errorf("expected counter to reset to 0 after approval, got %d", l.reviewRoundsByStory["T-001"])
	}
}

// findStory is a small test helper.
func findStory(p *prd.PRD, id string) *prd.UserStory {
	for i := range p.UserStories {
		if p.UserStories[i].ID == id {
			return &p.UserStories[i]
		}
	}
	return nil
}

func containsEvent(events []Event, t EventType) bool {
	for _, e := range events {
		if e.Type == t {
			return true
		}
	}
	return false
}
