package pelican

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

var errTestFailure = errors.New("test failure")

func newTestHandlers(t *testing.T, completer ChatCompleter) (*gin.Engine, *Store) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	store := NewStoreAt(t.TempDir())
	clock := func() time.Time { return time.Date(2026, 9, 23, 7, 20, 0, 0, time.UTC) }
	runner := newRunner(store, completer, WithClock(clock))
	handlers := NewHandlers(newService(store, runner, clock))

	router := gin.New()
	handlers.RegisterRoutes(router.Group("/admin"))
	return router, store
}

func doJSON(t *testing.T, router *gin.Engine, method, path, body string) (int, map[string]any) {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	payload := map[string]any{}
	if recorder.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload), recorder.Body.String())
	}
	return recorder.Code, payload
}

func TestHandlers_ConfigRoundTrip(t *testing.T) {
	router, store := newTestHandlers(t, &fakeCompleter{})

	status, config := doJSON(t, router, http.MethodGet, "/admin/pelican/config", "")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, DefaultPrompt, config["prompt"], "the default prompt is offered so the UI can restore it")
	require.NotEmpty(t, config["defaultPrompt"])

	status, saved := doJSON(t, router, http.MethodPut, "/admin/pelican/config",
		`{"prompt":"画一只戴帽子的鹈鹕。","scheduleEnabled":true,"targets":[{"model":"gpt-5.6-sol","effort":"xhigh"},{"model":"deepseek-flash","effort":""}]}`)
	require.Equal(t, http.StatusOK, status, saved)
	require.Equal(t, true, saved["scheduleEnabled"])
	// Enabling the schedule must schedule the upcoming top of the hour.
	require.Equal(t, "2026-09-23T08:00:00Z", saved["nextRunAt"], "the next slot is the top of the following hour")

	stored, err := store.Config()
	require.NoError(t, err)
	require.Len(t, stored.Targets, 2)
	require.Equal(t, EffortXHigh, stored.Targets[0].Effort)
	require.Equal(t, EffortAuto, stored.Targets[1].Effort)
}

func TestHandlers_ConfigRejectsBadInput(t *testing.T) {
	router, _ := newTestHandlers(t, &fakeCompleter{})

	status, body := doJSON(t, router, http.MethodPut, "/admin/pelican/config", `{"prompt":"x","targets":[]}`)
	require.Equal(t, http.StatusBadRequest, status)
	require.Contains(t, body["error"], "select at least one model")

	status, body = doJSON(t, router, http.MethodPut, "/admin/pelican/config", `{"prompt":"x","targets":[{"model":"m","effort":"ultra"}]}`)
	require.Equal(t, http.StatusBadRequest, status)
	require.Contains(t, body["error"], "unsupported reasoning effort")

	status, _ = doJSON(t, router, http.MethodPut, "/admin/pelican/config", `{not json`)
	require.Equal(t, http.StatusBadRequest, status)
}

func TestHandlers_RoundLifecycleAndArtifacts(t *testing.T) {
	completer := &fakeCompleter{handle: func(_ context.Context, target Target) (CompletionResult, error) {
		if target.Model == "broken" {
			return CompletionResult{}, errTestFailure
		}
		return CompletionResult{Reply: "这是代码：\n```html\n<!doctype html><html><body><svg></svg></body></html>\n```\n希望有帮助。", FinishReason: "stop"}, nil
	}}
	router, _ := newTestHandlers(t, completer)

	// A round without targets is refused instead of doing nothing.
	status, body := doJSON(t, router, http.MethodPost, "/admin/pelican/rounds", "")
	require.Equal(t, http.StatusBadRequest, status)
	require.Contains(t, body["error"], "select at least one model")

	status, _ = doJSON(t, router, http.MethodPut, "/admin/pelican/config",
		`{"prompt":"题板","targets":[{"model":"good","effort":""},{"model":"broken","effort":"max"}]}`)
	require.Equal(t, http.StatusOK, status)

	status, _ = doJSON(t, router, http.MethodPost, "/admin/pelican/rounds", "")
	require.Equal(t, http.StatusAccepted, status)

	// Wait for the background round to finish, not just for the records to appear.
	require.Eventually(t, func() bool {
		_, result := doJSON(t, router, http.MethodGet, "/admin/pelican/results", "")
		return result["succeeded"] == float64(1) && result["failed"] == float64(1)
	}, 5*time.Second, 20*time.Millisecond)

	_, listing := doJSON(t, router, http.MethodGet, "/admin/pelican/results", "")
	require.Equal(t, float64(1), listing["succeeded"])
	require.Equal(t, float64(1), listing["failed"])

	byModel := map[string]map[string]any{}
	for _, entry := range listing["results"].([]any) {
		result := entry.(map[string]any)
		byModel[result["model"].(string)] = result
	}

	good := byModel["good"]
	require.Equal(t, StatusSucceeded, good["status"])
	require.Equal(t, "html", good["format"])

	// The artifact is the document only, served with a sandbox policy.
	artifactRequest := httptest.NewRequest(http.MethodGet, "/admin/pelican/results/"+good["id"].(string)+"/artifact", nil)
	artifactRecorder := httptest.NewRecorder()
	router.ServeHTTP(artifactRecorder, artifactRequest)
	require.Equal(t, http.StatusOK, artifactRecorder.Code)
	require.Contains(t, artifactRecorder.Header().Get("Content-Security-Policy"), "sandbox allow-scripts")
	require.Equal(t, "<!doctype html><html><body><svg></svg></body></html>", artifactRecorder.Body.String())

	// The conversation keeps the raw reply including the surrounding prose.
	conversationRequest := httptest.NewRequest(http.MethodGet, "/admin/pelican/results/"+good["id"].(string)+"/conversation", nil)
	conversationRecorder := httptest.NewRecorder()
	router.ServeHTTP(conversationRecorder, conversationRequest)
	require.Equal(t, http.StatusOK, conversationRecorder.Code)
	var conversation Conversation
	require.NoError(t, json.Unmarshal(conversationRecorder.Body.Bytes(), &conversation))
	require.Contains(t, conversation.Reply, "希望有帮助")
	require.Equal(t, "题板", conversation.Prompt)

	broken := byModel["broken"]
	require.Equal(t, StatusFailed, broken["status"])
	require.Contains(t, broken["error"], "test failure")
}

func TestHandlers_MissingResultsReturnNotFound(t *testing.T) {
	router, _ := newTestHandlers(t, &fakeCompleter{})

	for _, path := range []string{"/admin/pelican/results/nope/artifact", "/admin/pelican/results/nope/conversation"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusNotFound, recorder.Code, path)
	}
}
