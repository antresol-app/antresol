// Package transcript makes the transcripts of audiobooks with a
// speech-to-text service, and their automatic chapters with a language
// model: made once for a book's files, in pieces, in the background, and
// kept until the files change.
package transcript

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"slices"
	"strings"
	"time"
)

// The speech-to-text providers. Groq, OpenAI and Gemini have their own
// addresses; «custom» is any OpenAI-compatible /audio/transcriptions
// (a proxy, a self-hosted Whisper server).
const (
	ProviderGroq   = "groq"
	ProviderOpenAI = "openai"
	ProviderGemini = "gemini"
	ProviderCustom = "custom"
)

// fixed are the addresses and default models of the providers with an
// address of their own.
var fixed = map[string]struct{ url, model string }{
	ProviderGroq:   {"https://api.groq.com/openai/v1", "whisper-large-v3-turbo"},
	ProviderOpenAI: {"https://api.openai.com/v1", "whisper-1"},
	ProviderGemini: {"https://generativelanguage.googleapis.com/v1beta", "gemini-3.5-flash-lite"},
}

// Config is the speech-to-text service asked.
type Config struct {
	Provider string
	// URL is the API's address; only «custom» has its own, Resolve fills
	// the others'.
	URL   string
	Model string
	Key   string
}

// Resolve is the configuration as it is used: a provider's own address
// whatever was given, its default model when none is, a custom address
// without the trailing slash.
func (c Config) Resolve() Config {
	c.Model, c.Key = strings.TrimSpace(c.Model), strings.TrimSpace(c.Key)
	if f, ok := fixed[c.Provider]; ok {
		c.URL = f.url
		if c.Model == "" {
			c.Model = f.model
		}
		return c
	}
	if c.Provider != ProviderCustom {
		c.URL = ""
		return c
	}
	c.URL = strings.TrimRight(strings.TrimSpace(c.URL), "/")
	return c
}

// Ready: a resolved configuration is enough to ask: an address and a
// model, and a key for the providers that have none without it.
func (c Config) Ready() bool {
	if c.URL == "" || c.Model == "" {
		return false
	}
	return c.Provider == ProviderCustom || c.Key != ""
}

// ConfigFromEnv: VAULT_STT_PROVIDER (groq, openai, gemini, custom; empty
// with an address: custom), VAULT_STT_URL (custom only), VAULT_STT_MODEL,
// VAULT_STT_KEY; false without enough to ask.
func ConfigFromEnv() (Config, bool) {
	c := Config{Provider: strings.ToLower(strings.TrimSpace(os.Getenv("VAULT_STT_PROVIDER"))),
		URL: os.Getenv("VAULT_STT_URL"), Model: os.Getenv("VAULT_STT_MODEL"), Key: os.Getenv("VAULT_STT_KEY")}
	if c.Provider == "" && strings.TrimSpace(c.URL) != "" {
		c.Provider = ProviderCustom
	}
	c = c.Resolve()
	if !c.Ready() {
		return Config{}, false
	}
	return c, true
}

// Audio is a piece of a recording to transcribe.
type Audio struct {
	Data []byte
	// Name and MIME of the upload («piece.mp3», audio/mp3).
	Name, MIME string
	DurationMs int64
}

// Silence is a silent recording of length d (16 kHz mono WAV): what the
// settings' check sends.
func Silence(d time.Duration) Audio {
	const rate, header = 16000, 44
	samples := int(d.Milliseconds() * rate / 1000)
	b := make([]byte, header+samples*2)
	le := binary.LittleEndian
	copy(b, "RIFF")
	le.PutUint32(b[4:], uint32(len(b)-8))
	copy(b[8:], "WAVEfmt ")
	le.PutUint32(b[16:], 16) // the fmt chunk: PCM, mono, 16 bits
	le.PutUint16(b[20:], 1)
	le.PutUint16(b[22:], 1)
	le.PutUint32(b[24:], rate)
	le.PutUint32(b[28:], rate*2)
	le.PutUint16(b[32:], 2)
	le.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	le.PutUint32(b[40:], uint32(samples*2))
	return Audio{Data: b, Name: "silence.wav", MIME: "audio/wav", DurationMs: d.Milliseconds()}
}

// Segment is a phrase and when it is said, in milliseconds.
type Segment struct {
	StartMs int64  `json:"startMs"`
	EndMs   int64  `json:"endMs"`
	Text    string `json:"text"`
}

// Result is a piece's transcript.
type Result struct {
	// Language is the ISO 639-1 code of what is spoken ("" unknown).
	Language string
	// Segments are in order, within the piece.
	Segments []Segment
	// Timed: the segments' times are the service's; else the whole text is
	// one segment with the piece's own times.
	Timed bool
}

// Client asks one speech-to-text service.
type Client struct {
	Config
	// HTTP sends the requests (nil: the default client). Redirects are
	// never followed: the key goes to the configured address only.
	HTTP *http.Client
	// Timeout of one piece (0: 15 minutes, a slow server of one's own).
	Timeout time.Duration
}

const (
	defaultTimeout = 15 * time.Minute
	answerLimit    = 8 << 20
)

// Transcribe asks for a piece's transcript. A failure is an *Error, but
// for ctx's own end.
func (c *Client) Transcribe(ctx context.Context, a Audio) (Result, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var res Result
	var err error
	if c.Provider == ProviderGemini {
		res, err = c.gemini(tctx, a)
	} else {
		res, err = c.openAI(tctx, a)
	}
	if err != nil && ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	return res, err
}

// send sends a request and reads the answer, whatever its status.
func (c *Client) send(req *http.Request) (*http.Response, []byte, error) {
	var hc http.Client
	if c.HTTP != nil {
		hc = *c.HTTP
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := hc.Do(req)
	if err != nil {
		return nil, nil, c.errorf(KindProvider, 0, "%v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, answerLimit))
	if err != nil {
		return nil, nil, c.errorf(KindProvider, resp.StatusCode, "%v", err)
	}
	return resp, raw, nil
}

// openAI is an OpenAI-compatible /audio/transcriptions: verbose_json for
// the segments' times, plain json where the model has no such format.
func (c *Client) openAI(ctx context.Context, a Audio) (Result, error) {
	resp, raw, err := c.upload(ctx, a, "verbose_json")
	if err != nil {
		return Result{}, err
	}
	if (resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnprocessableEntity) &&
		(bytes.Contains(raw, []byte("response_format")) || bytes.Contains(raw, []byte("verbose_json"))) {
		if resp, raw, err = c.upload(ctx, a, "json"); err != nil {
			return Result{}, err
		}
	}
	if resp.StatusCode != http.StatusOK {
		return Result{}, c.httpError(resp, raw)
	}
	var out struct {
		Text     string `json:"text"`
		Language string `json:"language"`
		Segments []struct {
			Start float64 `json:"start"`
			End   float64 `json:"end"`
			Text  string  `json:"text"`
		} `json:"segments"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		// a page of some other server, not this API's answer
		return Result{}, c.errorf(KindProvider, resp.StatusCode, "not a transcription answer: %s", raw)
	}
	res := Result{Language: languageCode(out.Language), Timed: true}
	timed := false
	for _, s := range out.Segments {
		timed = timed || s.End > 0
		res.Segments = append(res.Segments, Segment{int64(s.Start*1000 + 0.5), int64(s.End*1000 + 0.5), s.Text})
	}
	if !timed {
		text := out.Text
		if text == "" {
			var parts []string
			for _, s := range out.Segments {
				parts = append(parts, s.Text)
			}
			text = strings.Join(parts, " ")
		}
		return untimed(res.Language, text, a.DurationMs), nil
	}
	res.Segments = tidy(res.Segments, a.DurationMs)
	return res, nil
}

// upload sends the piece as the multipart form the API takes.
func (c *Client) upload(ctx context.Context, a Audio, format string) (*http.Response, []byte, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for _, f := range [][2]string{{"model", c.Model}, {"response_format", format}, {"temperature", "0"}} {
		if err := w.WriteField(f[0], f[1]); err != nil {
			return nil, nil, err
		}
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="file"; filename="`+a.Name+`"`)
	h.Set("Content-Type", a.MIME)
	part, err := w.CreatePart(h)
	if err != nil {
		return nil, nil, err
	}
	if _, err := part.Write(a.Data); err != nil {
		return nil, nil, err
	}
	if err := w.Close(); err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL+"/audio/transcriptions", &body)
	if err != nil {
		return nil, nil, c.errorf(KindProvider, 0, "%v", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	if c.Key != "" {
		req.Header.Set("Authorization", "Bearer "+c.Key)
	}
	return c.send(req)
}

// untimed is a transcript without times of its own: the text over the
// whole piece.
func untimed(lang, text string, durationMs int64) Result {
	text = strings.TrimSpace(text)
	if text == "" {
		return Result{Language: lang, Timed: true} // silence
	}
	return Result{Language: lang, Segments: []Segment{{0, durationMs, text}}}
}

// tidy puts segments in order within the piece: the empty ones and those
// past its end gone, the times held to it.
func tidy(segs []Segment, durationMs int64) []Segment {
	var out []Segment
	for _, s := range segs {
		s.Text = strings.TrimSpace(s.Text)
		s.StartMs = max(s.StartMs, 0)
		if s.Text == "" || durationMs > 0 && s.StartMs >= durationMs {
			continue
		}
		s.EndMs = max(s.EndMs, s.StartMs)
		if durationMs > 0 {
			s.EndMs = min(s.EndMs, durationMs)
		}
		out = append(out, s)
	}
	slices.SortStableFunc(out, func(a, b Segment) int { return int(a.StartMs - b.StartMs) })
	return out
}
