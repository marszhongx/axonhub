// Package pelican implements the "Pelican test": it asks every configured model to draw a
// pelican riding a bicycle and keeps the results side by side for comparison.
//
// The package is intentionally self-contained: it stores its own state as files, so enabling
// it does not add database tables, migrations or generated GraphQL code to AxonHub.
package pelican

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DefaultPrompt is the fixed task sent to every model.
const DefaultPrompt = "请生成一个完整、可独立运行的 HTML 文件，绘制一只骑自行车的鹈鹕。"

// MaxPromptLength bounds the editable task text.
const MaxPromptLength = 4000

// Effort mirrors the reasoning effort levels accepted by the gateway.
type Effort string

// Supported reasoning levels. Empty means "let the provider decide".
const (
	EffortAuto    Effort = ""
	EffortNone    Effort = "none"
	EffortMinimal Effort = "minimal"
	EffortLow     Effort = "low"
	EffortMedium  Effort = "medium"
	EffortHigh    Effort = "high"
	EffortXHigh   Effort = "xhigh"
	EffortMax     Effort = "max"
)

var supportedEfforts = []Effort{EffortNone, EffortMinimal, EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax}

// SupportedEfforts lists the reasoning levels in display order.
func SupportedEfforts() []Effort { return append([]Effort(nil), supportedEfforts...) }

// ValidEffort reports whether the level is one of the supported values (empty is allowed).
func ValidEffort(effort Effort) bool {
	if effort == EffortAuto {
		return true
	}
	for _, candidate := range supportedEfforts {
		if effort == candidate {
			return true
		}
	}
	return false
}

// Target is one model entry: which model to call and at which reasoning level.
type Target struct {
	Model  string `json:"model"`
	Effort Effort `json:"effort"`
}

// Result status values.
const (
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
)

// Result is the outcome of one attempt at one target.
type Result struct {
	ID              string  `json:"id"`
	Model           string  `json:"model"`
	Effort          Effort  `json:"effort"`
	Status          string  `json:"status"`
	CreatedAt       string  `json:"createdAt"`
	DurationSeconds float64 `json:"durationSeconds"`
	Format          string  `json:"format,omitempty"`
	Error           string  `json:"error,omitempty"`
	// PromptEcho records the prompt used for this attempt so history stays explainable.
	PromptEcho string `json:"promptEcho,omitempty"`
}

// Schedule holds the hourly toggle and the next pending slot.
type Schedule struct {
	Enabled   bool   `json:"enabled"`
	NextRunAt string `json:"nextRunAt,omitempty"`
}

// Config is the module configuration.
type Config struct {
	Prompt   string   `json:"prompt"`
	Schedule Schedule `json:"schedule"`
	Targets  []Target `json:"targets"`
	// ProjectID records which project the configuration belongs to, so the hourly task can
	// run without an HTTP request context. It is filled in by the API, not by the UI.
	ProjectID int `json:"projectId,omitempty"`
}

type state struct {
	Version int      `json:"version"`
	Config  Config   `json:"config"`
	Results []Result `json:"results"`
}

const stateVersion = 1

// ErrNotFound is returned when a result, artifact or conversation does not exist.
var ErrNotFound = errors.New("not found")

// Store persists the module state as plain files: state.json plus one file per artifact.
type Store struct {
	dir string

	mu sync.Mutex
}

// NewStoreAt returns a store rooted at dir. The directory is created on first write.
func NewStoreAt(dir string) *Store { return &Store{dir: filepath.Clean(dir)} }

// Dir is the root directory of the store.
func (s *Store) Dir() string { return s.dir }

func (s *Store) statePath() string       { return filepath.Join(s.dir, "state.json") }
func (s *Store) artifactDir() string     { return filepath.Join(s.dir, "artifacts") }
func (s *Store) conversationDir() string { return filepath.Join(s.dir, "conversations") }

func defaultConfig() Config {
	return Config{Prompt: DefaultPrompt, Schedule: Schedule{}, Targets: []Target{}}
}

// safeName guards against path traversal in identifiers that come from HTTP requests.
func safeName(id string) error {
	if id == "" || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return fmt.Errorf("invalid identifier %q", id)
	}
	return nil
}

// Config returns only the configuration.
func (s *Store) Config() (Config, error) {
	config, _, err := s.Load()
	return config, err
}

// Load reads the current state, falling back to defaults when the file does not exist yet.
func (s *Store) Load() (Config, []Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *Store) loadLocked() (Config, []Result, error) {
	raw, err := os.ReadFile(s.statePath())
	if errors.Is(err, os.ErrNotExist) {
		return defaultConfig(), nil, nil
	}
	if err != nil {
		return Config{}, nil, fmt.Errorf("read pelican state: %w", err)
	}

	var decoded state
	if err := json.Unmarshal(raw, &decoded); err != nil {
		// A corrupt file must surface instead of silently resetting the configuration.
		return Config{}, nil, fmt.Errorf("pelican state is not valid JSON: %w", err)
	}
	if decoded.Version != stateVersion {
		return Config{}, nil, fmt.Errorf("unsupported pelican state version %d", decoded.Version)
	}

	config := decoded.Config
	if strings.TrimSpace(config.Prompt) == "" {
		config.Prompt = DefaultPrompt
	}
	if config.Targets == nil {
		config.Targets = []Target{}
	}
	for _, target := range config.Targets {
		if !ValidEffort(target.Effort) {
			return Config{}, nil, fmt.Errorf("unsupported reasoning effort %q for model %q", target.Effort, target.Model)
		}
	}
	return config, decoded.Results, nil
}

// SaveConfig replaces the configuration. Targets are validated and de-duplicated by model+effort.
func (s *Store) SaveConfig(config Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	prompt := strings.TrimSpace(config.Prompt)
	if prompt == "" {
		return errors.New("prompt must not be empty")
	}
	if len([]rune(prompt)) > MaxPromptLength {
		return fmt.Errorf("prompt must be at most %d characters", MaxPromptLength)
	}

	seen := make(map[Target]struct{}, len(config.Targets))
	targets := make([]Target, 0, len(config.Targets))
	for _, target := range config.Targets {
		model := strings.TrimSpace(target.Model)
		if model == "" {
			return errors.New("model must not be empty")
		}
		if !ValidEffort(target.Effort) {
			return fmt.Errorf("unsupported reasoning effort %q", target.Effort)
		}
		normalized := Target{Model: model, Effort: target.Effort}
		if _, duplicate := seen[normalized]; duplicate {
			continue
		}
		seen[normalized] = struct{}{}
		targets = append(targets, normalized)
	}

	_, existing, err := s.loadLocked()
	if err != nil {
		return err
	}
	config.Prompt = prompt
	config.Targets = targets
	return s.writeStateLocked(state{Version: stateVersion, Config: config, Results: existing})
}

// NextHour returns the top of the following hour for the given moment.
func NextHour(now time.Time) time.Time {
	return now.Truncate(time.Hour).Add(time.Hour)
}

// SaveResults persists the whole result history.
func (s *Store) SaveResults(results []Result) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	config, _, err := s.loadLocked()
	if err != nil {
		return err
	}
	return s.writeStateLocked(state{Version: stateVersion, Config: config, Results: results})
}

// Update mutates the configuration and results under a single lock, then persists them.
func (s *Store) Update(change func(*Config, *[]Result) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	config, results, err := s.loadLocked()
	if err != nil {
		return err
	}
	if err := change(&config, &results); err != nil {
		return err
	}
	return s.writeStateLocked(state{Version: stateVersion, Config: config, Results: results})
}

func (s *Store) writeStateLocked(next state) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("create pelican data dir: %w", err)
	}
	raw, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return fmt.Errorf("encode pelican state: %w", err)
	}

	// Write to a temporary file and rename, so a crash never leaves a half-written state.
	temporary, err := os.CreateTemp(s.dir, "state-*.json.tmp")
	if err != nil {
		return fmt.Errorf("create pelican temp file: %w", err)
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()

	if _, err := temporary.Write(raw); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write pelican state: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close pelican state: %w", err)
	}
	if err := os.Rename(temporaryName, s.statePath()); err != nil {
		return fmt.Errorf("replace pelican state: %w", err)
	}
	return nil
}

// WriteArtifact stores the generated document for a result.
func (s *Store) WriteArtifact(id, format, content string) error {
	if err := safeName(id); err != nil {
		return err
	}
	if format != "html" && format != "svg" {
		return fmt.Errorf("unsupported artifact format %q", format)
	}
	if err := os.MkdirAll(s.artifactDir(), 0o700); err != nil {
		return fmt.Errorf("create artifact dir: %w", err)
	}
	return os.WriteFile(filepath.Join(s.artifactDir(), id+"."+format), []byte(content), 0o600)
}

// ReadArtifact returns the generated document for a result.
func (s *Store) ReadArtifact(id, format string) (string, error) {
	if err := safeName(id); err != nil {
		return "", err
	}
	raw, err := os.ReadFile(filepath.Join(s.artifactDir(), id+"."+format))
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read artifact: %w", err)
	}
	return string(raw), nil
}

// Conversation is the full exchange of one attempt, kept beside the artifact.
type Conversation struct {
	Prompt          string         `json:"prompt"`
	Effort          Effort         `json:"effort"`
	Model           string         `json:"model"`
	Reply           string         `json:"reply"`
	Error           string         `json:"error,omitempty"`
	FinishReason    string         `json:"finishReason,omitempty"`
	Usage           map[string]int `json:"usage,omitempty"`
	DurationSeconds float64        `json:"durationSeconds"`
	CreatedAt       string         `json:"createdAt"`
}

// WriteConversation stores the exchange of one attempt.
func (s *Store) WriteConversation(id string, conversation Conversation) error {
	if err := safeName(id); err != nil {
		return err
	}
	if err := os.MkdirAll(s.conversationDir(), 0o700); err != nil {
		return fmt.Errorf("create conversation dir: %w", err)
	}
	raw, err := json.MarshalIndent(conversation, "", "  ")
	if err != nil {
		return fmt.Errorf("encode conversation: %w", err)
	}
	return os.WriteFile(filepath.Join(s.conversationDir(), id+".json"), raw, 0o600)
}

// ReadConversation returns the stored exchange of one attempt.
func (s *Store) ReadConversation(id string) (Conversation, error) {
	if err := safeName(id); err != nil {
		return Conversation{}, err
	}
	raw, err := os.ReadFile(filepath.Join(s.conversationDir(), id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return Conversation{}, ErrNotFound
	}
	if err != nil {
		return Conversation{}, fmt.Errorf("read conversation: %w", err)
	}
	var conversation Conversation
	if err := json.Unmarshal(raw, &conversation); err != nil {
		return Conversation{}, fmt.Errorf("conversation is not valid JSON: %w", err)
	}
	return conversation, nil
}
