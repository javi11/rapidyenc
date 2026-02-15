package rapidyenc

import (
	"bytes"
	"hash/crc32"
)

// ieeeTable is pre-initialized at package load to avoid lazy init allocations in DecodeAll.
var ieeeTable = crc32.MakeTable(crc32.IEEE)

// DecodeAll decodes an entire yEnc-encoded article from src into dst in a single call.
// This is faster than using NewDecoder/Read for cases where the full article is already
// in memory, because it avoids io.Reader overhead, interface dispatch for CRC32,
// and buffer copies from io.Copy.
//
// dst must be at least len(src) bytes. Returns the number of decoded bytes written to dst,
// the parsed metadata, and any error.
func DecodeAll(dst, src []byte) (n int, meta DecodedMeta, err error) {
	if len(src) == 0 {
		return 0, meta, ErrDataMissing
	}
	if len(dst) < len(src) {
		return 0, meta, errDestinationTooSmall
	}

	var (
		format     Format
		body       bool
		begin      bool
		part       bool
		endFound   bool
		hasCrc     bool
		state      State
		actualSize int64
		expCrc     uint32
		crcAcc     uint32 // IEEE CRC32 accumulator (no interface dispatch)
	)
	crcTab := ieeeTable

	p := src // remaining source to process

	// decodeBody decodes yEnc body data from p, advancing p and n.
	// Returns on EndControl, EndArticle, or when all data is consumed.
	decodeBody := func() error {
		for body && len(p) > 0 {
			nd, ns, endType, decErr := DecodeIncremental(dst[n:], p, &state)
			if decErr != nil {
				return decErr
			}
			crcAcc = crc32.Update(crcAcc, crcTab, dst[n:n+nd])
			n += nd
			actualSize += int64(nd)

			if endType == EndControl {
				body = false
				// Back up 2 bytes so p starts at "=yend..." (same as Decoder.decodeYenc)
				p = p[ns-2:]
				return nil
			}
			if endType == EndArticle {
				body = false
				// Back up 3 bytes for ".\r\n" (same as Decoder.decodeYenc)
				p = p[ns-3:]
				return nil
			}
			if state == StateCRLFEQ {
				// Found "\r\n=" but no more data — might be start of =yend
				state = StateCRLF
				p = p[ns-1:]
				return nil
			}
			p = p[ns:]
		}
		return nil
	}

	// If we're already in body state from a previous partial, decode first
	if body && format == FormatYenc {
		if err := decodeBody(); err != nil {
			return n, meta, err
		}
	}

	// Line-by-line header processing
	for !body {
		line, after, found := bytes.Cut(p, []byte("\r\n"))
		if !found {
			break
		}
		p = after

		if bytes.Equal(line, []byte(".")) {
			break
		}

		if format == FormatUnknown {
			format = detectFormat(line)
		}

		if format == FormatYenc {
			if bytes.HasPrefix(line, []byte("=ybegin ")) {
				begin = true
				meta.FileSize, _ = extractInt(line, []byte(" size="))
				meta.FileName, _ = extractString(line, []byte(" name="))
				if meta.PartNumber, err = extractInt(line, []byte(" part=")); err != nil {
					body = true
					meta.PartSize = meta.FileSize
				}
				meta.TotalParts, _ = extractInt(line, []byte(" total="))
			} else if bytes.HasPrefix(line, []byte("=ypart ")) {
				part = true
				body = true
				var beginVal int64
				if beginVal, err = extractInt(line, []byte(" begin=")); err == nil {
					meta.Offset = beginVal - 1
				}
				if endVal, err2 := extractInt(line, []byte(" end=")); err2 == nil && beginVal > 0 {
					meta.PartSize = endVal - meta.Offset
				}
			} else if bytes.HasPrefix(line, []byte("=yend ")) {
				endFound = true
				if part {
					if c, err2 := extractCRC(line, []byte(" pcrc32=")); err2 == nil {
						expCrc = c
						hasCrc = true
					}
				} else if c, err2 := extractCRC(line, []byte(" crc32=")); err2 == nil {
					expCrc = c
					hasCrc = true
				}
				meta.PartSize, _ = extractInt(line, []byte(" size="))
				meta.Hash = crcAcc
			}

			if body {
				if err := decodeBody(); err != nil {
					return n, meta, err
				}
			}
		} else if format == FormatUU {
			return 0, meta, ErrUU
		}
	}

	// Validate
	if !begin {
		return n, meta, ErrDataMissing
	}
	if !endFound {
		return n, meta, ErrDataCorruption
	}
	if (!part && meta.FileSize != actualSize) || (part && meta.PartSize != actualSize) {
		return n, meta, ErrDataCorruption
	}
	if hasCrc && expCrc != meta.Hash {
		return n, meta, ErrCrcMismatch
	}

	return n, meta, nil
}
