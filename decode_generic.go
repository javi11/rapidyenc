package rapidyenc

// decodeGeneric is the pure Go scalar yEnc incremental decoder.
// It decodes src into dst, handling CRLF stripping, escape sequences,
// dot-unstuffing (raw/NNTP mode), and end detection (=y control, .\r\n article end).
//
// This is a faithful port of do_decode_end_scalar<true> from decoder_common.h.
func decodeGeneric(dst, src []byte, state *State) (nDst, nSrc int, end End) {
	sLen := len(src)
	if sLen == 0 {
		return 0, 0, EndNone
	}

	p := 0 // dst write offset
	i := 0 // src read offset

	// Handle state carried over from previous call
	switch *state {
	case StateCRLFEQ:
		if src[i] == 'y' {
			*state = StateNone
			return 0, 1, EndControl
		}
		// Not 'y' — fall through to EQ handling
		fallthrough
	case StateEQ:
		c := src[i]
		dst[p] = c - 42 - 64
		p++
		i++
		if c != '\r' {
			if i >= sLen {
				*state = StateNone
				goto done
			}
			goto stateHandled
		}
		if i >= sLen {
			*state = StateCR
			goto done
		}
		fallthrough
	case StateCR:
		if src[i] != '\n' {
			goto stateHandled
		}
		i++
		if i >= sLen {
			*state = StateCRLF
			goto done
		}
		fallthrough
	case StateCRLF:
		goto handleCRLF
	case StateCRLFDT:
		goto handleCRLFDT
	case StateCRLFDTCR:
		goto handleCRLFDTCR
	case StateNone:
		// no special handling
	}
	goto mainLoop

handleCRLF:
	if src[i] == '.' {
		i++
		if i >= sLen {
			*state = StateCRLFDT
			goto done
		}
		goto handleCRLFDT
	}
	if src[i] == '=' {
		i++
		if i >= sLen {
			*state = StateCRLFEQ
			goto done
		}
		if src[i] == 'y' {
			*state = StateNone
			return p, i + 1, EndControl
		}
		// escape char
		c := src[i]
		dst[p] = c - 42 - 64
		p++
		if c == '\r' {
			// don't advance i — reprocess \r for \r\n handling
			goto stateHandled
		}
		i++
		if i >= sLen {
			*state = StateNone
			goto done
		}
		goto stateHandled
	}
	goto stateHandled

handleCRLFDT:
	if src[i] == '\r' {
		i++
		if i >= sLen {
			*state = StateCRLFDTCR
			goto done
		}
		goto handleCRLFDTCR
	}
	if src[i] == '=' {
		// dot-stuffed ending: \r\n.=y
		i++
		if i >= sLen {
			*state = StateCRLFEQ
			goto done
		}
		if src[i] == 'y' {
			*state = StateNone
			return p, i + 1, EndControl
		}
		// escape char
		c := src[i]
		dst[p] = c - 42 - 64
		p++
		if c == '\r' {
			goto stateHandled
		}
		i++
		if i >= sLen {
			*state = StateNone
			goto done
		}
		goto stateHandled
	}
	// dot was consumed (dot-unstuffing), continue with data
	goto stateHandled

handleCRLFDTCR:
	if src[i] == '\n' {
		*state = StateCRLF
		return p, i + 1, EndArticle
	}
	// Not \n after \r\n.\r — the \r wasn't part of end sequence
	// Back up so \r is reprocessed (it might start a new \r\n sequence)
	i--
	goto stateHandled

stateHandled:
	// State handling complete, enter main loop

mainLoop:
	for i < sLen-2 {
		c := src[i]
		switch c {
		case '\r':
			if src[i+1] != '\n' {
				i++
				continue
			}
			if src[i+2] == '.' {
				// skip past \r\n. (dot-unstuffing)
				i += 3
				if i >= sLen {
					*state = StateCRLFDT
					goto done
				}
				// check for article end: .\r\n
				if src[i] == '\r' {
					i++
					if i >= sLen {
						*state = StateCRLFDTCR
						goto done
					}
					if src[i] == '\n' {
						*state = StateCRLF
						return p, i + 1, EndArticle
					}
					i-- // not \n, back up to reprocess
				} else if src[i] == '=' {
					// dot-stuffed control: \r\n.=y
					i++
					if i >= sLen {
						*state = StateCRLFEQ
						goto done
					}
					if src[i] == 'y' {
						*state = StateNone
						return p, i + 1, EndControl
					}
					// escape char & continue
					ec := src[i]
					dst[p] = ec - 42 - 64
					p++
					if ec == '\r' {
						continue // reprocess \r (i not advanced)
					}
					i++
					continue
				} else {
					i-- // back up: after dot, not special, reprocess
				}
				i++
				continue
			}
			if src[i+2] == '=' {
				// \r\n= — possible control
				i += 3
				if i >= sLen {
					*state = StateCRLFEQ
					goto done
				}
				if src[i] == 'y' {
					*state = StateNone
					return p, i + 1, EndControl
				}
				// escape char & continue
				ec := src[i]
				dst[p] = ec - 42 - 64
				p++
				if ec == '\r' {
					continue // reprocess \r
				}
				i++
				continue
			}
			// bare \r\n — skip both
			i++
			fallthrough
		case '\n':
			i++
			continue
		case '=':
			ec := src[i+1]
			dst[p] = ec - 42 - 64
			p++
			if ec != '\r' {
				i += 2
			} else {
				i++ // advance past '=', reprocess '\r'
			}
			continue
		default:
			// SIMD fast path: process multiple non-special bytes at once
			if useSIMDDecode {
				n := decodeFast(dst[p:], src[i:sLen-2])
				if n > 0 {
					p += n
					i += n
					continue
				}
			}
			dst[p] = c - 42
			p++
			i++
		}
	}

	// Handle last 2 bytes carefully
	*state = StateNone

	if i == sLen-2 {
		c := src[i]
		switch c {
		case '\r':
			if src[i+1] == '\n' {
				*state = StateCRLF
				i += 2
				goto done
			}
			// bare \r — skip
			i++
			// fall through to process next byte
		case '\n':
			i++
			// fall through to process final byte
		case '=':
			ec := src[i+1]
			dst[p] = ec - 42 - 64
			p++
			if ec != '\r' {
				i += 2
			} else {
				i++ // past '=', reprocess \r as final byte
			}
			goto checkFinal
		default:
			dst[p] = c - 42
			p++
			i++
		}
	}

checkFinal:
	// Process final byte
	if i == sLen-1 {
		c := src[i]
		if c != '\n' && c != '\r' && c != '=' {
			dst[p] = c - 42
			p++
		} else {
			switch c {
			case '=':
				*state = StateEQ
			case '\r':
				*state = StateCR
			default:
				*state = StateNone
			}
		}
		i++
	}

done:
	return p, i, EndNone
}
