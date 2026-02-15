//go:build !(amd64 || arm64)

package rapidyenc

// No SIMD acceleration available on this platform.
var useSIMDDecode = false

// decodeFast is a no-op stub on platforms without SIMD.
func decodeFast(dst, src []byte) int { return 0 }
