// Package taskqueue is the root of a distributed job scheduler; see full_plan(v4).md.
package taskqueue

// Regenerate gen/ from proto/ with `go generate .` (CI fails if gen/ is stale).
//go:generate go run github.com/bufbuild/buf/cmd/buf@v1.73.0 lint
//go:generate go run github.com/bufbuild/buf/cmd/buf@v1.73.0 generate
