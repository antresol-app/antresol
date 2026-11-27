package transcript

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Gemini transcribes through generateContent: the audio inline (a piece is
// far under the 20 MB a request may hold) and the answer held to a JSON
// schema.

const geminiPrompt = `Transcribe the speech of this recording word for word, in the language spoken.
Split it into segments of one or two sentences, none longer than about 15 seconds.
For each segment give start and end as MM:SS.s from the beginning of this recording, and its text.
language: the ISO 639-1 code of the spoken language.
When nothing is spoken, answer with no segments.
Do not translate, summarise or add anything that is not said.`

func geminiSchema() map[string]any {
	str := map[string]any{"type": "string"}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"language": str,
			"segments": map[string]any{"type": "array", "items": map[string]any{
				"type":       "object",
				"properties": map[string]any{"start": str, "end": str, "text": str},
				"required":   []string{"start", "end", "text"},
			}},
		},
		"required": []string{"language", "segments"},
	}
}

func (c *Client) gemini(ctx context.Context, a Audio) (Result, error) {
	parts := []any{
		map[string]any{"text": geminiPrompt},
		map[string]any{"inlineData": map[string]any{"mimeType": a.MIME, "data": base64.StdEncoding.EncodeToString(a.Data)}},
	}
	ask := func(config map[string]any) (*http.Response, []byte, error) {
		body, err := json.Marshal(map[string]any{"contents": []any{map[string]any{"role": "user", "parts": parts}}, "generationConfig": config})
		if err != nil {
			return nil, nil, err
		}
		model := url.PathEscape(strings.TrimPrefix(c.Model, "models/"))
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL+"/models/"+model+":generateContent", bytes.NewReader(body))
		if err != nil {
			return nil, nil, c.errorf(KindProvider, 0, "%v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-goog-api-key", c.Key) // never in the address, which logs keep
		return c.send(req)
	}
	resp, raw, err := ask(map[string]any{"responseFormat": map[string]any{"text": map[string]any{"mimeType": "APPLICATION_JSON", "schema": geminiSchema()}}})
	if err != nil {
		return Result{}, err
	}
	// an endpoint that predates responseFormat
	if resp.StatusCode == http.StatusBadRequest && bytes.Contains(raw, []byte("responseFormat")) {
		if resp, raw, err = ask(map[string]any{"responseMimeType": "application/json", "responseJsonSchema": geminiSchema()}); err != nil {
			return Result{}, err
		}
	}
	if resp.StatusCode != http.StatusOK {
		return Result{}, c.httpError(resp, raw)
	}
	var out struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text    string `json:"text"`
					Thought bool   `json:"thought"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		PromptFeedback struct {
			BlockReason string `json:"blockReason"`
		} `json:"promptFeedback"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Result{}, c.errorf(KindProvider, resp.StatusCode, "not a generateContent answer: %s", raw)
	}
	refused := func(why string) (Result, error) {
		e := c.errorf(KindInput, resp.StatusCode, "the model gave no transcript: %s", why)
		e.Shorter = true
		return Result{}, e
	}
	if len(out.Candidates) == 0 {
		return refused(out.PromptFeedback.BlockReason)
	}
	cand := out.Candidates[0]
	if cand.FinishReason != "" && cand.FinishReason != "STOP" {
		return refused(cand.FinishReason)
	}
	var text strings.Builder
	for _, p := range cand.Content.Parts {
		if !p.Thought {
			text.WriteString(p.Text)
		}
	}
	var ans struct {
		Language string `json:"language"`
		Segments []struct {
			Start json.RawMessage `json:"start"`
			End   json.RawMessage `json:"end"`
			Text  string          `json:"text"`
		} `json:"segments"`
	}
	if err := json.Unmarshal([]byte(text.String()), &ans); err != nil {
		return Result{}, c.errorf(KindInput, resp.StatusCode, "the model's answer is not the JSON asked for: %s", text.String())
	}
	res := Result{Language: languageCode(ans.Language), Timed: true}
	// times a model got wrong (not times, past the end, going back, all at
	// the start) are no times
	good := true
	var last, latest int64
	var words []string
	for _, s := range ans.Segments {
		start, okStart := clock(s.Start)
		end, okEnd := clock(s.End)
		good = good && okStart && okEnd && start >= last-backSlackMs && (a.DurationMs <= 0 || start <= a.DurationMs+endSlackMs)
		last, latest = start, max(latest, start, end)
		res.Segments = append(res.Segments, Segment{start, end, s.Text})
		if t := strings.TrimSpace(s.Text); t != "" {
			words = append(words, t)
		}
	}
	if len(ans.Segments) > 0 && (!good || latest == 0) {
		return untimed(res.Language, strings.Join(words, " "), a.DurationMs), nil
	}
	res.Segments = tidy(res.Segments, a.DurationMs)
	return res, nil
}

const (
	// endSlackMs and backSlackMs: how far a segment's start may pass the
	// recording's end, or lie before the one before it, and still be a time.
	endSlackMs  = 5_000
	backSlackMs = 2_000
)

// clock reads a time as a model writes it, in milliseconds: MM:SS.s,
// HH:MM:SS, or seconds.
func clock(raw json.RawMessage) (int64, bool) {
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	fields := strings.Split(strings.TrimSuffix(s, "s"), ":")
	if len(fields) > 3 {
		return 0, false
	}
	total := 0.0
	for _, f := range fields {
		v, err := strconv.ParseFloat(strings.Replace(strings.TrimSpace(f), ",", ".", 1), 64)
		if err != nil || v < 0 || math.IsInf(v, 0) || math.IsNaN(v) {
			return 0, false
		}
		total = total*60 + v
	}
	return int64(math.Round(total * 1000)), true
}
