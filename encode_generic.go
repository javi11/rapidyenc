package rapidyenc

// escapeLUT maps each byte to its encoded form (byte+42), or 0 if the byte needs escaping.
// "Needs escaping" means the encoded form is NUL, CR, LF, TAB, SPACE, '=', or '.'.
var escapeLUT [256]byte

// escapedLUT maps each byte to its 2-byte escaped sequence (packed as uint16 little-endian: '=' | (byte+42+64)<<8).
// Non-zero only for bytes that need escaping.
var escapedLUT [256]uint16

func init() {
	for n := 0; n < 256; n++ {
		encoded := byte((n + 42) & 0xFF)
		needsEscape := encoded == 0 || encoded == '\r' || encoded == '\n' || encoded == '='
		if needsEscape {
			escapeLUT[n] = 0
		} else {
			escapeLUT[n] = encoded
		}

		needsEscapedEntry := needsEscape || encoded == '\t' || encoded == ' ' || encoded == '.'
		if needsEscapedEntry {
			escaped := byte((n + 42 + 64) & 0xFF)
			escapedLUT[n] = uint16('=') | uint16(escaped)<<8
		}
	}
}

// encodeGeneric is the pure Go scalar yEnc encoder.
// It encodes src into dst (which must be large enough — use MaxLength to compute size).
// lineSize is the target line length (commonly 128).
// col is the current column position (for multi-call encoding); pass 0 for new lines.
// Returns the number of bytes written to dst and the updated column position.
func encodeGeneric(lineSize int, src, dst []byte, col int) (int, int) {
	if len(src) == 0 {
		return 0, col
	}

	p := 0    // destination offset
	i := 0    // source offset
	var c byte // current source byte

	if col == 0 {
		// First character of first line
		c = src[i]
		i++
		if escapedLUT[c] != 0 {
			dst[p] = byte(escapedLUT[c])
			dst[p+1] = byte(escapedLUT[c] >> 8)
			p += 2
			col = 2
		} else {
			dst[p] = c + 42
			p++
			col = 1
		}
	}

	for i < len(src) {
		// Main line body
		for col < lineSize-1 && i < len(src) {
			// SIMD fast path: encode multiple non-escaped bytes at once
			if useSIMDEncode && col+16 <= lineSize-1 && len(src)-i >= 16 {
				n := encodeFast(dst[p:], src[i:])
				if n > 0 {
					p += n
					i += n
					col += n
					continue
				}
			}
			c = src[i]
			i++
			escaped := escapeLUT[c]
			if escaped != 0 {
				dst[p] = escaped
				p++
				col++
			} else {
				e := escapedLUT[c]
				dst[p] = byte(e)
				dst[p+1] = byte(e >> 8)
				p += 2
				col += 2
			}
		}

		if i >= len(src) {
			break
		}

		// Last character on line
		if col < lineSize {
			c = src[i]
			i++
			if escapedLUT[c] != 0 && c != '.'-42 {
				e := escapedLUT[c]
				dst[p] = byte(e)
				dst[p+1] = byte(e >> 8)
				p += 2
			} else {
				dst[p] = c + 42
				p++
			}
		}

		if i >= len(src) {
			break
		}

		// First character of next line (after CRLF)
		c = src[i]
		i++
		if escapedLUT[c] != 0 {
			dst[p] = '\r'
			dst[p+1] = '\n'
			e := escapedLUT[c]
			dst[p+2] = byte(e)
			dst[p+3] = byte(e >> 8)
			p += 4
			col = 2
		} else {
			dst[p] = '\r'
			dst[p+1] = '\n'
			dst[p+2] = c + 42
			p += 3
			col = 1
		}
	}

	return p, col
}
