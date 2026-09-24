package pelican

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/server/api"
	"github.com/looplj/axonhub/internal/server/orchestrator"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
)

// Gateway implements ChatCompleter on top of AxonHub's own chat completion pipeline.
//
// This is the only file that depends on gateway internals: channel selection, protocol
// transforms, retries and usage logging all behave exactly like a normal API request, and
// no API key is needed because the call never leaves the process. If AxonHub's internal
// request types change upstream, this is the file to adjust.
type Gateway struct {
	orchestrator *orchestrator.ChatCompletionOrchestrator
}

// NewGateway wires the gateway adapter onto the chat orchestrator AxonHub already built for its
// own OpenAI-compatible endpoint. Reusing that instance matters: it carries the channel
// selector, limiter and metrics, and building a second one would re-register them.
func NewGateway(handlers *api.OpenAIHandlers) *Gateway {
	return &Gateway{orchestrator: handlers.ChatCompletionHandlers.ChatCompletionOrchestrator}
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model           string        `json:"model"`
	Messages        []chatMessage `json:"messages"`
	Stream          bool          `json:"stream"`
	ReasoningEffort string        `json:"reasoning_effort,omitempty"`
}

type chatChoice struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
	Delta struct {
		Content string `json:"content"`
	} `json:"delta"`
	FinishReason string `json:"finish_reason"`
}

type chatResponse struct {
	Choices []chatChoice `json:"choices"`
	Usage   *usage       `json:"usage"`
}

type usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// withProject stamps the project on a context. Scheduled rounds have no HTTP request, so the
// project recorded in the configuration is used instead.
func withProject(ctx context.Context, projectID int) context.Context {
	return contexts.WithProjectID(ctx, projectID)
}

// Complete asks the gateway to run one chat completion for the target.
func (g *Gateway) Complete(ctx context.Context, target Target, prompt string) (CompletionResult, error) {
	body, err := json.Marshal(chatRequest{
		Model:           target.Model,
		Messages:        []chatMessage{{Role: "user", Content: prompt}},
		Stream:          false,
		ReasoningEffort: string(target.Effort),
	})
	if err != nil {
		return CompletionResult{}, fmt.Errorf("encode pelican request: %w", err)
	}

	request := &httpclient.Request{
		Method:      http.MethodPost,
		Path:        "/v1/chat/completions",
		APIFormat:   llm.APIFormatOpenAIChatCompletion.String(),
		ContentType: "application/json",
		Headers:     http.Header{"Content-Type": []string{"application/json"}},
		Body:        body,
	}

	result, err := g.orchestrator.Process(ctx, request)
	if err != nil {
		return CompletionResult{}, errors.New(RedactCredentials(truncate(err.Error(), 300)))
	}

	// Some channels answer with a stream even when stream=false was requested.
	if result.ChatCompletionStream != nil {
		return collectStream(result.ChatCompletionStream)
	}
	if result.ChatCompletion == nil {
		return CompletionResult{}, errors.New("the gateway returned an empty response")
	}
	if status := result.ChatCompletion.StatusCode; status >= http.StatusBadRequest {
		detail := RedactCredentials(truncate(strings.TrimSpace(string(result.ChatCompletion.Body)), 300))
		if detail == "" {
			return CompletionResult{}, fmt.Errorf("upstream returned HTTP %d", status)
		}
		return CompletionResult{}, fmt.Errorf("upstream returned HTTP %d: %s", status, detail)
	}

	return parseChatResponse(result.ChatCompletion.Body)
}

// collectStream aggregates an SSE chat stream into a single reply.
func collectStream(stream streams.Stream[*httpclient.StreamEvent]) (CompletionResult, error) {
	defer func() { _ = stream.Close() }()

	var builder strings.Builder
	completion := CompletionResult{Usage: map[string]int{}}

	for stream.Next() {
		event := stream.Current()
		if event == nil || len(event.Data) == 0 {
			continue
		}
		var chunk chatResponse
		if err := json.Unmarshal(event.Data, &chunk); err != nil {
			continue // keep-alive and non-JSON events are expected on SSE streams
		}
		for _, choice := range chunk.Choices {
			builder.WriteString(choice.Delta.Content)
			if choice.FinishReason != "" {
				completion.FinishReason = choice.FinishReason
			}
		}
		if chunk.Usage != nil {
			completion.Usage = usageToMap(*chunk.Usage)
		}
	}
	if err := stream.Err(); err != nil {
		return CompletionResult{}, errors.New(RedactCredentials(truncate(err.Error(), 300)))
	}

	completion.Reply = builder.String()
	if strings.TrimSpace(completion.Reply) == "" {
		return CompletionResult{}, errors.New("the gateway returned an empty reply")
	}
	return completion, nil
}

func parseChatResponse(body []byte) (CompletionResult, error) {
	var decoded chatResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return CompletionResult{}, errors.New("the gateway returned a response that is not a chat completion")
	}
	if len(decoded.Choices) == 0 {
		return CompletionResult{}, errors.New("the gateway returned no choices")
	}

	completion := CompletionResult{
		Reply:        decoded.Choices[0].Message.Content,
		FinishReason: decoded.Choices[0].FinishReason,
	}
	if decoded.Usage != nil {
		completion.Usage = usageToMap(*decoded.Usage)
	}
	if strings.TrimSpace(completion.Reply) == "" {
		return CompletionResult{}, errors.New("the gateway returned an empty reply")
	}
	return completion, nil
}

func usageToMap(value usage) map[string]int {
	return map[string]int{
		"prompt_tokens":     value.PromptTokens,
		"completion_tokens": value.CompletionTokens,
		"total_tokens":      value.TotalTokens,
	}
}
