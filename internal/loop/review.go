package loop

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/minicodemonkey/chief/embed"
	"github.com/minicodemonkey/chief/internal/prd"
	"github.com/minicodemonkey/chief/internal/skills"
)

// MaxReviewRounds is the hard cap on reviewer→implementer revision rounds.
// After this many failed reviews for a single story, the loop pauses and
// emits EventReviewEscalated so a human can intervene. Counted per story.
const MaxReviewRounds = 2

// reviewDoneTag is the literal string the reviewer prompt instructs the
// agent to emit when it has finished writing its verdict file.
const reviewDoneTag = "<chief-review-done/>"

// ReviewVerdict is the parsed result of a single reviewer pass.
type ReviewVerdict struct {
	// Approved is true when the verdict file's first non-empty line begins
	// with "# APPROVED" (case-sensitive on the keyword).
	Approved bool
	// Body is the full body of the verdict file with the header line stripped.
	// Empty when Approved with no notes.
	Body string
	// Path is the absolute path to the verdict file the reviewer wrote.
	Path string
}

// reviewDir returns the directory holding per-story review verdict files.
func reviewDir(prdPath string) string {
	return filepath.Join(filepath.Dir(prdPath), ".review")
}

// reviewFilePath returns the path the reviewer is told to write to.
func reviewFilePath(prdPath, storyID string) string {
	return filepath.Join(reviewDir(prdPath), storyID+".md")
}

// approvedArchivePath returns the rotated path used after a story is approved
// so the user can still see the reviewer's last verdict for history.
func approvedArchivePath(prdPath, storyID string) string {
	return filepath.Join(reviewDir(prdPath), storyID+".approved.md")
}

// loadPendingReviewFindings returns the body of a NEEDS REVISION verdict for
// the given story, or the empty string when no pending findings exist.
// APPROVED files are ignored — they are archive only, not a request for revision.
func loadPendingReviewFindings(prdPath, storyID string) string {
	data, err := os.ReadFile(reviewFilePath(prdPath, storyID))
	if err != nil {
		return ""
	}
	verdict := parseVerdictBytes(data, "")
	if verdict.Approved {
		return ""
	}
	return verdict.Body
}

// parseVerdictBytes parses the contents of a verdict file. The first
// non-empty line is matched against the APPROVED / NEEDS REVISION header.
// Anything that does not start with "# APPROVED" is treated as needing
// revision so an unparseable verdict fails closed (safer default).
func parseVerdictBytes(data []byte, path string) *ReviewVerdict {
	text := string(data)
	lines := strings.Split(text, "\n")

	headerIdx := -1
	for i, line := range lines {
		if strings.TrimSpace(line) != "" {
			headerIdx = i
			break
		}
	}

	if headerIdx == -1 {
		return &ReviewVerdict{Approved: false, Body: "(empty verdict file)", Path: path}
	}

	header := strings.TrimSpace(lines[headerIdx])
	body := strings.TrimSpace(strings.Join(lines[headerIdx+1:], "\n"))

	approved := strings.HasPrefix(header, "# APPROVED")
	if !approved && body == "" {
		body = header
	}
	return &ReviewVerdict{Approved: approved, Body: body, Path: path}
}

// runReview spawns a reviewer subagent for the given story, waits for it to
// finish, reads its verdict file, and returns the parsed verdict. Errors are
// returned for setup failures (could not start the agent, could not read the
// file). A successfully-parsed "needs revision" verdict is NOT an error — it
// is reported via verdict.Approved == false.
func runReview(ctx context.Context, l *Loop, storyID, storyTitle, storyContext string) (*ReviewVerdict, error) {
	if l.provider == nil {
		return nil, fmt.Errorf("review: provider not configured")
	}

	prdPath := l.prdPath
	workDir := l.effectiveWorkDir()

	if err := os.MkdirAll(reviewDir(prdPath), 0755); err != nil {
		return nil, fmt.Errorf("review: create review dir: %w", err)
	}

	// Always start each round from a clean slate so a stale APPROVED or a
	// stale NEEDS REVISION from a prior story doesn't get mistaken for the
	// current run.
	verdictPath := reviewFilePath(prdPath, storyID)
	_ = os.Remove(verdictPath)

	// Discover skills the same way the implementer does so the reviewer
	// judges the diff against the same standards the implementer was held to.
	discoveryRoot := workDir
	if discoveryRoot == "" {
		discoveryRoot = filepath.Dir(prdPath)
	}
	discovered, _ := skills.Discover(discoveryRoot)
	manifest := skills.RenderManifest(discovered, discoveryRoot)

	invariants, _ := prd.LoadInvariants(prdPath)

	prompt := embed.GetReviewPrompt(
		prdPath,
		storyID,
		storyTitle,
		storyContext,
		invariants,
		manifest,
		prd.ProgressPath(prdPath),
		verdictPath,
	)

	cmd := l.provider.LoopCommand(ctx, prompt, workDir)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("review: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("review: stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("review: start agent: %w", err)
	}

	// Watchdog: kill the reviewer process if its stdout goes silent for the
	// same duration as the implementer's watchdog. The reviewer process does
	// NOT touch l.agentCmd, so an interactive Stop() of the loop won't kill
	// the reviewer mid-flight — that's intentional, since stopping the loop
	// while a review is in flight should let the review finish so its
	// verdict is captured.
	l.mu.Lock()
	watchdogTimeout := l.watchdogTimeout
	l.mu.Unlock()

	var lastOutput atomic.Int64
	lastOutput.Store(time.Now().UnixNano())

	watchdogDone := make(chan struct{})
	var watchdogFired atomic.Bool
	if watchdogTimeout > 0 {
		go runReviewWatchdog(cmd, &lastOutput, watchdogTimeout, watchdogDone, &watchdogFired)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		l.scanReviewerOutput(stdout, &lastOutput)
	}()
	go func() {
		defer wg.Done()
		l.logStream(stderr, "[review-stderr] ")
	}()
	wg.Wait()

	close(watchdogDone)

	waitErr := cmd.Wait()
	if waitErr != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if watchdogFired.Load() {
			return nil, fmt.Errorf("review: watchdog timeout (no output for %s)", watchdogTimeout)
		}
		// The reviewer agent exited with an error but may still have written
		// a usable verdict file. Fall through to read it; if it's missing,
		// the error below surfaces.
		l.logLine(fmt.Sprintf("[review] agent exited with error: %v", waitErr))
	}

	data, readErr := os.ReadFile(verdictPath)
	if readErr != nil {
		return nil, fmt.Errorf("review: verdict file not written at %s: %w", verdictPath, readErr)
	}

	return parseVerdictBytes(data, verdictPath), nil
}

// scanReviewerOutput reads the reviewer agent's stdout line-by-line, logs it
// with a [review] prefix so it's distinguishable from implementer output, and
// updates the watchdog clock. The reviewer's events are intentionally NOT
// emitted on the loop's main events channel — only the high-level review
// lifecycle events are.
func (l *Loop) scanReviewerOutput(r io.Reader, lastOutput *atomic.Int64) {
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		lastOutput.Store(time.Now().UnixNano())
		l.logLine("[review] " + line)
	}
}

// runReviewWatchdog kills the reviewer cmd if its stdout has been silent for
// longer than timeout. Stops when watchdogDone is closed.
func runReviewWatchdog(cmd *exec.Cmd, lastOutput *atomic.Int64, timeout time.Duration, done <-chan struct{}, fired *atomic.Bool) {
	checkInterval := timeout / 5
	if checkInterval < 10*time.Millisecond {
		checkInterval = 10 * time.Millisecond
	}
	if checkInterval > 10*time.Second {
		checkInterval = 10 * time.Second
	}
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			last := time.Unix(0, lastOutput.Load())
			if time.Since(last) > timeout {
				fired.Store(true)
				if cmd != nil && cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
				return
			}
		case <-done:
			return
		}
	}
}

// archiveApprovedVerdict moves the active verdict file to its archive name so
// the user can still see the last verdict but the next iteration's
// promptBuilder won't pick it up as a pending revision.
func archiveApprovedVerdict(prdPath, storyID string) error {
	src := reviewFilePath(prdPath, storyID)
	dst := approvedArchivePath(prdPath, storyID)
	if _, err := os.Stat(src); err != nil {
		return nil
	}
	// Remove any prior archive so we always rotate cleanly.
	_ = os.Remove(dst)
	return os.Rename(src, dst)
}
