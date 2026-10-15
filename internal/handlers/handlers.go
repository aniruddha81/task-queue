// Package handlers holds the demo workloads. Each passes job.EffectKey to its destination,
// which is how an effect happens once even when a job runs twice (G4), except email:
// SMTP has no idempotency, so email.send is the documented at-least-once case.
package handlers

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"mime"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"os"
	"strings"
	"time"

	"github.com/aniruddha81/task-queue/sdk/go/worker"
)

type Config struct {
	Sinks        string       // base URL of the sinks service
	SMTP         string       // host:port of the test mail server (Mailpit)
	Relay        string       // host:port of a real mail server (SES, port 587); empty: all mail goes to SMTP
	RelayUser    string       // SMTP credentials for Relay
	RelayPass    string       //
	From         string       // sender, "Name <address>"; empty: jobs@task-queue.local
	WebhookAllow []string     // URL prefixes webhook.deliver may call (anything else is refused)
	Client       *http.Client // mTLS client for the sinks
}

// Register adds every demo handler to w.
func Register(w *worker.Worker, c Config) {
	w.Handle("ledger.transfer", c.ledgerTransfer)
	w.Handle("webhook.deliver", c.webhookDeliver)
	w.Handle("report.daily", c.reportDaily)
	w.Handle("email.send", c.emailSend)
	w.Handle("chaos.sleep", chaosSleep)
	w.Handle("chaos.fail", chaosFail)
	w.Handle("chaos.overrun", chaosOverrun)
	w.Handle("chaos.crash", chaosCrash)
}

func decode(job worker.Job, v any) error {
	if err := json.Unmarshal(job.Payload, v); err != nil {
		return worker.Permanent(fmt.Errorf("bad payload: %w", err))
	}
	return nil
}

func (c Config) ledgerTransfer(ctx context.Context, job worker.Job) error {
	var p struct {
		From, To string
		Amount   int64
	}
	if err := decode(job, &p); err != nil {
		return err
	}
	body, _ := json.Marshal(p)
	return c.post(ctx, c.Sinks+"/ledger/transfer", job.EffectKey, body)
}

func (c Config) webhookDeliver(ctx context.Context, job worker.Job) error {
	var p struct {
		URL  string
		Body json.RawMessage
	}
	if err := decode(job, &p); err != nil {
		return err
	}
	if !allowed(p.URL, c.WebhookAllow) {
		return worker.Permanent(fmt.Errorf("webhook URL %q is not allowlisted", p.URL))
	}
	if len(p.Body) == 0 {
		p.Body = json.RawMessage(`{}`)
	}
	return c.post(ctx, p.URL, job.EffectKey, p.Body)
}

func (c Config) reportDaily(ctx context.Context, job worker.Job) error {
	return c.post(ctx, c.Sinks+"/digest", job.EffectKey, []byte(`{}`))
}

// allowed matches whole URL prefixes, so "https://sinks:8090/webhook" does not admit
// "https://sinks:8090.evil.example/".
func allowed(url string, prefixes []string) bool {
	for _, p := range prefixes {
		if p != "" && (url == p || strings.HasPrefix(url, strings.TrimSuffix(p, "/")+"/")) {
			return true
		}
	}
	return false
}

// post sends body with the effect key. 2xx is success (including "already applied");
// 4xx other than 429 won't improve with retries; anything else is retried.
func (c Config) post(ctx context.Context, url, key string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return worker.Permanent(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	resp, err := c.Client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	switch {
	case resp.StatusCode < 300:
		return nil
	case resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests:
		return worker.Permanent(fmt.Errorf("%s: %s", url, resp.Status))
	}
	return fmt.Errorf("%s: %s", url, resp.Status)
}

// emailSend can deliver twice: if the worker dies after sending but before reporting, the
// retry sends again. Message-ID lets a recipient spot the duplicate, but SMTP itself
// can't refuse it.
//
// With a Relay, real recipients get real mail. example.com recipients (the chaos harness's,
// a reserved domain no one can receive at) still go to Mailpit: through SES they would only
// bounce, and bounces get an SES account suspended.
func (c Config) emailSend(ctx context.Context, job worker.Job) error {
	var p struct{ To, Subject, Body string }
	if err := decode(job, &p); err != nil {
		return err
	}
	from, err := mail.ParseAddress(cmp.Or(c.From, "jobs@task-queue.local"))
	if err != nil {
		return worker.Permanent(fmt.Errorf("sender %q: %w", c.From, err))
	}
	msg, to, err := emailMessage(from, p.To, p.Subject, p.Body, job.ID, job.EffectKey)
	if err != nil {
		return worker.Permanent(err)
	}
	server, auth := c.SMTP, smtp.Auth(nil)
	if c.Relay != "" && !strings.HasSuffix(strings.ToLower(to.Address), "@example.com") {
		host, _, _ := net.SplitHostPort(c.Relay)
		server, auth = c.Relay, smtp.PlainAuth("", c.RelayUser, c.RelayPass, host)
	}
	err = smtp.SendMail(server, auth, from.Address, []string{to.Address}, msg)
	// 5xx is a refusal (unverified recipient, bad credentials) that a retry won't change;
	// 4xx (throttling, greylisting) and network errors are retried.
	if reply := (*textproto.Error)(nil); errors.As(err, &reply) && reply.Code >= 500 {
		return worker.Permanent(err)
	}
	return err
}

// emailMessage builds a plain-text UTF-8 message. A line break in to or subject would let the
// payload add its own headers (a hidden Bcc), so it is refused. The body may have any line
// endings; SMTP gets CRLF, and net/smtp escapes lines that start with a dot.
func emailMessage(from *mail.Address, to, subject, body, jobID, effectKey string) ([]byte, *mail.Address, error) {
	addr, err := mail.ParseAddress(to)
	if err != nil || strings.ContainsAny(to, "\r\n") {
		return nil, nil, fmt.Errorf("bad recipient %q", to)
	}
	if strings.ContainsAny(subject, "\r\n") {
		return nil, nil, errors.New("subject may not contain line breaks")
	}
	if body == "" {
		body = "Sent by job " + jobID
	}
	body = strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n")
	sum := sha256.Sum256([]byte(effectKey))
	domain := from.Address[strings.LastIndexByte(from.Address, '@')+1:]
	return []byte("From: " + from.String() + "\r\nTo: " + addr.String() +
		"\r\nSubject: " + mime.QEncoding.Encode("utf-8", subject) +
		"\r\nMessage-ID: <" + hex.EncodeToString(sum[:16]) + "@" + domain + ">" +
		"\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + body + "\r\n"), addr, nil
}

// chaosSleep sleeps for {"ms": N}, stopping early if its context ends.
func chaosSleep(ctx context.Context, job worker.Job) error {
	var p struct{ MS int }
	if err := decode(job, &p); err != nil {
		return err
	}
	select {
	case <-time.After(time.Duration(p.MS) * time.Millisecond):
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// chaosFail fails with probability {"p": 0..1}.
func chaosFail(_ context.Context, job worker.Job) error {
	var p struct{ P float64 }
	if err := decode(job, &p); err != nil {
		return err
	}
	if rand.Float64() < p.P {
		return errors.New("chaos.fail: unlucky")
	}
	return nil
}

// chaosOverrun ignores its deadline: it sleeps {"ms": N} regardless, then reports success
// too late. The lease has expired by then, so the completion is fenced (G3).
func chaosOverrun(_ context.Context, job worker.Job) error {
	var p struct{ MS int }
	if err := decode(job, &p); err != nil {
		return err
	}
	time.Sleep(time.Duration(p.MS) * time.Millisecond)
	return nil
}

// chaosCrash kills the whole worker process with probability {"p": 0..1}, taking every
// job it holds with it. Their leases expire and the reaper requeues them.
func chaosCrash(_ context.Context, job worker.Job) error {
	var p struct{ P float64 }
	if err := decode(job, &p); err != nil {
		return err
	}
	if rand.Float64() < p.P {
		os.Exit(3)
	}
	return nil
}
