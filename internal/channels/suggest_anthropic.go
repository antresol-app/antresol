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
// or without a path; a path such as /v1 is ignored, see anthropicBase).
func IsAnthropicURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	h := strings.ToLower(u.Hostname())
	return h == "anthropic.com" || strings.HasSuffix(h, ".anthropic.com")
}

func (l *LLM) anthropic() bool { return IsAnthropicURL(l.URL) }

// anthropicBase is the address the SDK is given: the scheme and host only,
// over https. The SDK adds /v1/messages itself, so an address copied with
// its /v1 (as the OpenAI-compatible ones end) would call /v1/v1/messages.
func anthropicBase(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return AnthropicURL
	}
	return "https://" + strings.ToLower(u.Host)
}

// chatAnthropic sends the messages to the Messages API: the system message
// goes to System, the rest (the few-shot pairs end on the user's request)
// stay in order. No temperature and no thinking config: current models
// reject both; a low effort keeps a short answer quick. The JSON contract
// is the prompt's (jsonPart reads the object out of the text), or, with a
// schema, structured outputs' (output_config.format).
func (l *LLM) chatAnthropic(ctx context.Context, msgs []map[string]string, schema map[string]any) (string, error) {
	// Only the key of the settings: without one the SDK would take the
	// host's (ANTHROPIC_API_KEY, a profile on disk), and it would also add
	// ANTHROPIC_CUSTOM_HEADERS and ANTHROPIC_BASE_URL of the environment.
	key := strings.TrimSpace(l.Key)
	if key == "" {
		return "", errors.New("llm: an API key is required for the Anthropic API")
	}
	opts := []option.RequestOption{option.WithoutEnvironmentDefaults(), option.WithAPIKey(key), option.WithBaseURL(anthropicBase(l.URL))}
	if l.Client != nil {
		opts = append(opts, option.WithHTTPClient(l.Client))
	}
	client := anthropic.NewClient(opts...)
	params := anthropic.MessageNewParams{
		Model:        anthropic.Model(l.Model),
		MaxTokens:    16000,
		OutputConfig: anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffortLow},
	}
	if schema != nil {
		params.OutputConfig.Format = anthropic.JSONOutputFormatParam{Schema: schema}
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
