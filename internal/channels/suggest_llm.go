package channels

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// LLM is a language model that reads a channel request into tags: a local
// Ollama (VAULT_LLM_URL=http://host:11434, its native /api/chat with a JSON
// schema), any OpenAI-compatible endpoint (a URL ending in /v1 or, Gemini, /openai, with
// VAULT_LLM_KEY) or Anthropic's Messages API (https://api.anthropic.com). Small models do: the words (suggestWords) are trusted
// first, the model only fills in.
type LLM struct {
	URL   string
	Model string
	Key   string
	// Threads: Ollama's num_thread (0: its default). In a container with a
	// CPU quota the default can be ~30× slower.
	Threads int
	// Timeout of one answer (0: 45 s).
	Timeout time.Duration
	Client  *http.Client
	// Chain: the models behind this one, in the admin's order (nil: this
	// model alone). See Chain.
	Chain *Chain
}

// Tier is how heavy a model an AI task needs: the admin sets one model of
// each (light: Haiku-class, many quick calls; medium: Sonnet-class;
// powerful: Opus-class) and every task asks its own.
type Tier string

const (
	TierLight    Tier = "light"
	TierMedium   Tier = "medium"
	TierPowerful Tier = "powerful"
)

// Feature is one thing the admin's «ИИ-помощник» lets the server do with a
// model (a language model, or the speech model of the transcripts); each is
// listed on its page with a switch, on unless turned off. A task asks its
// feature's model (Service.ModelFor): a feature that is off gets none, and
// goes on as it does without a model.
type Feature string

const (
	// FeatureDescribe: «Опишите канал» reads a sentence into a channel's rules.
	FeatureDescribe Feature = "describe"
	// FeatureFranchises: the library's franchises (their channels) are told apart.
	FeatureFranchises Feature = "franchises"
	// FeatureIPTVCategories: the external channels get categories and languages.
	FeatureIPTVCategories Feature = "iptvCategories"
	// FeatureIPTVVariants: look-alike entries of a playlist are judged one channel or two.
	FeatureIPTVVariants Feature = "iptvVariants"
	// FeatureBookNames: a book's names in other languages, the model the last resort.
	FeatureBookNames Feature = "bookNames"
	// FeatureChapters: an audiobook's chapters made of its transcript.
	FeatureChapters Feature = "chapters"
	// FeatureTranscripts: an audiobook's transcript by speech recognition (not a language model).
	FeatureTranscripts Feature = "transcripts"
)

// Features are all of them, in the order the page lists them.
var Features = []Feature{FeatureDescribe, FeatureFranchises, FeatureIPTVCategories, FeatureIPTVVariants, FeatureBookNames, FeatureChapters, FeatureTranscripts}

// LLMFromEnv: VAULT_LLM_URL, VAULT_LLM_MODEL, VAULT_LLM_KEY,
// VAULT_LLM_THREADS, VAULT_LLM_TIMEOUT (seconds); nil without a URL and model.
func LLMFromEnv() *LLM {
	url, model := strings.TrimRight(strings.TrimSpace(os.Getenv("VAULT_LLM_URL")), "/"), strings.TrimSpace(os.Getenv("VAULT_LLM_MODEL"))
	if url == "" || model == "" {
		return nil
	}
	l := &LLM{URL: url, Model: model, Key: strings.TrimSpace(os.Getenv("VAULT_LLM_KEY"))}
	l.Threads, _ = strconv.Atoi(os.Getenv("VAULT_LLM_THREADS"))
	if n, _ := strconv.Atoi(os.Getenv("VAULT_LLM_TIMEOUT")); n > 0 {
		l.Timeout = time.Duration(n) * time.Second
	}
	return l
}

func (l *LLM) timeout() time.Duration {
	if l.Timeout > 0 {
		return l.Timeout
	}
	return 45 * time.Second
}

// openAI: an OpenAI-compatible endpoint, its address ending in /v1 (OpenAI,
// Groq, OpenRouter, a LiteLLM) or /openai (Gemini's
// https://generativelanguage.googleapis.com/v1beta/openai); else Ollama.
func (l *LLM) openAI() bool {
	u := strings.TrimRight(l.URL, "/")
	return strings.HasSuffix(u, "/v1") || strings.HasSuffix(u, "/openai")
}

// llmAnswer is what the model is asked to return.
type llmAnswer struct {
	Name      string   `json:"name"`
	Kinds     []string `json:"kinds"`
	Genres    []string `json:"genres"`
	Countries []string `json:"countries"`
	Languages []string `json:"languages"`
	YearFrom  int      `json:"yearFrom"`
	YearTo    int      `json:"yearTo"`
	Keywords  []string `json:"keywords"`
}

const llmSystem = `You turn a request for a TV channel into filters over a home film library. Answer JSON only.
kinds: "movie" and/or "series" (empty: both).
genres: only from the list given.
countries: ISO 3166-1 alpha-2 production countries; the Soviet Union is SU, Russia is RU.
languages: ISO 639-1 original languages, only when the request names the original language.
yearFrom, yearTo: the years asked for (0: none).
keywords: English TMDB keywords for themes (space, christmas, time travel), empty when none.
name: a short channel name in the request's language.
Use only what the request asks for; leave the rest empty.`

var llmShots = [][2]string{
	{"комедии 90-х", `{"name":"Комедии 90-х","kinds":["movie"],"genres":["комедия"],"countries":[],"languages":[],"yearFrom":1990,"yearTo":1999,"keywords":[]}`},
	{"French films about space", `{"name":"French space films","kinds":["movie"],"genres":[],"countries":["FR"],"languages":[],"yearFrom":0,"yearTo":0,"keywords":["space"]}`},
	{"советские мультфильмы", `{"name":"Советские мультфильмы","kinds":[],"genres":["мультфильм"],"countries":["SU"],"languages":[],"yearFrom":0,"yearTo":0,"keywords":[]}`},
}

func (l *LLM) suggest(ctx context.Context, text string, v Vocabulary) (Suggestion, error) {
	genres := sortedKeys(v.Genres)
	system := llmSystem + "\nGenres: " + strings.Join(genres, ", ") + "."
	msgs := []map[string]string{{"role": "system", "content": system}}
	for _, s := range llmShots {
		msgs = append(msgs, map[string]string{"role": "user", "content": s[0]}, map[string]string{"role": "assistant", "content": s[1]})
	}
	msgs = append(msgs, map[string]string{"role": "user", "content": text})
	content, err := l.chat(ctx, msgs, true)
	if err != nil {
		return Suggestion{}, err
	}
	var a llmAnswer
	if err := json.Unmarshal([]byte(jsonPart(content)), &a); err != nil {
		return Suggestion{}, fmt.Errorf("llm: %w", err)
	}
	return Suggestion{Name: a.Name, Rules: Rules{Kinds: a.Kinds, Genres: a.Genres, Countries: a.Countries, Languages: a.Languages,
		YearFrom: a.YearFrom, YearTo: a.YearTo, Keywords: a.Keywords}}, nil
}

// chat sends the messages and returns the answer's text.
func (l *LLM) chat(ctx context.Context, msgs []map[string]string, asJSON bool) (string, error) {
	return l.chatFormat(ctx, msgs, asJSON, nil)
}

// chatFormat is chat with the answer held to a JSON schema (nil: none):
// Anthropic's structured outputs, an OpenAI-compatible endpoint's strict
// json_schema response format, Ollama's format schema. An OpenAI-compatible
// endpoint that refuses the schema (400: a proxy or a model without
// structured outputs) is asked again for a plain JSON object; the caller
// validates the answer either way.
func (l *LLM) chatFormat(ctx context.Context, msgs []map[string]string, asJSON bool, schema map[string]any) (string, error) {
	if l.Chain != nil {
		return l.Chain.run(ctx, func(ctx context.Context, m *LLM) (string, error) {
			out, err := m.chatFormat(ctx, msgs, asJSON, schema)
			if err == nil && (asJSON || schema != nil) {
				err = answerJSON(out)
			}
			return out, err
		})
	}
	if l.anthropic() {
		return l.chatAnthropic(ctx, msgs, schema)
	}
	if schema != nil && l.openAI() {
		out, err := l.chatHTTP(ctx, msgs, true, schema)
		var st httpStatusErr
		if errors.As(err, &st) && st.code == http.StatusBadRequest {
			return l.chatHTTP(ctx, msgs, true, nil)
		}
		return out, err
	}
	return l.chatHTTP(ctx, msgs, asJSON, schema)
}

// httpStatusErr: an endpoint's answer other than 200 (the provider's
// words, cut short).
type httpStatusErr struct {
	code int
	text string
}

func (e httpStatusErr) Error() string { return e.text }

func (l *LLM) chatHTTP(ctx context.Context, msgs []map[string]string, asJSON bool, schema map[string]any) (string, error) {
	var body map[string]any
	path := "/api/chat"
	if l.openAI() {
		path = "/chat/completions"
		body = map[string]any{"model": l.Model, "messages": msgs, "temperature": 0}
		switch {
		case schema != nil:
			body["response_format"] = map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "answer", "strict": true, "schema": schema}}
		case asJSON:
			body["response_format"] = map[string]string{"type": "json_object"}
		}
	} else {
		opts := map[string]any{"temperature": 0}
		if l.Threads > 0 {
			opts["num_thread"] = l.Threads
		}
		body = map[string]any{"model": l.Model, "messages": msgs, "stream": false, "think": false, "options": opts, "keep_alive": "2m"}
		switch {
		case schema != nil:
			body["format"] = schema
		case asJSON:
			body["format"] = "json"
		}
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.URL+path, bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if l.Key != "" {
		req.Header.Set("Authorization", "Bearer "+l.Key)
	}
	c := l.Client
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		detail := clipRunes(strings.TrimSpace(string(raw)), 200, "…")
		return "", httpStatusErr{code: resp.StatusCode, text: fmt.Sprintf("llm: %s %s", resp.Status, detail)}
	}
	var out struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	if len(out.Choices) > 0 {
		return out.Choices[0].Message.Content, nil
	}
	if out.Message.Content == "" {
		return "", errEmptyAnswer
	}
	return out.Message.Content, nil
}

// AskJSON asks one question whose answer is a JSON object (another
// package's task: its own system prompt): the object's text, read out of
// any prose or code fence around it.
func (l *LLM) AskJSON(ctx context.Context, system, user string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, l.timeout())
	defer cancel()
	out, err := l.chat(ctx, []map[string]string{{"role": "system", "content": system}, {"role": "user", "content": user}}, true)
	if err != nil {
		return "", err
	}
	return jsonPart(out), nil
}

// AskSchema is AskJSON with the answer held to schema (a JSON Schema
// object every provider accepts: objects with additionalProperties false
// and every property required, no numeric or length limits). The answer's
// text is still the caller's to validate: a provider without structured
// outputs falls back to a plain JSON object.
func (l *LLM) AskSchema(ctx context.Context, system, user string, schema map[string]any) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, l.timeout())
	defer cancel()
	out, err := l.chatFormat(ctx, []map[string]string{{"role": "system", "content": system}, {"role": "user", "content": user}}, true, schema)
	if err != nil {
		return "", err
	}
	return jsonPart(out), nil
}

// Ping asks a one-word question (the settings' «Проверить»): the answer,
// or why there is none (a provider's own words, cut short).
func (l *LLM) Ping(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, l.timeout())
	defer cancel()
	out, err := l.chat(ctx, []map[string]string{{"role": "user", "content": "Reply with the single word: ok"}}, false)
	if err != nil {
		return "", err
	}
	return clipRunes(strings.TrimSpace(out), 80, "…"), nil
}

// jsonPart: the object in an answer that wrapped it in prose or a code fence.
func jsonPart(s string) string {
	i, j := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if i < 0 || j < i {
		return s
	}
	return s[i : j+1]
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
