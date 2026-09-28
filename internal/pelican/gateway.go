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
	"github.com/looplj/axonhub/internal/server/biz"
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
	channels     *biz.ChannelService
}

// NewGateway wires the gateway adapter onto the chat orchestrator AxonHub already built for its
// own OpenAI-compatible endpoint. Reusing that instance matters: it carries the channel
// selector, limiter and metrics, and building a second one would re-register them.
func NewGateway(handlers *api.OpenAIHandlers) *Gateway {
	return &Gateway{
		orchestrator: handlers.ChatCompletionHandlers.ChatCompletionOrchestrator,
		channels:     handlers.ChannelService,
	}
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

	processor := g.orchestrator
	if target.Channel > 0 {
		// Pin the round to one channel. Comparing channels is only meaningful when a retry
		// cannot silently fall back to a different one. WithAllowedChannels returns a copy, so
		// the shared orchestrator behind the public API is left untouched.
		processor = g.orchestrator.WithAllowedChannels([]int{target.Channel})
	}

	result, err := processor.Process(ctx, request)
	if err != nil {
		return g.failed(target, errors.New(RedactCredentials(truncate(err.Error(), 300))))
	}
	return g.respond(target, result)
}

// failed keeps the channel label on a rejected attempt, so a failed row still says which
// channel it was pinned to.
func (g *Gateway) failed(target Target, err error) (CompletionResult, error) {
	return CompletionResult{ChannelName: g.channelName(target.Channel)}, err
}

// channelName labels a pinned channel. An unknown or disabled channel yields an empty name and
// the round fails in candidate selection with the gateway's own error.
func (g *Gateway) channelName(id int) string {
	if id <= 0 || g.channels == nil {
		return ""
	}
	for _, channel := range g.channels.GetEnabledChannels() {
		if channel.ID == id {
			return channel.Name
		}
	}
	return ""
}

// respond turns one orchestrator result into a completion.
func (g *Gateway) respond(target Target, result orchestrator.ChatCompletionResult) (CompletionResult, error) {
	// Some channels answer with a stream even when stream=false was requested.
	if result.ChatCompletionStream != nil {
		completion, err := collectStream(result.ChatCompletionStream)
		if err != nil {
			return g.failed(target, err)
		}
		completion.ChannelName = g.channelName(target.Channel)
		return completion, nil
	}
	if result.ChatCompletion == nil {
		return g.failed(target, errors.New("the gateway returned an empty response"))
	}
	if status := result.ChatCompletion.StatusCode; status >= http.StatusBadRequest {
		detail := RedactCredentials(truncate(strings.TrimSpace(string(result.ChatCompletion.Body)), 300))
		if detail == "" {
			return g.failed(target, fmt.Errorf("upstream returned HTTP %d", status))
		}
		return g.failed(target, fmt.Errorf("upstream returned HTTP %d: %s", status, detail))
	}

	completion, err := parseChatResponse(result.ChatCompletion.Body)
	if err != nil {
		return g.failed(target, err)
	}
	completion.ChannelName = g.channelName(target.Channel)
	return completion, nil
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
