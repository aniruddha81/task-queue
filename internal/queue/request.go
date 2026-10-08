package queue

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"
)

var nameRE = regexp.MustCompile(`^[a-z0-9._-]{1,64}$`)

// ValidName reports whether s is a valid queue or job type name.
func ValidName(s string) bool { return nameRE.MatchString(s) }

// ParseJob validates a job request body (the POST /v1/jobs format, also a schedule's job
// template) and fills in defaults. The caller sets Owner and Key.
func ParseJob(raw []byte) (NewJob, error) {
	var b struct {
		Queue          string          `json:"queue"`
		Type           string          `json:"type"`
		Payload        json.RawMessage `json:"payload"`
		Priority       int             `json:"priority"`
		RunAt          *time.Time      `json:"run_at"`
		MaxAttempts    *int            `json:"max_attempts"`
		TimeoutSeconds *int            `json:"timeout_seconds"`
		Affinity       *string         `json:"affinity"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		return NewJob{}, fmt.Errorf("invalid JSON body: %w", err)
	}
	switch {
	case !ValidName(b.Queue):
		return NewJob{}, errors.New("queue must match " + nameRE.String())
	case !ValidName(b.Type):
		return NewJob{}, errors.New("type must match " + nameRE.String())
	case b.Priority < -1000 || b.Priority > 1000:
		return NewJob{}, errors.New("priority must be between -1000 and 1000")
	case b.Affinity != nil && *b.Affinity != "aws" && *b.Affinity != "azure":
		return NewJob{}, errors.New(`affinity must be "aws", "azure" or absent`)
	}
	j := NewJob{
		Request: raw, Queue: b.Queue, Type: b.Type, Payload: b.Payload, Priority: b.Priority,
		RunAt: b.RunAt, Affinity: b.Affinity, MaxAttempts: 5, TimeoutSeconds: 60,
	}
	if len(j.Payload) == 0 {
		j.Payload = json.RawMessage(`{}`)
	}
	if b.MaxAttempts != nil {
		j.MaxAttempts = *b.MaxAttempts
	}
	if b.TimeoutSeconds != nil {
		j.TimeoutSeconds = *b.TimeoutSeconds
	}
	if j.MaxAttempts < 1 || j.MaxAttempts > 50 {
		return NewJob{}, errors.New("max_attempts must be between 1 and 50")
	}
	if j.TimeoutSeconds < 1 || j.TimeoutSeconds > 3600 {
		return NewJob{}, errors.New("timeout_seconds must be between 1 and 3600")
	}
	return j, nil
}
