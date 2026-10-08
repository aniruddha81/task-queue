//go:build mutant_nofence

package mutant

// NoFence: Complete ignores the lease token (removes G3's fencing).
const NoFence = true
