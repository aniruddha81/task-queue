package handlers

import (
	"strings"
	"testing"
)

func TestEmailMessage(t *testing.T) {
	msg, err := emailMessage("a@example.com", "Héllo", "line 1\nline 2", "job-1", "key-1")
	if err != nil {
		t.Fatal(err)
	}
	s := string(msg)
	for _, want := range []string{"To: <a@example.com>\r\n", "Subject: =?utf-8?q?H=C3=A9llo?=\r\n",
		"charset=utf-8\r\n\r\nline 1\r\nline 2\r\n"} {
		if !strings.Contains(s, want) {
			t.Errorf("message lacks %q:\n%s", want, s)
		}
	}
	if msg, _ := emailMessage("a@example.com", "s", "", "job-1", "k"); !strings.Contains(string(msg), "Sent by job job-1") {
		t.Error("an empty body should say which job sent it")
	}
	// A line break in a header field would let the payload add headers, such as a hidden Bcc.
	for _, bad := range [][2]string{
		{"a@example.com", "hi\r\nBcc: victim@example.com"},
		{"a@example.com\r\nBcc: victim@example.com", "hi"},
		{"not an address", "hi"},
	} {
		if _, err := emailMessage(bad[0], bad[1], "b", "j", "k"); err == nil {
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
