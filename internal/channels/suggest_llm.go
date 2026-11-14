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
// schema) or any OpenAI-compatible endpoint (a URL ending in /v1, with
// VAULT_LLM_KEY). Small models do: the words (suggestWords) are trusted
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
}

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

func (l *LLM) openAI() bool { return strings.HasSuffix(l.URL, "/v1") }

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
	content, err := l.chat(ctx, msgs)
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
func (l *LLM) chat(ctx context.Context, msgs []map[string]string) (string, error) {
	var body map[string]any
	path := "/api/chat"
	if l.openAI() {
		path = "/chat/completions"
		body = map[string]any{"model": l.Model, "messages": msgs, "temperature": 0, "response_format": map[string]string{"type": "json_object"}}
	} else {
		opts := map[string]any{"temperature": 0}
		if l.Threads > 0 {
			opts["num_thread"] = l.Threads
		}
		body = map[string]any{"model": l.Model, "messages": msgs, "stream": false, "think": false, "format": "json", "options": opts, "keep_alive": "2m"}
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
		return "", fmt.Errorf("llm: %s", resp.Status)
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
		return "", errors.New("llm: empty answer")
	}
	return out.Message.Content, nil
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
