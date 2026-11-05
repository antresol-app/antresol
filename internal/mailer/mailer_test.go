package mailer

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http/httptest"
	netmail "net/mail"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSMTP is a small server with STARTTLS or TLS from the start.
type fakeSMTP struct {
	t        *testing.T
	ln       net.Listener
	cert     tls.Certificate
	implicit bool
	noTLS    bool
	size     int64
	authCode string
	rcptCode string
	dataCode string

	mu    sync.Mutex
	plain string // the base64 of AUTH PLAIN
	from  string
	rcpt  string
	data  string
	tlsOn bool
}

func startFake(t *testing.T, f *fakeSMTP) (host string, port int) {
	t.Helper()
	ts := httptest.NewUnstartedServer(nil)
	ts.StartTLS() // only for its certificate for 127.0.0.1
	f.cert = ts.TLS.Certificates[0]
	pool := x509.NewCertPool()
	pool.AddCert(ts.Certificate())
	ts.Close()
	old := tlsConfig
	tlsConfig = func(h string) *tls.Config { return &tls.Config{RootCAs: pool, ServerName: h} }
	t.Cleanup(func() { tlsConfig = old })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.ln, f.t = ln, t
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	h, p, _ := net.SplitHostPort(ln.Addr().String())
	port, _ = strconv.Atoi(p)
	return h, port
}

func (f *fakeSMTP) serve(c net.Conn) {
	defer c.Close()
	if f.implicit {
		c = tls.Server(c, &tls.Config{Certificates: []tls.Certificate{f.cert}})
		f.mu.Lock()
		f.tlsOn = true
		f.mu.Unlock()
	}
	r := bufio.NewReader(c)
	say := func(s string) { io.WriteString(c, s+"\r\n") }
	say("220 fake")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		up := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(up, "EHLO"):
			f.mu.Lock()
			on := f.tlsOn
			f.mu.Unlock()
			var ext []string
			if !on && !f.noTLS {
				ext = append(ext, "STARTTLS")
			}
			if on {
				ext = append(ext, "AUTH PLAIN")
			}
			if f.size > 0 {
				ext = append(ext, "SIZE "+strconv.FormatInt(f.size, 10))
			}
			ext = append(ext, "8BITMIME")
			io.WriteString(c, "250-fake\r\n")
			for i, e := range ext {
				sep := "-"
				if i == len(ext)-1 {
					sep = " "
				}
				io.WriteString(c, "250"+sep+e+"\r\n")
			}
		case up == "STARTTLS":
			say("220 go")
			tc := tls.Server(c, &tls.Config{Certificates: []tls.Certificate{f.cert}})
			if err := tc.Handshake(); err != nil {
				return
			}
			c, r = tc, bufio.NewReader(tc)
			f.mu.Lock()
			f.tlsOn = true
			f.mu.Unlock()
		case strings.HasPrefix(up, "AUTH PLAIN"):
			f.mu.Lock()
			f.plain = strings.TrimSpace(line[len("AUTH PLAIN"):])
			f.mu.Unlock()
			if f.authCode != "" {
				say(f.authCode)
			} else {
				say("235 ok")
			}
		case strings.HasPrefix(up, "MAIL FROM:"):
			f.mu.Lock()
			f.from = line
			f.mu.Unlock()
			say("250 ok")
		case strings.HasPrefix(up, "RCPT TO:"):
			f.mu.Lock()
			f.rcpt = line
			f.mu.Unlock()
			if f.rcptCode != "" {
				say(f.rcptCode)
			} else {
				say("250 ok")
			}
		case up == "DATA":
			say("354 go")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(strings.TrimPrefix(l, "."))
			}
			f.mu.Lock()
			f.data = b.String()
			f.mu.Unlock()
			if f.dataCode != "" {
				say(f.dataCode)
			} else {
				say("250 queued")
			}
		case up == "QUIT":
			say("221 bye")
			return
		case up == "RSET" || strings.HasPrefix(up, "NOOP"):
			say("250 ok")
		default:
			say("500 what")
		}
	}
}

func cfg(host string, port int) Config {
	return Config{Host: host, Port: port, Username: "sender@example.com", Password: "s3cret", From: "sender@example.com"}
}

func msg(payload []byte) Message {
	return Message{To: "reader@example.com", Subject: "Книга: Война и мир", Body: "Текст", Filename: "Война и мир.epub",
		ContentType: "application/epub+zip", Attachment: bytes.NewReader(payload), Size: int64(len(payload))}
}

func ctx5(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return c
}

func TestSendStartTLSAndMessage(t *testing.T) {
	f := &fakeSMTP{}
	host, port := startFake(t, f)
	payload := bytes.Repeat([]byte("книга\x00\xff"), 1000)
	if err := Send(ctx5(t), cfg(host, port), msg(payload)); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.tlsOn {
		t.Error("the login crossed without TLS")
	}
	cred, _ := base64.StdEncoding.DecodeString(f.plain)
	if string(cred) != "\x00sender@example.com\x00s3cret" {
		t.Errorf("auth %q", cred)
	}
	if !strings.Contains(f.from, "sender@example.com") || !strings.Contains(f.rcpt, "reader@example.com") {
		t.Errorf("envelope %q %q", f.from, f.rcpt)
	}
	m, err := netmail.ReadMessage(strings.NewReader(f.data))
	if err != nil {
		t.Fatal(err)
	}
	dec := new(mime.WordDecoder)
	if s, _ := dec.DecodeHeader(m.Header.Get("Subject")); s != "Книга: Война и мир" {
		t.Errorf("subject %q", s)
	}
	mt, params, err := mime.ParseMediaType(m.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/mixed" {
		t.Fatalf("%q %v", mt, err)
	}
	mr := multipart.NewReader(m.Body, params["boundary"])
	body, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	text, _ := io.ReadAll(body) // NextPart decodes only quoted-printable
	if d, _ := base64.StdEncoding.DecodeString(strings.ReplaceAll(string(text), "\r\n", "")); string(d) != "Текст" {
		t.Errorf("body %q", text)
	}
	att, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if att.FileName() != "Война и мир.epub" {
		t.Errorf("file name %q (%s)", att.FileName(), att.Header.Get("Content-Disposition"))
	}
	if ct := att.Header.Get("Content-Type"); ct != "application/epub+zip" {
		t.Errorf("type %q", ct)
	}
	raw, _ := io.ReadAll(att)
	for _, l := range strings.Split(string(raw), "\r\n") {
		if len(l) > 76 {
			t.Fatalf("line of %d columns", len(l))
		}
	}
	got, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(string(raw), "\r\n", ""))
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("payload differs: %v", err)
	}
}

func TestSendImplicitTLS(t *testing.T) {
	f := &fakeSMTP{implicit: true}
	host, port := startFake(t, f)
	c := cfg(host, port)
	old := implicitTLSPort
	implicitTLSPort = port
	t.Cleanup(func() { implicitTLSPort = old })
	if err := Send(ctx5(t), c, msg([]byte("x"))); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !strings.Contains(f.data, "attachment") {
		t.Errorf("no message: %q", f.data)
	}
}

func TestRefusesServerWithoutTLS(t *testing.T) {
	f := &fakeSMTP{noTLS: true}
	host, port := startFake(t, f)
	err := Send(ctx5(t), cfg(host, port), msg([]byte("x")))
	if !errors.Is(err, ErrNoTLS) {
		t.Fatalf("err = %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.plain != "" {
		t.Error("password sent in the clear")
	}
}

func TestLoginRefused(t *testing.T) {
	f := &fakeSMTP{authCode: "535 5.7.8 bad credentials"}
	host, port := startFake(t, f)
	if err := Send(ctx5(t), cfg(host, port), msg([]byte("x"))); !errors.Is(err, ErrAuth) {
		t.Fatalf("err = %v", err)
	}
}

func TestServerSizeLimitCheckedBeforeSending(t *testing.T) {
	f := &fakeSMTP{size: 1000}
	host, port := startFake(t, f)
	if err := Send(ctx5(t), cfg(host, port), msg(make([]byte, 2000))); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.data != "" {
		t.Error("the message was sent anyway")
	}
}

func TestSizeRefusedAfterData(t *testing.T) {
	f := &fakeSMTP{dataCode: "552 5.3.4 too big"}
	host, port := startFake(t, f)
	if err := Send(ctx5(t), cfg(host, port), msg([]byte("x"))); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v", err)
	}
}

func TestRecipientRefused(t *testing.T) {
	f := &fakeSMTP{rcptCode: "550 no such user"}
	host, port := startFake(t, f)
	if err := Send(ctx5(t), cfg(host, port), msg([]byte("x"))); !errors.Is(err, ErrRecipient) {
		t.Fatalf("err = %v", err)
	}
}

func TestHeaderInjectionRefused(t *testing.T) {
	f := &fakeSMTP{}
	host, port := startFake(t, f)
	m := msg([]byte("x"))
	m.To = "reader@example.com\r\nBcc: evil@example.com"
	if err := Send(ctx5(t), cfg(host, port), m); err == nil {
		t.Fatal("an address with a line break was taken")
	}
	m = msg([]byte("x"))
	m.Subject = "а\r\nBcc: evil@example.com"
	m.Filename = "x\r\nBcc: evil@example.com.epub"
	if err := Send(ctx5(t), cfg(host, port), m); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.Contains(f.data, "\r\nBcc:") {
		t.Errorf("a header was injected:\n%s", f.data)
	}
}

func TestCancelEndsTheExchange(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() { // accepts and says nothing
		c, err := ln.Accept()
		if err == nil {
			defer c.Close()
			time.Sleep(10 * time.Second)
		}
	}()
	_, p, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(p)
	c, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := Send(c, cfg("127.0.0.1", port), msg([]byte("x"))); err == nil {
		t.Fatal("no error")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
}

func TestSecurityBlockIsNotASizeLimit(t *testing.T) {
	// Gmail's real answer for an attachment it refuses: 552 like a size limit
	f := &fakeSMTP{dataCode: "552 5.7.0 This message was blocked because its content presents a potential security issue"}
	host, port := startFake(t, f)
	err := Send(ctx5(t), cfg(host, port), msg([]byte("x")))
	if !errors.Is(err, ErrBlocked) || errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v", err)
	}
}

func TestSizeLimitAnswers(t *testing.T) {
	for _, answer := range []string{"552 5.3.4 Message size exceeds fixed limit", "552 Too much mail data", "523 5.3.4 too big"} {
		f := &fakeSMTP{dataCode: answer}
		host, port := startFake(t, f)
		if err := Send(ctx5(t), cfg(host, port), msg([]byte("x"))); !errors.Is(err, ErrTooLarge) {
			t.Errorf("%s: %v", answer, err)
		}
	}
}

// partText is a part's decoded body (base64 or as is).
func partText(t *testing.T, p *multipart.Part) string {
	t.Helper()
	raw, _ := io.ReadAll(p)
	if p.Header.Get("Content-Transfer-Encoding") == "base64" {
		d, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(string(raw), "\r\n", ""))
		if err != nil {
			t.Fatal(err)
		}
		return string(d)
	}
	return string(raw)
}

func TestSendTextAndHTMLWithoutAttachment(t *testing.T) {
	f := &fakeSMTP{}
	host, port := startFake(t, f)
	m := Message{To: "reader@example.com", Subject: "Новые серии", Body: "Тед Лассо — 3 новые серии", HTML: "<p>Тед Лассо</p>",
		Headers: map[string]string{"List-Unsubscribe": "<https://a.example/u?t=1>", "List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
			"X-Evil\r\nBcc": "x", "X-Bad": "a\r\nBcc: evil@example.com"}}
	if err := Send(ctx5(t), cfg(host, port), m); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	msg, err := netmail.ReadMessage(strings.NewReader(f.data))
	if err != nil {
		t.Fatal(err)
	}
	if msg.Header.Get("List-Unsubscribe") != "<https://a.example/u?t=1>" || msg.Header.Get("List-Unsubscribe-Post") != "List-Unsubscribe=One-Click" {
		t.Errorf("headers %v", msg.Header)
	}
	if strings.Contains(f.data, "\r\nBcc:") || msg.Header.Get("X-Bad") != "" {
		t.Errorf("a header was injected:\n%s", f.data)
	}
	mt, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/alternative" {
		t.Fatalf("%q %v", mt, err)
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	p, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p.Header.Get("Content-Type"), "text/plain") || partText(t, p) != m.Body {
		t.Errorf("text part %v", p.Header)
	}
	p, err = mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p.Header.Get("Content-Type"), "text/html") || partText(t, p) != m.HTML {
		t.Errorf("html part %v", p.Header)
	}
	if _, err := mr.NextPart(); err != io.EOF {
		t.Errorf("a third part: %v", err)
	}
}

func TestSendTextOnly(t *testing.T) {
	f := &fakeSMTP{}
	host, port := startFake(t, f)
	if err := Send(ctx5(t), cfg(host, port), Message{To: "reader@example.com", Subject: "s", Body: "Проверка"}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	msg, err := netmail.ReadMessage(strings.NewReader(f.data))
	if err != nil {
		t.Fatal(err)
	}
	if mt, _, _ := mime.ParseMediaType(msg.Header.Get("Content-Type")); mt != "text/plain" {
		t.Fatalf("type %q", mt)
	}
	b, _ := io.ReadAll(msg.Body)
	if d, _ := base64.StdEncoding.DecodeString(strings.ReplaceAll(string(b), "\r\n", "")); string(d) != "Проверка" {
		t.Errorf("body %q", b)
	}
}
