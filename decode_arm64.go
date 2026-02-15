//go:build arm64

package rapidyenc

// ARM64 always has NEON (ASIMD), no runtime detection needed.
var useSIMDDecode = true

// decodeFast subtracts 42 from each byte using NEON SIMD, handling = escapes inline.
// Stops only at \r or \n (which require Go state machine for EndControl/dot-unstuffing).
// Returns (nDst, nSrc): nDst bytes written to dst, nSrc bytes consumed from src.
// nSrc >= nDst because = escape sequences consume 2 src bytes per 1 dst byte.
//
//go:noescape
func decodeFast(dst, src []byte) (nDst int, nSrc int)
