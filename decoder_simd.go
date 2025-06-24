//go:build !cgo

package rapidyenc

import (
	"sync"
	"unsafe"
)

var (
	compactLUT [32768][16]byte
	initLUT    sync.Once
)

func maybeInitLUT() {
	initLUT.Do(func() {
		const tableSize = 16
		for i := range compactLUT {
			k := i
			p := 0
			for j := range tableSize {
				if (k & 1) == 0 {
					compactLUT[i][p] = byte(j)
					p++
				}
				k >>= 1
			}
			for ; p < tableSize; p++ {
				compactLUT[i][p] = 0x80
			}
		}
	})
}

// RapidYenc::_do_decode_raw = &do_decode_simd<true, false, sizeof(__m256i)*2, do_decode_avx2<true, false, ISA_LEVEL_AVX2> >;

// func decodeSIMD(dest, src []byte, state *State) (nSrc int, decoded []byte, end End, err error) {
func decodeSIMD(
	width int,
	src []byte,
	dest []byte,
	state *State,
	kernel func(src []byte, dest *[]byte, escFirst *byte, nextMask *uint16, compactLUT *[32768][16]byte),
) (nSrc int, decoded []byte, end End, err error) {
	const isRaw = true
	length := len(src)

	if len(src) <= width*2 {
		return decodeGeneric(dest, src, state)
	}

	tState := StateCRLF
	pState := state
	if pState == nil {
		pState = &tState
	}

	if uintptr(unsafe.Pointer(&src[0]))&(uintptr(width)-1) != 0 {
		alignOffset := int(uintptr(width) - (uintptr(unsafe.Pointer(&src[0])) & uintptr(width-1)))
		length -= alignOffset
		nSrc, decoded, end, err = decodeGeneric(dest, src[:length], pState)
		if end != EndNone {
			return nSrc, decoded, end, err
		}
		//src = *srcPtr
		//dest = *destPtr
		src = src[nSrc:]
	}

	lenBuffer := width - 1
	if isRaw {
		lenBuffer += 2
	}

	if len(src) > lenBuffer {
		// Core SIMD logic
		var escFirst byte = 0
		var nextMask uint16 = 0

		switch *pState {
		case StateCRLF:
			if isRaw && src[0] == '.' {
				nextMask = 1
			}
		case StateCR:
			if isRaw && len(src) >= 2 && src[0] == '\n' && src[1] == '.' {
				nextMask = 2
			}
		case StateCRLFDT, StateCRLFDTCR, StateCRLFEQ:
			// All searchEnd only cases – skip
		}

		if *pState == StateEQ || *pState == StateCRLFEQ {
			escFirst = 1
		}

		dLen := (len(src) - lenBuffer + (width - 1)) & ^(width - 1)
		if dLen > len(src) {
			dLen = len(src)
		}

		kernel(src[:dLen], &dest, &escFirst, &nextMask, &compactLUT)

		switch {
		case escFirst == 1:
			*pState = StateEQ
		case nextMask == 1:
			*pState = StateCRLF
		case nextMask == 2:
			*pState = StateCR
		default:
			*pState = StateNone
		}

		src = src[dLen:]
		length -= dLen
		//*srcPtr = src
		//*destPtr = dest
	}

	if len(src) > 0 {
		return decodeGeneric(dest, src, pState)
	}

	return 0, dest, EndNone, nil
}
