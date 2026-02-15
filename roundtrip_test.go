package rapidyenc

import (
	"bytes"
	"crypto/rand"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEncodeDecodeRoundTrip1MB(t *testing.T) {
	raw := make([]byte, 1024*1024)
	_, err := rand.Read(raw)
	require.NoError(t, err)

	w := new(bytes.Buffer)
	enc, err := NewEncoder(w, Meta{
		FileName:   "test",
		FileSize:   int64(len(raw)),
		PartSize:   int64(len(raw)),
		PartNumber: 1,
		TotalParts: 1,
	})
	require.NoError(t, err)

	_, err = io.Copy(enc, bytes.NewReader(raw))
	require.NoError(t, err)
	err = enc.Close()
	require.NoError(t, err)

	dec := NewDecoder(bytes.NewReader(w.Bytes()))
	decoded := new(bytes.Buffer)
	n, err := io.Copy(decoded, dec)
	t.Logf("Decoded %d bytes, expected %d", n, len(raw))
	require.NoError(t, err)
	require.Equal(t, int64(len(raw)), n)
}

func TestEncodeDecodeRoundTripSIMDEncodeOnly(t *testing.T) {
	oldDecode := useSIMDDecode
	useSIMDDecode = false
	defer func() { useSIMDDecode = oldDecode }()

	raw := make([]byte, 1024*1024)
	_, err := rand.Read(raw)
	require.NoError(t, err)

	w := new(bytes.Buffer)
	enc, err := NewEncoder(w, Meta{
		FileName:   "test",
		FileSize:   int64(len(raw)),
		PartSize:   int64(len(raw)),
		PartNumber: 1,
		TotalParts: 1,
	})
	require.NoError(t, err)

	_, err = io.Copy(enc, bytes.NewReader(raw))
	require.NoError(t, err)
	err = enc.Close()
	require.NoError(t, err)

	dec := NewDecoder(bytes.NewReader(w.Bytes()))
	decoded := new(bytes.Buffer)
	n, err := io.Copy(decoded, dec)
	t.Logf("SIMD encode + scalar decode: Decoded %d bytes, expected %d", n, len(raw))
	require.NoError(t, err)
	require.Equal(t, int64(len(raw)), n)
}

func TestEncodeDecodeRoundTripSIMDDecodeOnly(t *testing.T) {
	oldEncode := useSIMDEncode
	useSIMDEncode = false
	defer func() { useSIMDEncode = oldEncode }()

	raw := make([]byte, 1024*1024)
	_, err := rand.Read(raw)
	require.NoError(t, err)

	w := new(bytes.Buffer)
	enc, err := NewEncoder(w, Meta{
		FileName:   "test",
		FileSize:   int64(len(raw)),
		PartSize:   int64(len(raw)),
		PartNumber: 1,
		TotalParts: 1,
	})
	require.NoError(t, err)

	_, err = io.Copy(enc, bytes.NewReader(raw))
	require.NoError(t, err)
	err = enc.Close()
	require.NoError(t, err)

	dec := NewDecoder(bytes.NewReader(w.Bytes()))
	decoded := new(bytes.Buffer)
	n, err := io.Copy(decoded, dec)
	t.Logf("Scalar encode + SIMD decode: Decoded %d bytes, expected %d", n, len(raw))
	require.NoError(t, err)
	require.Equal(t, int64(len(raw)), n)
}

func TestEncodeDecodeRoundTripNoSIMD(t *testing.T) {
	// Temporarily disable SIMD
	oldDecode := useSIMDDecode
	oldEncode := useSIMDEncode
	useSIMDDecode = false
	useSIMDEncode = false
	defer func() {
		useSIMDDecode = oldDecode
		useSIMDEncode = oldEncode
	}()

	raw := make([]byte, 1024*1024)
	_, err := rand.Read(raw)
	require.NoError(t, err)

	w := new(bytes.Buffer)
	enc, err := NewEncoder(w, Meta{
		FileName:   "test",
		FileSize:   int64(len(raw)),
		PartSize:   int64(len(raw)),
		PartNumber: 1,
		TotalParts: 1,
	})
	require.NoError(t, err)

	_, err = io.Copy(enc, bytes.NewReader(raw))
	require.NoError(t, err)
	err = enc.Close()
	require.NoError(t, err)

	dec := NewDecoder(bytes.NewReader(w.Bytes()))
	decoded := new(bytes.Buffer)
	n, err := io.Copy(decoded, dec)
	t.Logf("No SIMD: Decoded %d bytes, expected %d", n, len(raw))
	require.NoError(t, err)
	require.Equal(t, int64(len(raw)), n)
}

func TestRawEncodeDecodeRoundTrip(t *testing.T) {
	raw := make([]byte, 1024*1024)
	_, err := rand.Read(raw)
	require.NoError(t, err)

	dst := make([]byte, MaxLength(len(raw), 128))
	encodedLen, _ := encodeGeneric(128, raw, dst, 0)
	encoded := dst[:encodedLen]

	t.Logf("Raw: %d, Encoded: %d", len(raw), encodedLen)

	// Append \r\n=yend so decoder knows when to stop
	encoded = append(encoded, "\r\n=yend size=1048576\r\n"...)

	decoded := make([]byte, len(encoded))
	var state State
	nDst, nSrc, end := decodeGeneric(decoded, encoded, &state)
	t.Logf("Decoded: %d, Consumed: %d, End: %d", nDst, nSrc, end)

	if nDst != len(raw) {
		t.Errorf("Size mismatch: decoded %d, expected %d (diff: %d)", nDst, len(raw), nDst-len(raw))

		// Find first difference
		for i := 0; i < min(nDst, len(raw)); i++ {
			if decoded[i] != raw[i] {
				t.Errorf("First diff at byte %d: decoded=0x%02x, raw=0x%02x", i, decoded[i], raw[i])
				break
			}
		}
	}
}

func TestRawEncodeDecodeSmall(t *testing.T) {
	// Test with small inputs that include all byte values
	for size := 1; size <= 512; size++ {
		raw := make([]byte, size)
		for i := range raw {
			raw[i] = byte(i)
		}

		dst := make([]byte, MaxLength(len(raw), 128))
		encodedLen, _ := encodeGeneric(128, raw, dst, 0)
		encoded := dst[:encodedLen]

		// Add end marker
		encoded = append(encoded, "\r\n=yend\r\n"...)

		decoded := make([]byte, len(encoded))
		var state State
		nDst, _, end := decodeGeneric(decoded, encoded, &state)

		if end != EndControl {
			t.Errorf("size=%d: expected EndControl, got %d", size, end)
		}
		if nDst != size {
			t.Errorf("size=%d: decoded %d bytes, expected %d", size, nDst, size)
		}
		if nDst == size {
			require.Equal(t, raw, decoded[:nDst], "size=%d", size)
		}
	}
}
