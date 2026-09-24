package pelican

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestService_ClosesResultsInterruptedByARestart(t *testing.T) {
	store := NewStoreAt(t.TempDir())
	// A restart happens while an attempt is in flight: the entry was persisted as running and can
	// never finish, so it would stay a spinner in the gallery forever.
	require.NoError(t, store.Update(func(_ *Config, results *[]Result) error {
		*results = append(*results,
			Result{ID: "interrupted", Model: "m", Status: StatusRunning},
			Result{ID: "done", Model: "m", Status: StatusSucceeded},
		)
		return nil
	}))
	service := newService(store, newRunner(store, &fakeCompleter{}), time.Now)

	service.failInterruptedResults(context.Background())

	_, results, err := store.Load()
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.Equal(t, StatusFailed, results[0].Status)
	require.Contains(t, results[0].Error, "interrupted")
	require.Equal(t, StatusSucceeded, results[1].Status, "finished attempts are left alone")
}
