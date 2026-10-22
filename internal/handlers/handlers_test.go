package handlers

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/aniruddha81/task-queue/sdk/go/worker"
)

func TestEmailMessage(t *testing.T) {
	from := &mail.Address{Name: "Task Queue", Address: "jobs@tq.example"}
	msg, to, err := emailMessage(from, "Ann <a@example.com>", "Héllo", "line 1\nline 2", "job-1", "key-1")
	if err != nil {
		t.Fatal(err)
	}
	if to.Address != "a@example.com" {
		t.Errorf("envelope recipient %q, want the bare address", to.Address)
	}
	s := string(msg)
	for _, want := range []string{"From: \"Task Queue\" <jobs@tq.example>\r\n", "To: \"Ann\" <a@example.com>\r\n",
		"Subject: =?utf-8?q?H=C3=A9llo?=\r\n", "@tq.example>\r\n", "charset=utf-8\r\n\r\nline 1\r\nline 2\r\n"} {
		if !strings.Contains(s, want) {
			t.Errorf("message lacks %q:\n%s", want, s)
		}
	}
	if msg, _, _ := emailMessage(from, "a@example.com", "s", "", "job-1", "k"); !strings.Contains(string(msg), "Sent by job job-1") {
		t.Error("an empty body should say which job sent it")
	}
	// A line break in a header field would let the payload add headers, such as a hidden Bcc.
	for _, bad := range [][2]string{
		{"a@example.com", "hi\r\nBcc: victim@example.com"},
		{"a@example.com\r\nBcc: victim@example.com", "hi"},
		{"not an address", "hi"},
	} {
		if _, _, err := emailMessage(from, bad[0], bad[1], "b", "j", "k"); err == nil {
			t.Errorf("emailMessage(%q, %q) accepted", bad[0], bad[1])
		}
	}
}

func TestWebhookAllowlist(t *testing.T) {
	allow := []string{"https://sinks:8090/webhook"}
	for url, want := range map[string]bool{
		"https://sinks:8090/webhook":              true,
		"https://sinks:8090/webhook/orders":       true,
		"https://sinks:8090/webhookx":             false,
		"https://sinks:8090.evil.example/webhook": false,
		"http://169.254.169.254/latest/meta-data": false, // cloud metadata (SSRF)
		"https://jobs:8080/v1/jobs":               false, // internal service (SSRF)
		"":                                        false,
	} {
		if got := allowed(url, allow); got != want {
			t.Errorf("allowed(%q) = %v, want %v", url, got, want)
		}
	}
	if allowed("https://anything", nil) {
		t.Error("an empty allowlist must allow nothing")
	}
}

// TestEmailRouting: example.com stays on the test server, anyone else goes to the relay, and a
// 5xx refusal fails the job for good while a dead server is retried.
func TestEmailRouting(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() { // answers everything with 250, then refuses the recipient with 554
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				tp := textproto.NewConn(conn)
				tp.PrintfLine("220 test")
				for {
					line, err := tp.ReadLine()
					if err != nil {
						return
					}
					if strings.HasPrefix(line, "RCPT") {
						tp.PrintfLine("554 Email address is not verified")
					} else {
						tp.PrintfLine("250 ok")
					}
				}
			}()
		}
	}()
	c := Config{SMTP: ln.Addr().String(), Relay: "127.0.0.1:1", From: "Task Queue <jobs@tq.example>"}
	send := func(to string) error {
		return c.emailSend(context.Background(), worker.Job{ID: "j", EffectKey: "k", Payload: []byte(`{"to":"` + to + `","subject":"s"}`)})
	}
	if err := send("a@example.com"); fmt.Sprintf("%T", err) != "worker.permanent" {
		t.Errorf("example.com via the test server: got %v (%T), want a permanent 554", err, err)
	}
	if err := send("a@real.test"); err == nil || fmt.Sprintf("%T", err) == "worker.permanent" {
		t.Errorf("real recipient via the dead relay: got %v (%T), want a retryable error", err, err)
	}
}

// TestDemoFreeze: both attempts send the transfer with the same effect key, and only the
// first stalls.
func TestDemoFreeze(t *testing.T) {
	var keys []string
	sinks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Idempotency-Key"))
	}))
	defer sinks.Close()
	freeze = 200 * time.Millisecond
	c := Config{Sinks: sinks.URL, Client: sinks.Client()}
	for attempt, stalls := range map[int]bool{1: true, 2: false} {
		start := time.Now()
		err := c.demoFreeze(context.Background(), worker.Job{Attempt: attempt, EffectKey: "k",
			Payload: []byte(`{"from":"visitor","to":"shop","amount":100}`)})
		if took := time.Since(start); err != nil || (took >= freeze) != stalls {
			t.Errorf("attempt %d: err %v after %v, want it to stall: %v", attempt, err, took, stalls)
		}
	}
	if len(keys) != 2 || keys[0] != "k" || keys[1] != "k" {
		t.Errorf("effect keys sent: %v, want [k k]", keys)
	}
}
