// Package loop provides the core agent loop that orchestrates Claude Code
// to implement user stories. It includes the main Loop struct for single
// PRD execution, Manager for parallel PRD execution, and Parser for
// processing Claude's stream-json output.
package loop

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/minicodemonkey/chief/embed"
	"github.com/minicodemonkey/chief/internal/prd"
	"github.com/minicodemonkey/chief/internal/skills"
)

// RetryConfig configures automatic retry behavior on Claude crashes.
type RetryConfig struct {
	MaxRetries  int             // Maximum number of retry attempts (default: 3)
	RetryDelays []time.Duration // Delays between retries (default: 0s, 5s, 15s)
	Enabled     bool            // Whether retry is enabled (default: true)
}

// DefaultWatchdogTimeout is the default duration of silence before the watchdog kills a hung process.
const DefaultWatchdogTimeout = 5 * time.Minute

// DefaultRetryConfig returns the default retry configuration.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:  3,
		RetryDelays: []time.Duration{0, 5 * time.Second, 15 * time.Second},
		Enabled:     true,
	}
}

// Loop manages the core agent loop that invokes the configured agent repeatedly until all stories are complete.
type Loop struct {
	prdPath             string
	workDir             string
	prompt              string
	buildPrompt         func() (builtPrompt, error) // optional: rebuild prompt each iteration
	maxIter             int
	iteration           int
	events              chan Event
	provider            Provider
	agentCmd            *exec.Cmd
	logFile             *os.File
	mu                  sync.Mutex
	stopped             bool
	paused              bool
	retryConfig         RetryConfig
	lastOutputTime      time.Time
	watchdogTimeout     time.Duration
	sawStoryDone        bool
	currentStoryID      string
	currentStoryTitle   string
	currentStoryContext string
	reviewRoundsByStory map[string]int
	skipReview          bool // tests only: bypass the reviewer gate
}

// NewLoop creates a new Loop instance.
func NewLoop(prdPath, prompt string, maxIter int, provider Provider) *Loop {
	return &Loop{
		prdPath:             prdPath,
		prompt:              prompt,
		maxIter:             maxIter,
		provider:            provider,
		events:              make(chan Event, 100),
		retryConfig:         DefaultRetryConfig(),
		watchdogTimeout:     DefaultWatchdogTimeout,
		reviewRoundsByStory: make(map[string]int),
	}
}

// NewLoopWithWorkDir creates a new Loop instance with a configurable working directory.
// When workDir is empty, defaults to the project root for backward compatibility.
func NewLoopWithWorkDir(prdPath, workDir string, prompt string, maxIter int, provider Provider) *Loop {
	return &Loop{
		prdPath:             prdPath,
		workDir:             workDir,
		prompt:              prompt,
		maxIter:             maxIter,
		provider:            provider,
		events:              make(chan Event, 100),
		retryConfig:         DefaultRetryConfig(),
		watchdogTimeout:     DefaultWatchdogTimeout,
		reviewRoundsByStory: make(map[string]int),
	}
}

// NewLoopWithEmbeddedPrompt creates a new Loop instance using the embedded agent prompt.
// The prompt is rebuilt on each iteration to inline the current story context.
// workDir is the project root used to discover skills (.claude/skills, .agents/skills);
// pass an empty string to fall back to the PRD directory.
func NewLoopWithEmbeddedPrompt(prdPath, workDir string, maxIter int, provider Provider) *Loop {
	l := NewLoopWithWorkDir(prdPath, workDir, "", maxIter, provider)
	l.buildPrompt = promptBuilderForPRD(prdPath, workDir)
	return l
}

// builtPrompt is what promptBuilderForPRD returns: the rendered prompt plus
// the story metadata the loop needs to drive the reviewer gate.
type builtPrompt struct {
	prompt       string
	storyID      string
	storyTitle   string
	storyContext string
}

// promptBuilderForPRD returns a function that loads the PRD and builds a prompt
// with the next story inlined. This is called before each iteration so that
// newly completed stories are skipped.
//
// skillDir is the directory used to discover project skills. When empty,
// skill discovery falls back to the PRD's parent directory, which usually
// will not contain a .claude/skills tree — that's intentional, since the
// embedded constructor without an explicit workDir is only used in tests.
//
// The builder also injects two cross-cutting blocks into every prompt:
//
//   - Global Invariants — read from the `## Global Invariants` section of
//     prd.md so project-wide rules are present in every iteration.
//   - Review findings — if the previous attempt at this story was rejected
//     by the reviewer gate, the findings file at
//     `<prdDir>/.review/<storyID>.md` is loaded and prepended.
func promptBuilderForPRD(prdPath, skillDir string) func() (builtPrompt, error) {
	return func() (builtPrompt, error) {
		p, err := prd.LoadPRD(prdPath)
		if err != nil {
			return builtPrompt{}, fmt.Errorf("failed to load PRD for prompt: %w", err)
		}

		story := p.NextStory()
		if story == nil {
			return builtPrompt{}, fmt.Errorf("all stories are complete")
		}

		// Mark the story as in-progress in the markdown file
		_ = prd.SetStoryStatus(prdPath, story.ID, "in-progress")

		storyCtx := p.NextStoryContext()

		discoveryRoot := skillDir
		if discoveryRoot == "" {
			discoveryRoot = filepath.Dir(prdPath)
		}
		discovered, _ := skills.Discover(discoveryRoot)
		manifest := skills.RenderManifest(discovered, discoveryRoot)

		// Load project-wide invariants. Errors here are non-fatal so PRDs
		// without an invariants section keep working.
		invariants, _ := prd.LoadInvariants(prdPath)

		// Load any pending review findings for this story. The reviewer
		// writes a findings file when it rejects an attempt; the implementer
		// must address those findings before the next <chief-done/>.
		findings := loadPendingReviewFindings(prdPath, story.ID)

		prompt := embed.GetPrompt(
			prd.ProgressPath(prdPath),
			*storyCtx,
			story.ID,
			story.Title,
			manifest,
			invariants,
			findings,
		)
		return builtPrompt{
			prompt:       prompt,
			storyID:      story.ID,
			storyTitle:   story.Title,
			storyContext: *storyCtx,
		}, nil
	}
}

// Events returns the channel for receiving events from the loop.
func (l *Loop) Events() <-chan Event {
	return l.events
}

// Iteration returns the current iteration number.
func (l *Loop) Iteration() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.iteration
}

// Run executes the agent loop until completion or max iterations.
func (l *Loop) Run(ctx context.Context) error {
	if l.provider == nil {
		return fmt.Errorf("loop provider is not configured")
	}

	// Open log file in PRD directory
	prdDir := filepath.Dir(l.prdPath)
	logPath := filepath.Join(prdDir, l.provider.LogFileName())
	var err error
	l.logFile, err = os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}
	defer l.logFile.Close()
	defer close(l.events)

	for {
		l.mu.Lock()
		if l.stopped {
			l.mu.Unlock()
			return nil
		}
		if l.paused {
			l.mu.Unlock()
			return nil
		}
		l.iteration++
		currentIter := l.iteration
		l.mu.Unlock()

		// Check if max iterations reached
		if currentIter > l.maxIter {
			l.events <- Event{
				Type:      EventMaxIterationsReached,
				Iteration: currentIter - 1,
			}
			return nil
		}

		// Rebuild prompt if builder is set (inlines the current story each iteration)
		if l.buildPrompt != nil {
			built, err := l.buildPrompt()
			if err != nil {
				l.events <- Event{
					Type:      EventComplete,
					Iteration: currentIter,
				}
				return nil
			}
			l.mu.Lock()
			l.prompt = built.prompt
			l.currentStoryID = built.storyID
			l.currentStoryTitle = built.storyTitle
			l.currentStoryContext = built.storyContext
			l.sawStoryDone = false
			l.mu.Unlock()
		}

		// Send iteration start event with current story ID
		l.mu.Lock()
		iterStoryID := l.currentStoryID
		l.mu.Unlock()
		l.events <- Event{
			Type:      EventIterationStart,
			Iteration: currentIter,
			StoryID:   iterStoryID,
		}

		// Run a single iteration with retry logic
		if err := l.runIterationWithRetry(ctx); err != nil {
			l.events <- Event{
				Type: EventError,
				Err:  err,
			}
			return err
		}

		// Check context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// If the agent emitted <chief-done/>, run the reviewer gate before
		// marking the story as done. The reviewer is an independent subagent
		// that reads the diff against the PRD, the global invariants, the
		// loaded skills, and the acceptance criteria. Approval marks the
		// story done; rejection keeps it in-progress and the next iteration
		// will pick up the findings file. After MaxReviewRounds rejections
		// the loop pauses and surfaces the escalation to the user.
		l.mu.Lock()
		saw := l.sawStoryDone
		storyID := l.currentStoryID
		storyTitle := l.currentStoryTitle
		storyContext := l.currentStoryContext
		skipReview := l.skipReview
		l.sawStoryDone = false
		l.mu.Unlock()
		if saw && storyID != "" {
			if skipReview {
				_ = prd.SetStoryStatus(l.prdPath, storyID, "done")
			} else {
				if escalated, err := l.runReviewGate(ctx, currentIter, storyID, storyTitle, storyContext); err != nil {
					l.events <- Event{Type: EventReviewError, Iteration: currentIter, StoryID: storyID, Err: err}
					l.Pause()
					return nil
				} else if escalated {
					return nil
				}
			}
		}
		// buildPrompt on the next iteration will return error if all stories are complete,
		// which causes EventComplete to be emitted above.

		// Check pause flag after iteration (loop stops after current iteration completes)
		l.mu.Lock()
		if l.paused {
			l.mu.Unlock()
			return nil
		}
		l.mu.Unlock()
	}
}

// runReviewGate runs the reviewer subagent for a freshly-completed story and
// then dispatches the verdict to the state machine in handleReviewVerdict.
// Returns (escalated, error). On error, the caller pauses the loop.
func (l *Loop) runReviewGate(ctx context.Context, iter int, storyID, storyTitle, storyContext string) (bool, error) {
	l.events <- Event{Type: EventReviewStart, Iteration: iter, StoryID: storyID}

	verdict, err := runReview(ctx, l, storyID, storyTitle, storyContext)
	if err != nil {
		return false, err
	}

	return l.handleReviewVerdict(iter, storyID, verdict), nil
}

// handleReviewVerdict applies the reviewer's verdict to loop state. It is
// split out from runReviewGate so tests can drive the state machine without
// having to spawn a real reviewer process.
//
//   - approved: marks the story done in prd.md, rotates the verdict to its
//     archive name, resets the round counter, emits EventReviewApproved.
//   - rejected (round < MaxReviewRounds): leaves the story in-progress, leaves
//     the findings file in place so the next iteration's promptBuilder picks
//     it up, increments the counter, emits EventReviewNeedsRevision.
//   - rejected (round >= MaxReviewRounds): leaves everything in place, emits
//     EventReviewEscalated, pauses the loop, and returns true so the caller
//     stops the iteration loop.
func (l *Loop) handleReviewVerdict(iter int, storyID string, verdict *ReviewVerdict) bool {
	if verdict.Approved {
		_ = prd.SetStoryStatus(l.prdPath, storyID, "done")
		_ = archiveApprovedVerdict(l.prdPath, storyID)
		l.mu.Lock()
		l.reviewRoundsByStory[storyID] = 0
		l.mu.Unlock()
		l.events <- Event{
			Type:      EventReviewApproved,
			Iteration: iter,
			StoryID:   storyID,
			Text:      verdict.Body,
		}
		return false
	}

	l.mu.Lock()
	l.reviewRoundsByStory[storyID]++
	round := l.reviewRoundsByStory[storyID]
	l.mu.Unlock()

	if round >= MaxReviewRounds {
		l.events <- Event{
			Type:      EventReviewEscalated,
			Iteration: iter,
			StoryID:   storyID,
			Text:      verdict.Body,
		}
		l.Pause()
		return true
	}

	l.events <- Event{
		Type:      EventReviewNeedsRevision,
		Iteration: iter,
		StoryID:   storyID,
		Text:      verdict.Body,
	}
	return false
}

// runIterationWithRetry wraps runIteration with retry logic for crash recovery.
func (l *Loop) runIterationWithRetry(ctx context.Context) error {
	l.mu.Lock()
	config := l.retryConfig
	l.mu.Unlock()

	var lastErr error
	for attempt := 0; attempt <= config.MaxRetries; attempt++ {
		// Check if retry is enabled (except for first attempt)
		if attempt > 0 {
			if !config.Enabled {
				return lastErr
			}

			// Get delay for this retry
			delayIdx := attempt - 1
			if delayIdx >= len(config.RetryDelays) {
				delayIdx = len(config.RetryDelays) - 1
			}
			delay := config.RetryDelays[delayIdx]

			// Emit retry event
			l.mu.Lock()
			iter := l.iteration
			l.mu.Unlock()
			l.events <- Event{
				Type:       EventRetrying,
				Iteration:  iter,
				RetryCount: attempt,
				RetryMax:   config.MaxRetries,
				Text:       fmt.Sprintf("%s crashed, retrying (%d/%d)...", l.provider.Name(), attempt, config.MaxRetries),
			}

			// Wait before retry
			if delay > 0 {
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}

		// Check if stopped during delay
		l.mu.Lock()
		if l.stopped {
			l.mu.Unlock()
			return nil
		}
		l.mu.Unlock()

		// Run the iteration
		err := l.runIteration(ctx)
		if err == nil {
			return nil // Success
		}

		// Check if this is a context cancellation (don't retry)
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Check if stopped intentionally
		l.mu.Lock()
		stopped := l.stopped
		l.mu.Unlock()
		if stopped {
			return nil
		}

		lastErr = err
	}

	return fmt.Errorf("max retries (%d) exceeded: %w", config.MaxRetries, lastErr)
}

// runIteration spawns the agent and processes its output.
func (l *Loop) runIteration(ctx context.Context) error {
	workDir := l.effectiveWorkDir()
	cmd := l.provider.LoopCommand(ctx, l.prompt, workDir)
	l.mu.Lock()
	l.agentCmd = cmd
	// Initialize watchdog state
	l.lastOutputTime = time.Now()
	watchdogTimeout := l.watchdogTimeout
	l.mu.Unlock()

	// Create pipes for stdout and stderr
	stdout, err := l.agentCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := l.agentCmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Start the command
	if err := l.agentCmd.Start(); err != nil {
		return fmt.Errorf("failed to start %s: %w", l.provider.Name(), err)
	}

	// Start watchdog goroutine to detect hung processes
	watchdogDone := make(chan struct{})
	var watchdogFired atomic.Bool
	if watchdogTimeout > 0 {
		go l.runWatchdog(watchdogTimeout, watchdogDone, &watchdogFired)
	}

	// Process stdout in a separate goroutine
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		l.processOutput(stdout)
	}()

	// Log stderr to the log file
	go func() {
		defer wg.Done()
		l.logStream(stderr, "[stderr] ")
	}()

	// Wait for output processing to complete
	wg.Wait()

	// Stop watchdog
	close(watchdogDone)

	// Wait for the command to finish
	if err := l.agentCmd.Wait(); err != nil {
		// If the context was cancelled, don't treat it as an error
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Check if we were stopped intentionally
		l.mu.Lock()
		stopped := l.stopped
		l.mu.Unlock()
		if stopped {
			return nil
		}
		// Check if the watchdog killed the process
		if watchdogFired.Load() {
			return fmt.Errorf("watchdog timeout: no output for %s", watchdogTimeout)
		}
		return fmt.Errorf("%s exited with error: %w", l.provider.Name(), err)
	}

	l.mu.Lock()
	l.agentCmd = nil
	l.mu.Unlock()

	return nil
}

// runWatchdog monitors lastOutputTime and kills the process if no output is received
// within the timeout duration. It stops when watchdogDone is closed.
func (l *Loop) runWatchdog(timeout time.Duration, done <-chan struct{}, fired *atomic.Bool) {
	// Check interval scales with timeout: 1/5 of timeout, clamped to [10ms, 10s]
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
			l.mu.Lock()
			lastOutput := l.lastOutputTime
			stopped := l.stopped
			l.mu.Unlock()

			if stopped {
				return
			}

			if time.Since(lastOutput) > timeout {
				fired.Store(true)

				// Emit watchdog timeout event
				l.mu.Lock()
				iter := l.iteration
				l.mu.Unlock()
				l.events <- Event{
					Type:      EventWatchdogTimeout,
					Iteration: iter,
					Text:      fmt.Sprintf("No output for %s, killing hung process", timeout),
				}

				// Kill the process
				l.mu.Lock()
				if l.agentCmd != nil && l.agentCmd.Process != nil {
					l.agentCmd.Process.Kill()
				}
				l.mu.Unlock()
				return
			}
		case <-done:
			return
		}
	}
}

// processOutput reads stdout line by line, logs it, and parses events.
func (l *Loop) processOutput(r io.Reader) {
	scanner := bufio.NewScanner(r)
	// Increase buffer size for long lines (Claude can output large JSON)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		// Update last output time for watchdog
		l.mu.Lock()
		l.lastOutputTime = time.Now()
		l.mu.Unlock()

		// Log raw output
		l.logLine(line)

		// Parse the line and emit event if valid
		if event := l.provider.ParseLine(line); event != nil {
			l.mu.Lock()
			event.Iteration = l.iteration
			if event.Type == EventStoryDone {
				l.sawStoryDone = true
			}
			l.mu.Unlock()
			l.events <- *event
		}
	}
}

// logStream logs a stream with a prefix.
func (l *Loop) logStream(r io.Reader, prefix string) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		l.logLine(prefix + scanner.Text())
	}
}

// logLine writes a line to the log file.
func (l *Loop) logLine(line string) {
	if l.logFile != nil {
		l.logFile.WriteString(line + "\n")
	}
}

// Stop terminates the current agent process and stops the loop.
func (l *Loop) Stop() {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.stopped = true

	if l.agentCmd != nil && l.agentCmd.Process != nil {
		l.agentCmd.Process.Kill()
	}
}

// Pause sets the pause flag. The loop will stop after the current iteration completes.
func (l *Loop) Pause() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.paused = true
}

// Resume clears the pause flag.
func (l *Loop) Resume() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.paused = false
}

// IsPaused returns whether the loop is paused.
func (l *Loop) IsPaused() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.paused
}

// IsStopped returns whether the loop is stopped.
func (l *Loop) IsStopped() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.stopped
}

// effectiveWorkDir returns the working directory to use for the agent.
// If workDir is set, it is used directly. Otherwise, defaults to the PRD directory.
func (l *Loop) effectiveWorkDir() string {
	if l.workDir != "" {
		return l.workDir
	}
	return filepath.Dir(l.prdPath)
}

// IsRunning returns whether an agent process is currently running.
func (l *Loop) IsRunning() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.agentCmd != nil && l.agentCmd.Process != nil
}

// SetMaxIterations updates the maximum iterations limit.
func (l *Loop) SetMaxIterations(maxIter int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.maxIter = maxIter
}

// MaxIterations returns the current max iterations limit.
func (l *Loop) MaxIterations() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.maxIter
}

// SetRetryConfig updates the retry configuration.
func (l *Loop) SetRetryConfig(config RetryConfig) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.retryConfig = config
}

// DisableRetry disables automatic retry on crash.
func (l *Loop) DisableRetry() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.retryConfig.Enabled = false
}

// SetWatchdogTimeout sets the watchdog timeout duration.
// Setting timeout to 0 disables the watchdog.
func (l *Loop) SetWatchdogTimeout(timeout time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.watchdogTimeout = timeout
}

// WatchdogTimeout returns the current watchdog timeout duration.
func (l *Loop) WatchdogTimeout() time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.watchdogTimeout
}
