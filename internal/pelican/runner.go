package pelican

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/looplj/axonhub/internal/log"
)

// CompletionResult is the outcome of one gateway call.
type CompletionResult struct {
	// Reply is the untouched model output, kept for the conversation view.
	Reply string
	// FinishReason mirrors the provider value, e.g. "stop" or "length".
	FinishReason string
	// Usage holds the token counts reported by the provider, when available.
	Usage map[string]int
}

// ChatCompleter performs one chat completion. The gateway adapter implements it; tests use a fake.
type ChatCompleter interface {
	Complete(ctx context.Context, target Target, prompt string) (CompletionResult, error)
}

// Runner executes one round: every configured target gets the same prompt.
type Runner struct {
	store       *Store
	completer   ChatCompleter
	timeout     time.Duration
	concurrency int

	now func() time.Time

	mu           sync.Mutex
	activeRounds int
}

// RunnerOption customises a Runner.
type RunnerOption func(*Runner)

// WithTimeout bounds a single model call.
func WithTimeout(timeout time.Duration) RunnerOption {
	return func(r *Runner) { r.timeout = timeout }
}

// WithConcurrency caps how many models are called at the same time. It is unset by default:
// a round dispatches every model at once.
func WithConcurrency(concurrency int) RunnerOption {
	return func(r *Runner) { r.concurrency = concurrency }
}

// WithClock injects the clock used for timestamps, so tests are deterministic.
func WithClock(now func() time.Time) RunnerOption {
	return func(r *Runner) { r.now = now }
}

// NewRunner wires the runner with its defaults. It deliberately has no variadic parameter:
// dependency injection resolves parameters by type, and a variadic one looks like an
// unresolved slice dependency. Tests use newRunner to pass options.
func NewRunner(store *Store, gateway *Gateway) *Runner {
	return newRunner(store, gateway)
}

// newRunner builds a runner. The timeout defaults to the same per-request budget the gateway
// uses for LLM calls, because reasoning models routinely need minutes.
func newRunner(store *Store, completer ChatCompleter, options ...RunnerOption) *Runner {
	runner := &Runner{
		store:       store,
		completer:   completer,
		timeout:     10 * time.Minute,
		concurrency: 0, // zero means "no cap": every model of the round starts together
		now:         time.Now,
	}
	for _, option := range options {
		option(runner)
	}
	if runner.concurrency < 0 {
		runner.concurrency = 0
	}
	return runner
}

// newID builds a stable, unique identifier for one attempt. The timestamp prefix keeps
// history readable; the random suffix keeps concurrent runs from colliding.
func newID(now time.Time, index int) string {
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return fmt.Sprintf("%d-%d", now.UnixMilli(), index)
	}
	return fmt.Sprintf("%d-%d-%s", now.UnixMilli(), index, hex.EncodeToString(suffix[:]))
}

// IsRunning reports whether any round is currently in flight.
func (r *Runner) IsRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.activeRounds > 0
}

// Run executes one full round and returns the recorded results.
//
// Rounds are independent: starting one while another is still running is allowed, and each
// round gets its own worker pool. Every attempt is written to the store as soon as it
// finishes, so the UI can follow progress.
func (r *Runner) Run(ctx context.Context) ([]Result, error) {
	config, _, err := r.store.Load()
	if err != nil {
		return nil, err
	}
	if len(config.Targets) == 0 {
		return nil, errors.New("no model is selected for the pelican test")
	}

	r.mu.Lock()
	r.activeRounds++
	r.mu.Unlock()

	defer func() {
		r.mu.Lock()
		r.activeRounds--
		r.mu.Unlock()
	}()

	results := make([]Result, len(config.Targets))
	for index, target := range config.Targets {
		results[index] = Result{
			ID:         newID(r.now(), index),
			Model:      target.Model,
			Effort:     target.Effort,
			Status:     StatusRunning,
			CreatedAt:  r.now().UTC().Format(time.RFC3339),
			PromptEcho: config.Prompt,
		}
	}
	if err := r.store.SaveResults(results); err != nil {
		return nil, err
	}

	queue := make(chan int)
	var waitGroup sync.WaitGroup
	workers := len(results)
	if r.concurrency > 0 {
		workers = min(r.concurrency, workers)
	}
	for worker := 0; worker < workers; worker++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			// A panic inside one worker must not take the whole round (or the process) down.
			defer func() {
				if recovered := recover(); recovered != nil {
					log.Error(ctx, "pelican worker panicked", log.Any("panic", recovered))
				}
			}()
			for index := range queue {
				r.attempt(ctx, config, index, &results[index])
			}
		}()
	}
	for index := range results {
		queue <- index
	}
	close(queue)
	waitGroup.Wait()

	return results, nil
}

// attempt calls one model and records the outcome for a single result.
func (r *Runner) attempt(ctx context.Context, config Config, index int, result *Result) {
	callCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	started := r.now()
	completion, err := r.completer.Complete(callCtx, Target{Model: result.Model, Effort: result.Effort}, config.Prompt)
	duration := r.now().Sub(started).Seconds()

	result.DurationSeconds = duration
	conversation := Conversation{
		Prompt:          config.Prompt,
		Effort:          result.Effort,
		Model:           result.Model,
		Reply:           completion.Reply,
		FinishReason:    completion.FinishReason,
		Usage:           completion.Usage,
		DurationSeconds: duration,
		CreatedAt:       result.CreatedAt,
	}

	if err != nil {
		result.Status = StatusFailed
		result.Error = describeFailure(callCtx, err, r.timeout)
		conversation.Error = result.Error
	} else if content, format, ok := ExtractDocument(completion.Reply); ok {
		result.Status = StatusSucceeded
		result.Format = format
		if writeErr := r.store.WriteArtifact(result.ID, format, content); writeErr != nil {
			result.Status = StatusFailed
			result.Error = "failed to store the generated document"
			conversation.Error = result.Error
			log.Error(ctx, "failed to store pelican artifact", log.Cause(writeErr), log.String("result_id", result.ID))
		}
	} else {
		result.Status = StatusFailed
		result.Error = "the reply did not contain a complete HTML or SVG document"
		conversation.Error = result.Error
	}

	if err := r.store.WriteConversation(result.ID, conversation); err != nil {
		log.Warn(ctx, "failed to store pelican conversation", log.Cause(err), log.String("result_id", result.ID))
	}
	if err := r.saveOne(*result); err != nil {
		log.Error(ctx, "failed to save pelican result", log.Cause(err), log.String("result_id", result.ID))
	}
}

// saveOne merges a single finished result into the persisted history.
func (r *Runner) saveOne(updated Result) error {
	return r.store.Update(func(_ *Config, results *[]Result) error {
		for index, existing := range *results {
			if existing.ID == updated.ID {
				(*results)[index] = updated
				return nil
			}
		}
		return nil
	})
}

// describeFailure turns a call error into a short, user-visible reason.
func describeFailure(ctx context.Context, err error, timeout time.Duration) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Sprintf("timed out after %s", timeout)
	}
	if errors.Is(err, context.Canceled) {
		return "the request was canceled"
	}
	message := strings.TrimSpace(err.Error())
	if message == "" {
		return "the model call failed"
	}
	return truncate(message, 300)
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

// credentialPattern matches strings that look like secrets and must never be stored.
var credentialPattern = regexp.MustCompile(`sk-[A-Za-z0-9_-]{8,}|Bearer\s+[A-Za-z0-9._-]{8,}|[A-Za-z0-9_-]{32,}`)

// RedactCredentials removes credential-shaped substrings from text that is persisted or shown.
func RedactCredentials(text string) string {
	return credentialPattern.ReplaceAllString(text, "[redacted]")
}

var (
	doctypePattern = regexp.MustCompile(`(?is)<!doctype html[^>]*>\s*<html[\s>].*?</html>`)
	htmlPattern    = regexp.MustCompile(`(?is)<html[\s>].*?</html>`)
	svgPattern     = regexp.MustCompile(`(?is)<svg[\s>].*?</svg>`)
	fencePattern   = regexp.MustCompile("(?is)```(?:html|svg|xml)?[ \t]*\r?\n(.*?)```")
)

// ExtractDocument pulls the generated document out of a model reply.
//
// Chatty models wrap the document in prose or in a Markdown fence; the document itself is
// returned verbatim, because rewriting model output would defeat the comparison.
func ExtractDocument(reply string) (string, string, bool) {
	trimmed := strings.TrimSpace(reply)
	candidates := make([]string, 0, 2)
	if match := fencePattern.FindStringSubmatch(trimmed); match != nil {
		candidates = append(candidates, match[1])
	}
	candidates = append(candidates, trimmed)

	for _, candidate := range candidates {
		document := doctypePattern.FindString(candidate)
		if document == "" {
			document = htmlPattern.FindString(candidate)
		}
		if document != "" {
			// The task is a drawing: an HTML page without an SVG is not a comparable result.
			if strings.Contains(strings.ToLower(document), "<svg") {
				return strings.TrimSpace(document), "html", true
			}
			continue
		}
		if match := svgPattern.FindString(candidate); match != "" {
			return strings.TrimSpace(match), "svg", true
		}
	}
	return "", "", false
}
