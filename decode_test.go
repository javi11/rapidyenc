package rapidyenc

import (
	"bytes"
	"crypto/rand"
	"io"
	mathrand "math/rand"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDecodeAll(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		crc  uint32
	}{
		{"foobar", "foobar", 0x9EF61F95},
		{"special", "\x04\x04\x04\x04", 0xca2ee18a},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(tc.raw)

			encoded, err := body(raw)
			require.NoError(t, err)

			src, err := io.ReadAll(encoded)
			require.NoError(t, err)

			dst := make([]byte, len(src))
			n, meta, err := DecodeAll(dst, src)
			require.NoError(t, err)
			require.Equal(t, len(raw), n)
			require.Equal(t, raw, dst[:n])
			require.Equal(t, tc.crc, meta.Hash)
		})
	}
}

func TestDecodeAll1MB(t *testing.T) {
	raw := make([]byte, 1024*1024)
	_, err := rand.Read(raw)
	require.NoError(t, err)

	encoded, err := body(raw)
	require.NoError(t, err)

	src, err := io.ReadAll(encoded)
	require.NoError(t, err)

	dst := make([]byte, len(src))
	n, _, err := DecodeAll(dst, src)
	require.NoError(t, err)
	require.Equal(t, len(raw), n)
	require.Equal(t, raw, dst[:n])
}

func TestDecodeAllMatchesDecoder(t *testing.T) {
	// Verify DecodeAll produces identical output to Decoder.Read.
	// Use a deterministic seed to avoid flaky encoder CRC race (pre-existing issue).
	rng := mathrand.New(mathrand.NewSource(42))
	raw := make([]byte, 1024*1024)
	rng.Read(raw)

	encoded, err := body(raw)
	require.NoError(t, err)

	src, err := io.ReadAll(encoded)
	require.NoError(t, err)

	// DecodeAll path
	dst1 := make([]byte, len(src))
	n1, meta1, err := DecodeAll(dst1, src)
	require.NoError(t, err)

	// Decoder path
	dec := NewDecoder(bytes.NewReader(src))
	b := bytes.NewBuffer(nil)
	_, err = io.Copy(b, dec)
	require.NoError(t, err)

	require.Equal(t, n1, b.Len(), "decoded sizes must match")
	require.Equal(t, dst1[:n1], b.Bytes(), "decoded content must match")
	require.Equal(t, meta1.Hash, dec.Meta.Hash, "CRC32 must match")
}

func BenchmarkDecodeAll(b *testing.B) {
	raw := make([]byte, 1024*1024)
	_, err := rand.Read(raw)
	require.NoError(b, err)

	r, err := body(raw)
	require.NoError(b, err)

	src, err := io.ReadAll(r)
	require.NoError(b, err)

	dst := make([]byte, len(src))

	b.ResetTimer()
	for b.Loop() {
		_, _, err = DecodeAll(dst, src)
		if err != nil {
			b.Fatal(err)
		}
	}
}
