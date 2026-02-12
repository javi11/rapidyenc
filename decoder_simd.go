//go:build !cgo

package rapidyenc

import (
	"bytes"
	"unsafe"
)

func decodeSIMD(
		width int,
		dest []byte,
		src []byte,
		state *State,
		kernel func(dest, src []byte, srcLength int, escFirst *uint8, nextMask *uint16) (int, int),
) (nSrc int, decoded []byte, end End, err error) {
	const isRaw = true
	const searchEnd = true
	const verbose = false
	length := len(src)

	if verbose {
		println("\nlength", length)
	}

	consumed := 0
	produced := 0

	if len(src) <= width*2 {
		if verbose {
			println(len(src), "<=", width*2)
		}
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
		if verbose {
			println("offset", alignOffset, len(src))
		}
		nSrc, decoded, end, err = decodeGeneric(dest, src[:alignOffset], pState)
		if end != EndNone {
			return nSrc, decoded, end, err
		}
		consumed += nSrc
		produced += len(decoded)
		src = src[nSrc:]
	}

	lenBuffer := width - 1
	if searchEnd {
		lenBuffer += 3
		if isRaw {
			lenBuffer += 1
		}
	} else if isRaw {
		lenBuffer += 3
	}

	if len(src) > lenBuffer {
		// Core SIMD logic
		var escFirst uint8 = 0
		var nextMask uint16 = 0

		switch *pState {
		case StateCRLF:
			if isRaw && src[0] == '.' {
				nextMask = 1
				if searchEnd && bytes.Equal(src[1:], []byte("\r\n")) {
					*pState = StateCRLF
					return 3, dest[:0], EndArticle, nil
				}
				if searchEnd && bytes.Equal(src[1:], []byte("=y")) {
					*pState = StateNone
					return 3, dest[:0], EndControl, nil
				}
			} else if searchEnd && bytes.Equal(src, []byte("=y")) {
				*pState = StateNone
				return 2, dest[:0], EndControl, nil
			}
		case StateCR:
			if isRaw && len(src) >= 2 && src[0] == '\n' && src[1] == '.' {
				nextMask = 2
				if searchEnd && bytes.Equal(src[2:], []byte("\r\n")) {
					*pState = StateCRLF
					return 4, dest[:0], EndArticle, nil
				}
				if searchEnd && bytes.Equal(src[2:], []byte("=y")) {
					*pState = StateNone
					return 4, dest[:0], EndControl, nil
				}
			} else if searchEnd && bytes.Equal(src[2:], []byte("\n=y")) {
				*pState = StateNone
				return 3, dest[:0], EndControl, nil
			}
		case StateCRLFDT:
			if searchEnd && bytes.Equal(src, []byte("\r\n")) {
				*pState = StateCRLF
				return 2, dest[:0], EndArticle, nil
			}
			if searchEnd && bytes.Equal(src, []byte("=y")) {
				*pState = StateNone
				return 2, dest[:0], EndControl, nil
			}
		case StateCRLFDTCR:
			if searchEnd && bytes.Equal(src, []byte("\n")) {
				*pState = StateCRLF
				return 1, dest[:0], EndArticle, nil
			}
		case StateCRLFEQ:
			if searchEnd && bytes.Equal(src, []byte("y")) {
				*pState = StateNone
				return 1, dest[:0], EndControl, nil
			}
		}

		if *pState == StateEQ || *pState == StateCRLFEQ {
			escFirst = 1
		}

		dLen := (len(src) - lenBuffer + (width - 1)) & ^(width - 1)
		if dLen > len(src) {
			dLen = len(src)
		}

		if verbose {
			println("kernel", dLen, "=>", consumed, produced)
		}
		c, p := kernel(dest[produced:], src, dLen, &escFirst, &nextMask)
		if verbose {
			println("kernel done", c, p)
		}
		consumed += c
		produced += p
		src = src[c:]
		length -= c

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
	}

	if len(src) > 0 {
		if verbose {
			println("generic", len(src), consumed, produced)
		}
		c, decoded, end, err := decodeGeneric(dest[produced:], src, pState)
		consumed += c
		produced += len(decoded)
		if verbose {
			println("generic done", consumed, produced)
		}
		return consumed, dest[:produced], end, err
	}

	if verbose {
		println("normal", consumed, produced)
	}
	return consumed, dest[:produced], EndNone, nil
}
