//go:build !mutant_ackfirst

package mutant

// AckFirst: The API acknowledges a submit before it commits (breaks G1).
const AckFirst = false
