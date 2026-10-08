package handlers

import "testing"

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
