package pgtest

import "testing"

func TestWithDatabase(t *testing.T) {
	for in, want := range map[string]string{
		"postgres://u:p@h:5432/postgres?sslmode=require":  "postgres://u:p@h:5432/test_x?sslmode=require",
		"postgres://u:p@h1:5432,h2:5434/postgres?x=1&y=2": "postgres://u:p@h1:5432,h2:5434/test_x?x=1&y=2",
		"postgres://u:p@h:5432":                           "postgres://u:p@h:5432/test_x",
	} {
		if got := withDatabase(in, "test_x"); got != want {
			t.Errorf("withDatabase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWithHost(t *testing.T) {
	for in, want := range map[string]string{
		"postgres://u:p@h1:5432,h2:5434/db?x=1": "postgres://u:p@127.0.0.1:5434/db?x=1",
		"postgres://h1:5432/db":                 "postgres://127.0.0.1:5434/db",
	} {
		if got := withHost(in, "127.0.0.1:5434"); got != want {
			t.Errorf("withHost(%q) = %q, want %q", in, got, want)
		}
	}
}
