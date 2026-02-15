//go:build !(amd64 || arm64)

package rapidyenc

var useSIMDEncode = false

func encodeFast(dst, src []byte) int { return 0 }
