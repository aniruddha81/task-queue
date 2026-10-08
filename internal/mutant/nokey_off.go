//go:build !mutant_nokey

package mutant

// NoKey: Submit makes every key unique (removes G2's idempotency).
const NoKey = false
