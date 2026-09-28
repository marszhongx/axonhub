package pelican

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/server/orchestrator"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
)

func TestGatewayRespond_ExtractsTextContent(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
	}{
		{name: "string", content: `"Here is the SVG:\n<svg></svg>\n"`},
		{name: "text parts", content: `[{"type":"text","text":"Here is the SVG:\n<svg>"},{"type":"text","text":"</svg>\n"}]`},
		{
			name: "image followed by SVG text",
			content: `[{"type":"image_url","image_url":{"url":"data:image/png;base64,aW1hZ2U="}},
				{"type":"text","text":"Here is the SVG:\n<svg>"},
				{"type":"text","text":null},
				{"type":"text","text":"</svg>\n"}]`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`{
				"id":"chatcmpl-pelican", "object":"chat.completion", "created":1791444665, "model":"pelican-model",
				"choices":[{"index":0,"message":{"role":"assistant","content":%s},"finish_reason":"stop"}],
				"usage":{"prompt_tokens":12,"completion_tokens":34,"total_tokens":46}
			}`, tc.content)
			completion, err := (&Gateway{}).respond(Target{Model: "pelican-model"}, orchestrator.ChatCompletionResult{
				ChatCompletion: &httpclient.Response{StatusCode: http.StatusOK, Body: []byte(body)},
			})
			require.NoError(t, err)
			require.Equal(t, "Here is the SVG:\n<svg></svg>\n", completion.Reply)
			require.Equal(t, "stop", completion.FinishReason)
			require.Equal(t, map[string]int{"prompt_tokens": 12, "completion_tokens": 34, "total_tokens": 46}, completion.Usage)

			document, format, ok := ExtractDocument(completion.Reply)
			require.True(t, ok, "multipart replies must reach the document extractor")
			require.Equal(t, "<svg></svg>", document)
			require.Equal(t, "svg", format)
		})
	}
}

func TestGatewayRespond_CollectsStreamTextContent(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
	}{
		{name: "string", content: `"</svg>"`},
		{
			name: "image and text parts",
			content: `[{"type":"image_url","image_url":{"url":"data:image/png;base64,aW1hZ2U="}},
				{"type":"text","text":"</"},{"type":"text","text":"svg>"}]`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream := streams.SliceStream([]*httpclient.StreamEvent{
				{Data: []byte(`{"id":"chatcmpl-pelican","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":null},"finish_reason":null}]}`)},
				{Data: []byte(`{"id":"chatcmpl-pelican","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"<svg>"},"finish_reason":null}]}`)},
				{Data: []byte(fmt.Sprintf(`{"id":"chatcmpl-pelican","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":%s},"finish_reason":"stop"}]}`, tc.content))},
				{Data: []byte(`{"id":"chatcmpl-pelican","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":12,"completion_tokens":34,"total_tokens":46}}`)},
				{Data: []byte("[DONE]")},
			})
			completion, err := (&Gateway{}).respond(Target{Model: "pelican-model"}, orchestrator.ChatCompletionResult{
				ChatCompletionStream: stream,
			})
			require.NoError(t, err)
			require.Equal(t, "<svg></svg>", completion.Reply, "array-valued deltas must not be silently dropped")
			require.Equal(t, "stop", completion.FinishReason)
			require.Equal(t, map[string]int{"prompt_tokens": 12, "completion_tokens": 34, "total_tokens": 46}, completion.Usage)
		})
	}
}

func TestGatewayRespond_RejectsEmptyOrInvalidContent(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "invalid JSON", body: `{not json`},
		{name: "no choices", body: `{"choices":[]}`},
		{name: "missing message", body: `{"choices":[{"finish_reason":"stop"}]}`},
		{name: "null content", body: `{"choices":[{"message":{"content":null}}]}`},
		{name: "empty string", body: `{"choices":[{"message":{"content":" "}}]}`},
		{name: "empty parts", body: `{"choices":[{"message":{"content":[]}}]}`},
		{name: "invalid content type", body: `{"choices":[{"message":{"content":{"text":"<svg></svg>"}}}]}`},
		{name: "image only", body: `{"choices":[{"message":{"content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,aW1hZ2U="}}]}}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := (&Gateway{}).respond(Target{Model: "pelican-model"}, orchestrator.ChatCompletionResult{
				ChatCompletion: &httpclient.Response{StatusCode: http.StatusOK, Body: []byte(tc.body)},
			})
			require.Error(t, err)
		})
	}
}
