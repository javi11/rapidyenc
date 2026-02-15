//go:build goexperiment.simd && amd64

package rapidyenc

import "simd/archsimd"

// useSIMDEncode indicates whether the SIMD encode fast path is available.
// archsimd 128-bit ops on AMD64 require AVX.
var useSIMDEncode = archsimd.X86.AVX()

// encodeFast adds 42 to each byte in src and stores to dst,
// stopping at the first byte whose encoded form needs escaping
// (NUL=0, CR=13, LF=10, '='=61). Returns the number of bytes processed.
// Uses archsimd SIMD intrinsics for 16-byte chunks when no escaping is needed.
func encodeFast(dst, src []byte) int {
	vBias := archsimd.BroadcastUint8x16(42)
	vNUL := archsimd.BroadcastUint8x16(0)
	vCR := archsimd.BroadcastUint8x16(13)
	vLF := archsimd.BroadcastUint8x16(10)
	vEQ := archsimd.BroadcastUint8x16(61)

	n := 0

	// SIMD loop: process 16 bytes at a time
	for n+16 <= len(src) {
		v := archsimd.LoadUint8x16Slice(src[n:])
		encoded := v.Add(vBias)

		// Check for specials in encoded output
		maskNUL := encoded.Equal(vNUL)
		maskCR := encoded.Equal(vCR)
		maskLF := encoded.Equal(vLF)
		maskEQ := encoded.Equal(vEQ)
		combined := maskNUL.Or(maskCR).Or(maskLF).Or(maskEQ)

		if combined.ToBits() != 0 {
			break
		}

		encoded.StoreSlice(dst[n:])
		n += 16
	}

	// Scalar tail: process remaining bytes, stop at first special
	for n < len(src) {
		encoded := src[n] + 42
		if encoded == 0 || encoded == 13 || encoded == 10 || encoded == 61 {
			break
		}
		dst[n] = encoded
		n++
	}
	return n
}
