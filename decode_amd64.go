//go:build amd64 && !goexperiment.simd

package rapidyenc

// AMD64 always has SSE2 (Go requires it).
var useSIMDDecode = true

// decodeFast subtracts 42 from each byte using SSE2 SIMD, handling = escapes inline.
// Stops only at \r or \n (which require Go state machine for EndControl/dot-unstuffing).
// Returns (nDst, nSrc): nDst bytes written to dst, nSrc bytes consumed from src.
// nSrc >= nDst because = escape sequences consume 2 src bytes per 1 dst byte.
//
//go:noescape
func decodeFast(dst, src []byte) (nDst int, nSrc int)
