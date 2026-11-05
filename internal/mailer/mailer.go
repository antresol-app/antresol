// Package mailer sends one message over SMTP: a text with one attachment
// (Send to Kindle) or with its HTML form (notification digests). The server
// and the sender are what the administrator configured; a connection without
// TLS is refused, so a password never crosses the network in the clear.
package mailer

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	// ErrAuth: the server refused the login or password.
	ErrAuth = errors.New("mailer: login refused")
	// ErrTooLarge: the message is larger than the server takes.
	ErrTooLarge = errors.New("mailer: message too large")
	// ErrBlocked: the server refused the content as unsafe.
	ErrBlocked = errors.New("mailer: message blocked as unsafe")
	// ErrRecipient: the server refused the sender or the recipient.
	ErrRecipient = errors.New("mailer: address refused")
	// ErrNoTLS: the server offers no STARTTLS.
	ErrNoTLS = errors.New("mailer: server offers no TLS")
)

// Config is an SMTP account.
type Config struct {
	Host     string
	Port     int // 465: TLS from the start; any other: STARTTLS (required)
	Username string
	Password string
	From     string
}

// Message has one text and either one attachment or an HTML form of the
// text (or neither).
type Message struct {
	To, Subject, Body string
	// HTML, when set and there is no Attachment, is sent with Body as
	// multipart/alternative.
	HTML        string
	Filename    string // shown to the recipient; any language
	ContentType string
	Attachment  io.Reader // nil: none
	Size        int64     // of Attachment: checked against the server's limit
	// Headers are added as is (List-Unsubscribe); a name that is not a
	// token, or a value with a line break, is dropped.
	Headers map[string]string
}

// implicitTLSPort is the port with TLS from the start (a variable for tests).
var implicitTLSPort = 465

// tlsConfig: a variable so that tests can trust their own certificate.
var tlsConfig = func(host string) *tls.Config { return &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12} }

// Send delivers m to the server; ctx bounds the whole exchange.
func Send(ctx context.Context, cfg Config, m Message) error {
	from, err := mail.ParseAddress(cfg.From)
	if err != nil {
		return fmt.Errorf("%w: sender: %v", ErrRecipient, err)
	}
	to, err := mail.ParseAddress(m.To)
	if err != nil {
		return fmt.Errorf("%w: recipient: %v", ErrRecipient, err)
	}
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	if d, ok := ctx.Deadline(); ok {
		conn.SetDeadline(d)
	}
	stop := context.AfterFunc(ctx, func() { conn.SetDeadline(time.Now()) })
	defer stop()

	var nc net.Conn = conn
	if cfg.Port == implicitTLSPort {
		tc := tls.Client(conn, tlsConfig(cfg.Host))
		if err := tc.HandshakeContext(ctx); err != nil {
			return err
		}
		nc = tc
	}
	c, err := smtp.NewClient(nc, cfg.Host)
	if err != nil {
		return err
	}
	defer c.Close()
	if cfg.Port != implicitTLSPort {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return ErrNoTLS
		}
		if err := c.StartTLS(tlsConfig(cfg.Host)); err != nil {
			return err
		}
	}
	// the message grows by base64 (4/3) and its line breaks
	if ok, limit := c.Extension("SIZE"); ok {
		if n, err := strconv.ParseInt(strings.TrimSpace(limit), 10, 64); err == nil && n > 0 && m.Size/3*4*78/76+4096 > n {
			return ErrTooLarge
		}
	}
	if cfg.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)); err != nil {
			if smtpCode(err)/100 == 5 {
				return fmt.Errorf("%w: %v", ErrAuth, err)
			}
			return err
		}
	}
	if err := c.Mail(from.Address); err != nil {
		return classify(err, ErrRecipient)
	}
	if err := c.Rcpt(to.Address); err != nil {
		return classify(err, ErrRecipient)
	}
	w, err := c.Data()
	if err != nil {
		return classify(err, ErrRecipient)
	}
	if err := writeMessage(w, from, to, m); err != nil {
		w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return classify(err, ErrRecipient)
	}
	// the message is accepted: a failing QUIT changes nothing
	c.Quit()
	return nil
}

func smtpCode(err error) int {
	var te *textproto.Error
	if errors.As(err, &te) {
		return te.Code
	}
	return 0
}

// classify names a refusal. The enhanced status decides: 5.7.x is a policy
// refusal even under 552, which otherwise means a size limit (Gmail answers
// «552 5.7.0» for an attachment it holds unsafe); other permanent refusals of
// this step are as.
func classify(err, as error) error {
	code := smtpCode(err)
	var te *textproto.Error
	policy := errors.As(err, &te) && strings.HasPrefix(strings.TrimSpace(te.Msg), "5.7.")
	switch {
	case policy && (code == 552 || code == 554):
		return fmt.Errorf("%w: %v", ErrBlocked, err)
	case code == 552 || code == 523:
		return fmt.Errorf("%w: %v", ErrTooLarge, err)
	case code/100 == 5:
		return fmt.Errorf("%w: %v", as, err)
	}
	return err
}

func writeMessage(w io.Writer, from, to *mail.Address, m Message) error {
	mw := multipart.NewWriter(io.Discard)
	boundary := mw.Boundary()
	hdr := "From: " + from.String() + "\r\n" +
		"To: " + to.String() + "\r\n" +
		"Subject: " + mime.QEncoding.Encode("utf-8", m.Subject) + "\r\n" +
		"Date: " + time.Now().Format(time.RFC1123Z) + "\r\n" +
		extraHeaders(m.Headers) +
		"MIME-Version: 1.0\r\n"
	if m.Attachment == nil && m.HTML == "" {
		hdr += "Content-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\n"
		if _, err := io.WriteString(w, hdr); err != nil {
			return err
		}
		return encodeBase64(w, strings.NewReader(m.Body))
	}
	kind := "mixed"
	if m.Attachment == nil {
		kind = "alternative"
	}
	hdr += `Content-Type: multipart/` + kind + `; boundary="` + boundary + `"` + "\r\n\r\n"
	if _, err := io.WriteString(w, hdr); err != nil {
		return err
	}
	mp := multipart.NewWriter(w)
	if err := mp.SetBoundary(boundary); err != nil {
		return err
	}
	text, err := mp.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {"text/plain; charset=utf-8"},
		"Content-Transfer-Encoding": {"base64"},
	})
	if err != nil {
		return err
	}
	if err := encodeBase64(text, strings.NewReader(m.Body)); err != nil {
		return err
	}
	if m.Attachment == nil {
		html, err := mp.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {"text/html; charset=utf-8"},
			"Content-Transfer-Encoding": {"base64"},
		})
		if err != nil {
			return err
		}
		if err := encodeBase64(html, strings.NewReader(m.HTML)); err != nil {
			return err
		}
		return mp.Close()
	}
	ct := m.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	attach, err := mp.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {ct},
		"Content-Disposition":       {contentDisposition(m.Filename)},
		"Content-Transfer-Encoding": {"base64"},
	})
	if err != nil {
		return err
	}
	if err := encodeBase64(attach, m.Attachment); err != nil {
		return err
	}
	return mp.Close()
}

// extraHeaders are the message's own headers, one per line, in name order;
// none can add a line of its own.
func extraHeaders(h map[string]string) string {
	names := make([]string, 0, len(h))
	for k := range h {
		names = append(names, k)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, k := range names {
		v := h[k]
		if !headerToken(k) || strings.ContainsAny(v, "\r\n") {
			continue
		}
		b.WriteString(textproto.CanonicalMIMEHeaderKey(k) + ": " + v + "\r\n")
	}
	return b.String()
}

func headerToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

// contentDisposition names the attachment for every mail parser: an ASCII
// filename (non-ASCII characters replaced) and the UTF-8 filename* of
// RFC 2231.
func contentDisposition(name string) string {
	var ascii strings.Builder
	nonASCII := false
	for _, r := range name {
		switch {
		case r < 0x20 || r == 0x7f || r == '/' || r == '\\':
			nonASCII = true // never in a header, not even encoded
		case r == '"':
			ascii.WriteByte('_')
		case r > 0x7e:
			ascii.WriteByte('_')
			nonASCII = true
		default:
			ascii.WriteRune(r)
		}
	}
	out := `attachment; filename="` + ascii.String() + `"`
	if nonASCII {
		out += `; filename*=UTF-8''` + rfc2231(name)
	}
	return out
}

func rfc2231(s string) string {
	const keep = "!#$&+-.^_`|~"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte(keep, c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// lineWriter breaks base64 text into the 76 columns of RFC 2045.
type lineWriter struct {
	w io.Writer
	n int
}

func (l *lineWriter) Write(p []byte) (int, error) {
	total := len(p)
	for len(p) > 0 {
		k := min(76-l.n, len(p))
		if _, err := l.w.Write(p[:k]); err != nil {
			return 0, err
		}
		p, l.n = p[k:], l.n+k
		if l.n == 76 {
			if _, err := io.WriteString(l.w, "\r\n"); err != nil {
				return 0, err
			}
			l.n = 0
		}
	}
	return total, nil
}

func encodeBase64(w io.Writer, r io.Reader) error {
	lw := &lineWriter{w: w}
	enc := base64.NewEncoder(base64.StdEncoding, lw)
	if _, err := io.Copy(enc, r); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	if lw.n > 0 {
		_, err := io.WriteString(w, "\r\n")
		return err
	}
	return nil
}
