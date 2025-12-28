package rapidyenc

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"strconv"
)

type Response struct {
	BytesProduced int64
	BytesConsumed int64
	Lines         []string
	Format        Format
	FileName      string
	FileSize      int64 // Total size of the file
	PartNumber    int64
	Offset        int64 // Offset of the part within the file relative to the start, like io.Seeker or io.WriterAt
	PartSize      int64 // Size of the unencoded data
	EndSize       int64
	TotalParts    int64
	ExpectedCRC   uint32
	Message       string
	StatusCode    int
	CRC           uint32

	hasStatusLine bool
	state         State
	eof           bool
	body          bool
	hasPart       bool
	hasBegin      bool
	hasEnd        bool
	hasCrc        bool
	hasEmptyLine  bool // for article requests has the empty line separating headers and body been seen
	hasBadData    bool // invalid line lengths for uu decoding; some data lost
}

// Begin is the "=ypart begin" value calculated from the Offset
func (r *Response) Begin() int64 {
	return r.Offset + 1
}

// End is the "=ypart end" value calculated from the Offset and PartSize
func (r *Response) End() int64 {
	return r.Offset + r.PartSize
}

const nntpBody = 222
const nntpArtiicle = 220
const nntpHead = 221
const nntpCapabilities = 101

// Feed consumes raw NNTP protocol bytes from buf, writing any decoded payload bytes to out.
// It returns (bytesConsumedFromBuf, done, error).
func (r *Response) Feed(buf []byte, out io.Writer) (consumed int, done bool, err error) {
	if out == nil {
		out = io.Discard
	}

	n, err := r.decode(buf, out)

	if err != nil {
		return n, false, err
	}
	if r.eof {
		return n, true, r.metaError()
	}
	return n, false, nil
}

func (r *Response) metaError() error {
	if r.Format == FormatUU {
		return ErrUU
	}
	if !r.hasBegin {
		return fmt.Errorf("[rapidyenc] end of article without finding \"=ybegin\" header: %w", ErrDataMissing)
	}
	if !r.hasEnd {
		return fmt.Errorf("[rapidyenc] end of article without finding \"=yend\" trailer: %w", ErrDataCorruption)
	}
	if (!r.hasPart && r.FileSize != r.BytesProduced) || (r.hasPart && r.PartSize != r.BytesProduced) {
		return fmt.Errorf("[rapidyenc] expected size %d but got %d: %w", r.PartSize, r.BytesProduced, ErrDataCorruption)
	}
	if r.hasCrc && r.ExpectedCRC != r.CRC {
		return fmt.Errorf("[rapidyenc] expected decoded data to have CRC32 hash %#08x but got %#08x: %w", r.ExpectedCRC, r.CRC, ErrCrcMismatch)
	}
	return nil
}

func (r *Response) decode(buf []byte, out io.Writer) (read int, err error) {
	if r.body && r.Format == FormatYenc {
		n, err := r.decodeYenc(buf, out)
		if err != nil {
			return int(n), err
		}
		read += int(n)
		buf = buf[n:]
		if r.body {
			return int(n), err
		}
	}

	// Line by line processing
	if !r.body {
		var line []byte
		var found bool
		for {
			if line, buf, found = bytes.Cut(buf, []byte("\r\n")); !found {
				break
			}
			read += len(line) + 2

			if bytes.Equal(line, []byte(".")) {
				r.eof = true
				break
			}

			if r.Format == FormatUnknown {
				if r.hasStatusLine && r.StatusCode == 0 && len(line) >= 3 {
					r.Message = string(line)
					r.StatusCode, err = strconv.Atoi(string(line[:3]))
					if err != nil || !isMultiline(r.StatusCode) {
						r.eof = true
						break
					}
					continue
				}
				r.detectFormat(line)
			}

			switch r.Format {
			case FormatUnknown:
				r.Lines = append(r.Lines, string(line))
			case FormatYenc:
				r.processYencHeader(line)
				if r.body {
					n, err := r.decodeYenc(buf, out)
					read += int(n)
					buf = buf[n:]
					if err != nil {
						return read, err
					}
					if r.body {
						// Still decoding, need more data
						return read, nil
					}
					// =ypart was encountered, switch to body decoding
				}
			case FormatUU:
				// TODO: does not uudecode, for now just copies encoded data
				if _, err := io.Copy(out, bytes.NewReader(line)); err != nil {
					return read, err
				}
				if _, err := out.Write([]byte("\r\n")); err != nil {
					return read, err
				}
			}
		}
	}

	return read, nil
}

func (r *Response) detectFormat(line []byte) {
	if r.hasStatusLine && r.StatusCode != nntpBody && r.StatusCode != nntpArtiicle {
		return
	}

	if len(line) == 0 {
		r.hasEmptyLine = true
		return
	}

	// YEnc detection
	if bytes.HasPrefix(line, []byte("=ybegin ")) {
		r.Format = FormatYenc
		return
	}

	// UUEncode detection: 60 or 61 chars, starts with 'M'
	if (len(line) == 60 || len(line) == 61) && line[0] == 'M' {
		r.Format = FormatUU
		return
	}

	// UUEncode alternative header form: "begin "
	if bytes.HasPrefix(line, []byte("begin ")) {
		// Skip leading spaces
		line = bytes.TrimLeft(line[6:], " ")

		// Extract the next token (permission part)
		perms, found := bytes.CutPrefix(line, []byte(" "))
		if !found {
			return
		}

		// Check all characters are between '0' and '7'
		valid := true
		for _, c := range perms {
			if c < '0' || c > '7' {
				valid = false
				break
			}
		}

		if valid {
			r.Format = FormatUU
		}
		return
	}

	// Remove dot stuffing
	if bytes.HasPrefix(line, []byte("..")) {
		line = line[1:]
	}

	// Multipart UU with a short final part
	if len(line) <= 1 {
		return
	}

	// For Article responses only consider after the headers
	if r.hasStatusLine && !(r.StatusCode == nntpBody || (r.StatusCode == nntpArtiicle && r.hasEmptyLine)) {
		return
	}

	first := line[0]
	n := len(line)

	for _, length := range []int{
		decodeUUCharWorkaround(first),
		decodeUUChar(first),
	} {
		if n < length {
			continue
		}

		body := line[1:length]
		padding := line[length:]

		if !allInASCIIRange(body, 32, 96) || !onlySpaceOrBacktick(padding) {
			continue
		}

		// Probably UU
		r.Format = FormatUU
		r.body = true
		return
	}
}

func allInASCIIRange(b []byte, lo, hi byte) bool {
	for _, c := range b {
		if c < lo || c > hi {
			return false
		}
	}
	return true
}

func onlySpaceOrBacktick(b []byte) bool {
	for _, c := range b {
		if c != ' ' && c != '`' {
			return false
		}
	}
	return true
}

func decodeUUCharWorkaround(c byte) int {
	return int(((int(c)-32)&63)*4+5) / 3
}

func decodeUUChar(c byte) int {
	if c == '`' {
		return 0
	}
	return int((c - ' ') & 0x3F)
}

func isMultiline(code int) bool {
	return code == nntpBody || code == nntpArtiicle || code == nntpHead || code == nntpCapabilities
}

func (r *Response) decodeYenc(buf []byte, out io.Writer) (n int64, err error) {
	if len(buf) == 0 {
		return 0, nil
	}

	var produced, consumed int
	var end End

	produced, consumed, end, err = DecodeIncremental(buf, buf, &r.state)

	if produced > 0 {
		r.CRC = crc32.Update(r.CRC, crc32.IEEETable, buf[:produced])
		r.BytesProduced += int64(produced)
		if _, werr := out.Write(buf[:produced]); werr != nil {
			return n, werr
		}
	}
	n += int64(consumed)

	switch end {
	case EndNone:
		if r.state == StateCRLFEQ {
			// Special case: found "\r\n=" but no more data - might be start of =yend
			r.state = StateCRLF
			n -= 1 // Back up to allow =yend detection
		}
	case EndControl:
		// Found "\r\n=y" - likely =yend line, exit body mode
		r.body = false
		n -= 2 // Back up to include "=y" for header processing
	case EndArticle:
		// Found ".\r\n" - NNTP article terminator, exit body mode
		r.body = false
		n -= 3 // Back up to include ".\r\n" for terminator detection
	}

	return n, nil
}

func (r *Response) processYencHeader(line []byte) {
	var err error
	if bytes.HasPrefix(line, []byte("=ybegin ")) {
		r.hasBegin = true
		line = line[len("=ybegin"):]
		r.FileSize, _ = extractInt(line, []byte(" size="))
		r.FileName, _ = extractString(line, []byte(" name="))
		if r.PartNumber, err = extractInt(line, []byte(" part=")); err != nil {
			// Not multi-part, so body starts immediately after =ybegin
			r.body = true
			r.PartSize = r.FileSize
		}
		r.TotalParts, _ = extractInt(line, []byte(" total="))
	} else if bytes.HasPrefix(line, []byte("=ypart ")) {
		// =ypart signals start of body data in multi-part files
		r.hasPart = true
		r.body = true
		line = line[len("=ypart"):]
		var begin int64
		// Convert from 1-based to 0-based indexing
		if begin, err = extractInt(line, []byte(" begin=")); err == nil {
			r.Offset = begin - 1
		}
		if end, err := extractInt(line, []byte(" end=")); err == nil && end >= begin {
			r.PartSize = end - r.Offset
		}
	} else if bytes.HasPrefix(line, []byte("=yend ")) {
		r.hasEnd = true
		line = line[len("=yend"):]
		if crc, err := extractCRC(line, []byte(" pcrc32=")); err == nil {
			r.ExpectedCRC = crc
			r.hasCrc = true
		} else if crc, err := extractCRC(line, []byte(" crc32=")); err == nil {
			r.ExpectedCRC = crc
			r.hasCrc = true
		}
		r.EndSize, _ = extractInt(line, []byte(" size="))
	}
}

func extractString(data, substr []byte) (string, error) {
	start := bytes.Index(data, substr)
	if start == -1 {
		return "", fmt.Errorf("substr not found: %s", substr)
	}

	data = data[start+len(substr):]
	if end := bytes.IndexAny(data, "\x00\r\n"); end != -1 {
		return string(data[:end]), nil
	}

	return string(data), nil
}

func extractInt(data, substr []byte) (int64, error) {
	start := bytes.Index(data, substr)
	if start == -1 {
		return 0, fmt.Errorf("substr not found: %s", substr)
	}

	data = data[start+len(substr):]
	if end := bytes.IndexAny(data, "\x00\x20\r\n"); end != -1 {
		return strconv.ParseInt(string(data[:end]), 10, 64)
	}

	return strconv.ParseInt(string(data), 10, 64)
}

var (
	errCrcNotfound = errors.New("crc not found")
)

// extractCRC converts a hexadecimal representation of a crc32 hash
func extractCRC(data, substr []byte) (uint32, error) {
	start := bytes.Index(data, substr)
	if start == -1 {
		return 0, errCrcNotfound
	}

	data = data[start+len(substr):]
	end := bytes.IndexAny(data, "\x00\x20\r\n")
	if end != -1 {
		data = data[:end]
	}

	// Take up to the last 8 characters
	parsed := data[len(data)-min(8, len(data)):]

	// Left pad unexpected length with 0
	if len(parsed) != 8 {
		padded := []byte("00000000")
		copy(padded[8-len(parsed):], parsed)
		parsed = padded
	}

	_, err := hex.Decode(parsed, parsed)
	return binary.BigEndian.Uint32(parsed), err
}
