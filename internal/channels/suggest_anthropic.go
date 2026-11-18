package channels

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
)

// AnthropicURL is the address «Anthropic Claude» stores: the Messages API's
// own, spoken through the official SDK (not an OpenAI-compatible shim).
const AnthropicURL = "https://api.anthropic.com"

// IsAnthropicURL: an address of the Anthropic API (api.anthropic.com, with
// or without a path).
func IsAnthropicURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	h := strings.ToLower(u.Hostname())
	return h == "anthropic.com" || strings.HasSuffix(h, ".anthropic.com")
}

func (l *LLM) anthropic() bool { return l.Anthropic || IsAnthropicURL(l.URL) }

// chatAnthropic sends the messages to the Messages API: the system message
// goes to System, the rest (the few-shot pairs end on the user's request)
// stay in order. No temperature and no thinking config: current models
// reject both; a low effort keeps a short answer quick. The JSON contract
// is the prompt's (jsonPart reads the object out of the text).
func (l *LLM) chatAnthropic(ctx context.Context, msgs []map[string]string) (string, error) {
	opts := []option.RequestOption{option.WithAPIKey(l.Key), option.WithBaseURL(l.URL)}
	if l.Client != nil {
		opts = append(opts, option.WithHTTPClient(l.Client))
	}
	client := anthropic.NewClient(opts...)
	params := anthropic.MessageNewParams{
		Model:        anthropic.Model(l.Model),
		MaxTokens:    16000,
		OutputConfig: anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffortLow},
	}
	for _, m := range msgs {
		block := anthropic.NewTextBlock(m["content"])
		switch m["role"] {
		case "system":
			params.System = append(params.System, anthropic.TextBlockParam{Text: m["content"]})
		case "assistant":
			params.Messages = append(params.Messages, anthropic.NewAssistantMessage(block))
		default:
			params.Messages = append(params.Messages, anthropic.NewUserMessage(block))
		}
	}
	resp, err := client.Messages.New(ctx, params)
	if err != nil {
		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) {
			detail := strings.TrimSpace(apiErr.RawJSON())
			var body struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.Unmarshal([]byte(detail), &body) == nil && body.Error.Message != "" {
				detail = body.Error.Message // the provider's own words, not its JSON
			}
			if r := []rune(detail); len(r) > 200 {
				detail = string(r[:200]) + "…"
			}
			return "", fmt.Errorf("llm: %d %s", apiErr.StatusCode, detail)
		}
		return "", fmt.Errorf("llm: %w", err)
	}
	if resp.StopReason == anthropic.StopReasonRefusal {
		if e := strings.TrimSpace(resp.StopDetails.Explanation); e != "" {
			return "", fmt.Errorf("llm: the model declined the request: %s", e)
		}
		return "", errors.New("llm: the model declined the request")
	}
	var text strings.Builder
	for _, b := range resp.Content {
		if t, ok := b.AsAny().(anthropic.TextBlock); ok {
			text.WriteString(t.Text)
		}
	}
	if strings.TrimSpace(text.String()) == "" {
		return "", errors.New("llm: empty answer")
	}
	return text.String(), nil
}
