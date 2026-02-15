//go:build arm64

package rapidyenc

// ARM64 always has NEON (ASIMD), no runtime detection needed.
var useSIMDDecode = true

// decodeFast subtracts 42 from each byte using NEON SIMD.
// Stops at any special byte (\r, \n, =) and returns the count of clean bytes processed.
//
//go:noescape
func decodeFast(dst, src []byte) int
