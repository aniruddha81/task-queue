// Package mutant holds compile-time switches that each remove one safety mechanism, so
// the chaos checker can prove it notices (week 7, "testing the tester"). Every switch is
// a constant that is false unless its build tag is set:
//
//	go build -tags mutant_nofence ./cmd/dispatch
//
// Normal builds contain none of this behaviour; nothing at runtime can turn it on.
package mutant
