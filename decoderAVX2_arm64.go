//go:build !cgo

package rapidyenc

func decodeARM64(dest, src []byte, state *State) (nSrc int, decoded []byte, end End, err error) {
	return decodeSIMD(256, src, dest, state, decodeSIMDARM64)
}

func decodeSIMDARM64(src []byte, dest *[]byte, escFirst *byte, nextMask *uint16, compactLUT *[32768][16]byte) {
	//fmt.Printf("decodeSIMDARM64: len(src):%d len(dest):%d escFirst:%d nextMask:%d lut:%d\n", len(src), len(*dest), *escFirst, *nextMask, len(*compactLUT))
}

//RapidYenc::_do_decode_raw = &do_decode_simd<true, false, sizeof(__m256i)*2, do_decode_avx2<true, false, ISA_LEVEL_AVX2> >;

// _do_decode_raw
func decodeIncremental(dst, src []byte, state *State) (int, []byte, End, error) {
	if state == nil {
		unusedState := StateCRLF
		state = &unusedState
	}

	switch {
	case true:
		maybeInitLUT()
		return decodeARM64(dst, src, state)
	default:
		return decodeGeneric(dst, src, state)
	}
}
