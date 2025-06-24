package rapidyenc

import (
	"golang.org/x/sys/cpu"
)

func init() {
	useAVX2 = cpu.X86.HasAVX2
}

func decodeAVX2(dest, src []byte, state *State) (nSrc int, decoded []byte, end End, err error) {
	return decodeSIMD(256, dest, src, state, decodeSIMDAVX2)
}

//go:noescape
func decodeSIMDAVX2(src []byte, dest *[]byte, escFirst *byte, nextMask *uint16)

//RapidYenc::_do_decode_raw = &do_decode_simd<true, false, sizeof(__m256i)*2, do_decode_avx2<true, false, ISA_LEVEL_AVX2> >;

// _do_decode_raw
func decodeIncremental(dst, src []byte, state *State) (int, []byte, End, error) {
	if state == nil {
		unusedState := StateCRLF
		state = &unusedState
	}

	switch {
	case useAVX2:
		maybeInitLUT()
		return decodeAVX2(dst, src, state)
		// AVX
		// SSSE3
	default:
		return decodeGeneric(true, dst, src, state)
	}
}
