//go:build amd64

package rapidyenc

// AMD64 always has SSE2 (Go requires it).
var useSIMDDecode = true

// decodeFast subtracts 42 from each byte using SSE2 SIMD.
// Stops at any special byte (\r, \n, =) and returns the count of clean bytes processed.
//
//go:noescape
func decodeFast(dst, src []byte) int
