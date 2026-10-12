//go:build mutant_notick

package mutant

// NoTick: Ticking a schedule takes no row lock and deduplicates nothing (removes G6's uniqueness).
const NoTick = true
