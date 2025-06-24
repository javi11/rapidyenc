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

func decodeSIMD(
		width int,
		dest []byte,
		src []byte,
		state *State,
		kernel func(dest, src []byte, escFirst *uint8, nextMask *uint16) (int, int),
) (nSrc int, decoded []byte, end End, err error) {
	const isRaw = true
	length := len(src)

	consumed := 0
	produced := 0

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
		consumed += nSrc
		produced += len(decoded)
		src = src[nSrc:]
	}

	lenBuffer := width - 1
	if isRaw {
		lenBuffer += 2
	}

	if len(src) > lenBuffer {
		// Core SIMD logic
		var escFirst uint8 = 0
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

		c, p := kernel(dest[produced:], src[:dLen], &escFirst, &nextMask)
		consumed += c
		produced += p

		switch {
		case escFirst > 0:
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
	}

	if len(src) > 0 {
		c, decoded, end, err := decodeGeneric(dest[produced:], src, pState)
		consumed += c
		produced += len(decoded)
		return consumed, dest[:produced], end, err
	}

	return consumed, dest[:produced], EndNone, nil
}
