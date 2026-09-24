package pelican

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStore_LoadWithoutFileReturnsDefaults(t *testing.T) {
	store := NewStoreAt(t.TempDir())

	config, results, err := store.Load()

	require.NoError(t, err)
	require.Equal(t, DefaultPrompt, config.Prompt)
	require.False(t, config.Schedule.Enabled)
	require.Empty(t, config.Schedule.NextRunAt)
	require.Empty(t, config.Targets)
	require.Empty(t, results)
}

func TestStore_SaveAndReloadRoundTripsConfig(t *testing.T) {
	dir := t.TempDir()
	store := NewStoreAt(dir)
	nextRun := NextHour(time.Date(2026, 9, 23, 6, 20, 0, 0, time.UTC)).Format(time.RFC3339)

	require.NoError(t, store.SaveConfig(Config{
		Prompt:   "画一只戴墨镜的鹈鹕。",
		Schedule: Schedule{Enabled: true, NextRunAt: nextRun},
		Targets: []Target{
			{Model: "gpt-5.6-sol", Effort: EffortXHigh},
			{Model: "deepseek-flash"},
		},
	}))

	// A fresh instance must see exactly the same configuration, so restarts keep it.
	reloaded, _, err := NewStoreAt(dir).Load()
	require.NoError(t, err)
	require.Equal(t, "画一只戴墨镜的鹈鹕。", reloaded.Prompt)
	require.True(t, reloaded.Schedule.Enabled)
	require.Equal(t, nextRun, reloaded.Schedule.NextRunAt)
	require.Equal(t, []Target{{Model: "gpt-5.6-sol", Effort: EffortXHigh}, {Model: "deepseek-flash", Effort: EffortAuto}}, reloaded.Targets)
}

func TestStore_SaveConfigRejectsInvalidInput(t *testing.T) {
	store := NewStoreAt(t.TempDir())

	require.Error(t, store.SaveConfig(Config{Prompt: "   "}), "empty prompt must be rejected")
	require.Error(t, store.SaveConfig(Config{Prompt: string(make([]rune, MaxPromptLength+1))}), "oversized prompt must be rejected")
	require.Error(t, store.SaveConfig(Config{Prompt: DefaultPrompt, Targets: []Target{{Model: "  "}}}), "empty model must be rejected")
	require.Error(t, store.SaveConfig(Config{Prompt: DefaultPrompt, Targets: []Target{{Model: "m", Effort: "ultra"}}}), "unknown effort must be rejected")

	// A rejected save must not leave a partially written state behind.
	config, _, err := store.Load()
	require.NoError(t, err)
	require.Equal(t, DefaultPrompt, config.Prompt)
	require.Empty(t, config.Targets)
}

func TestStore_SaveConfigDeduplicatesTargets(t *testing.T) {
	store := NewStoreAt(t.TempDir())

	require.NoError(t, store.SaveConfig(Config{Prompt: DefaultPrompt, Targets: []Target{
		{Model: "solver", Effort: EffortHigh},
		{Model: "solver", Effort: EffortHigh},
		{Model: "solver", Effort: EffortMax},
		{Model: " solver ", Effort: EffortHigh},
	}}))

	config, _, err := store.Load()
	require.NoError(t, err)
	// The same model at two levels stays, exact duplicates collapse, whitespace is trimmed.
	require.Equal(t, []Target{{Model: "solver", Effort: EffortHigh}, {Model: "solver", Effort: EffortMax}}, config.Targets)
}

func TestStore_KeepsResultsWhenConfigChanges(t *testing.T) {
	store := NewStoreAt(t.TempDir())
	require.NoError(t, store.SaveResults([]Result{{ID: "r1", Model: "m", Status: StatusSucceeded}}))

	require.NoError(t, store.SaveConfig(Config{Prompt: "新的题板", Targets: []Target{{Model: "m"}}}))

	config, results, err := store.Load()
	require.NoError(t, err)
	require.Equal(t, "新的题板", config.Prompt)
	require.Len(t, results, 1, "changing the prompt must not wipe history")
}

func TestStore_ConcurrentWritesKeepStateReadable(t *testing.T) {
	dir := t.TempDir()
	store := NewStoreAt(dir)

	var waitGroup sync.WaitGroup
	for index := 0; index < 12; index++ {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			_ = store.SaveResults([]Result{{ID: "r", Model: "m", Status: StatusRunning, Error: string(rune('a' + index))}})
		}(index)
	}
	waitGroup.Wait()

	raw, err := os.ReadFile(filepath.Join(dir, "state.json"))
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded), "concurrent writes must never truncate the state file")
}

func TestStore_CorruptStateFileIsReportedNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "state.json"), []byte("{not json"), 0o600))
	store := NewStoreAt(dir)

	_, _, err := store.Load()
	require.Error(t, err)
	require.Error(t, store.SaveConfig(Config{Prompt: "x"}), "a corrupt file must not be silently replaced")

	raw, readErr := os.ReadFile(filepath.Join(dir, "state.json"))
	require.NoError(t, readErr)
	require.Equal(t, "{not json", string(raw))
}

func TestStore_ArtifactAndConversationRoundTrip(t *testing.T) {
	store := NewStoreAt(t.TempDir())

	require.NoError(t, store.WriteArtifact("abc", "html", "<html><svg /></html>"))
	content, err := store.ReadArtifact("abc", "html")
	require.NoError(t, err)
	require.Equal(t, "<html><svg /></html>", content)

	require.NoError(t, store.WriteConversation("abc", Conversation{
		Prompt: "题板", Model: "m", Effort: EffortHigh, Reply: "好的", Usage: map[string]int{"total_tokens": 12},
	}))
	conversation, err := store.ReadConversation("abc")
	require.NoError(t, err)
	require.Equal(t, "好的", conversation.Reply)
	require.Equal(t, EffortHigh, conversation.Effort)
	require.Equal(t, 12, conversation.Usage["total_tokens"])

	_, err = store.ReadArtifact("missing", "html")
	require.ErrorIs(t, err, ErrNotFound)
	_, err = store.ReadConversation("missing")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestStore_RejectsUnsafeIdentifiers(t *testing.T) {
	store := NewStoreAt(t.TempDir())

	require.Error(t, store.WriteArtifact("../../etc/passwd", "html", "x"))
	require.Error(t, store.WriteConversation("..", Conversation{}))
	_, err := store.ReadArtifact("a/b", "html")
	require.Error(t, err)
}

func TestNextHourAlignsToTheTopOfTheHour(t *testing.T) {
	require.Equal(t,
		time.Date(2026, 9, 23, 7, 0, 0, 0, time.UTC),
		NextHour(time.Date(2026, 9, 23, 6, 20, 0, 0, time.UTC)))
	require.Equal(t,
		time.Date(2026, 9, 23, 7, 0, 0, 0, time.UTC),
		NextHour(time.Date(2026, 9, 23, 6, 0, 0, 0, time.UTC)),
		"an exact hour must move to the next hour, never stay put")
}
