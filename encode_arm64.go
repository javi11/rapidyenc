//go:build arm64

package rapidyenc

var useSIMDEncode = true

// encodeFast adds 42 to each byte in src and stores to dst,
// stopping at the first byte whose encoded form needs escaping
// (NUL, CR, LF, or '='). Returns the number of bytes processed.
// Uses NEON SIMD for 16-byte chunks when no escaping is needed.
//
//go:noescape
func encodeFast(dst, src []byte) int
