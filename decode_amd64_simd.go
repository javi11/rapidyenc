//go:build goexperiment.simd && amd64

package rapidyenc

import "simd/archsimd"

// useSIMDDecode indicates whether the SIMD decode fast path is available.
// archsimd 128-bit ops on AMD64 require AVX.
var useSIMDDecode = archsimd.X86.AVX()

// decodeFast subtracts 42 from each byte using archsimd SIMD intrinsics, handling = escapes inline.
// Stops only at \r or \n (which require Go state machine for EndControl/dot-unstuffing).
// Returns (nDst, nSrc): nDst bytes written to dst, nSrc bytes consumed from src.
// nSrc >= nDst because = escape sequences consume 2 src bytes per 1 dst byte.
func decodeFast(dst, src []byte) (nDst int, nSrc int) {
	vCR := archsimd.BroadcastUint8x16(13)
	vLF := archsimd.BroadcastUint8x16(10)
	vEQ := archsimd.BroadcastUint8x16(61)
	vBias := archsimd.BroadcastUint8x16(42)

	for nSrc < len(src) {
		// SIMD path: process 16 bytes at a time
		if nSrc+16 <= len(src) {
			v := archsimd.LoadUint8x16Slice(src[nSrc:])

			// Detect specials: cr | lf | eq
			maskCR := v.Equal(vCR)
			maskLF := v.Equal(vLF)
			maskEQ := v.Equal(vEQ)
			combined := maskCR.Or(maskLF).Or(maskEQ)

			if combined.ToBits() == 0 {
				// Fast path: no specials — subtract 42, store
				v.Sub(vBias).StoreSlice(dst[nDst:])
				nDst += 16
				nSrc += 16
				continue
			}
		}

		// Scalar tail: handle = escapes inline, stop at \r or \n
		c := src[nSrc]
		if c == '\r' || c == '\n' {
			return
		}
		if c == '=' {
			// Need at least 2 bytes (= + escaped byte)
			if nSrc+1 >= len(src) {
				return
			}
			next := src[nSrc+1]
			// If escaped byte is \r or \n, bail to Go state machine
			if next == '\r' || next == '\n' {
				return
			}
			dst[nDst] = next - 42 - 64 // -106
			nDst++
			nSrc += 2
			continue
		}
		// Normal byte: subtract 42
		dst[nDst] = c - 42
		nDst++
		nSrc++
	}
	return
}
