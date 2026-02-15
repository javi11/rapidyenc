# rapidyenc

**rapidyenc** is a high-performance, **pure Go** library for encoding and decoding [yEnc](https://en.wikipedia.org/wiki/YEnc). It provides fast, memory-efficient encoding/decoding with robust error handling, supporting multiple platforms and architectures.

## 🎯 CGo Removed - Pure Go + SIMD

This fork has **completely removed CGo dependencies**, replacing them with:
- **Pure Go implementation** for portability and easier cross-compilation
- **Hand-optimized SIMD assembly** (ARM64 NEON / AMD64 SSE2) for maximum performance
- **Zero external dependencies** (except testing utilities)
- **Simpler build process** - no C compiler required!

The implementation leverages native Go code with architecture-specific SIMD optimizations, achieving ~3.4 GB/s throughput (59% of CGo performance) while maintaining Go's portability benefits and eliminating C compiler dependencies.

## Features

- **Fast yEnc encoding/decoding** using pure Go + SIMD assembly (ARM64 NEON, AMD64 SSE2)
- **No CGo required** - pure Go implementation with native SIMD optimizations
- **Streaming interface** for efficient handling of large files
- **Cross-platform:** Supports Linux, Windows, macOS on `amd64` and `arm64`
- **Header parsing:** Extracts yEnc `Meta` (filename, size, CRC32, etc)
- **Error detection:** CRC mismatch, data corruption, and missing headers

## Usage Examples

### Encoding

```go
// An io.Reader of raw data, here random data, but could be a file, bufio.Reader, etc.
raw := make([]byte, 768_000)
_, err := rand.Read(raw)
input := bytes.NewReader(raw)

// yEnc headers
meta := Meta{
    FileName:   "filename",
    FileSize:   int64(len(raw)),
    PartSize:   int64(len(raw)),
    PartNumber: 1,
    TotalParts: 1,
}

// io.Writer for output
encoded := bytes.NewBuffer(nil)

// Pass input through the Encoder
enc, err := NewEncoder(encoded, meta)
_, err = io.Copy(enc, input)

// Must close to write the =yend footer
err = enc.Close()
```

### Decoding

```go
// An io.Reader of encoded data
input := bytes.NewReader(raw)
output := bytes.NewBuffer(nil)

// Will read from input until io.EOF or ".\r\n"
dec := NewDecoder(input)
n, err := io.Copy(output, dec) // Copy decoded data to output

// if err == nil then dec.Meta contains yEnc headers
```

## Benchmarks

Performance comparison between the pure Go + SIMD implementation (this branch) vs the `cgo-baseline` branch (CGo-based):

### Decoding Performance (1MB random data)

| Implementation | ns/op | MB/s | Memory | Allocs | Relative |
|---------------|-------|------|--------|--------|----------|
| CGo baseline branch (ARM64 M4) | **180,880** | **~5,800 MB/s** | 272 B | 10 allocs/op | **1.00x** (baseline) |
| **Pure Go + SIMD** (ARM64 M4) | 307,300 | ~3,410 MB/s | 280 B | 11 allocs/op | **0.59x** |

*Benchmark run on Apple M4 (darwin/arm64) with Go 1.24.0*

**Trade-offs:**
- ✅ **Pure Go advantages**: No C compiler required, easier cross-compilation, simpler build process, better portability
- ⚠️ **Performance**: CGo version is ~1.7x faster, but pure Go still achieves respectable ~3.4 GB/s throughput
- ⚡ **Use case**: Pure Go version is ideal when build simplicity and portability outweigh the performance difference

**Performance Profile** (ARM64 M4, 1MB decode):
- `decodeFast` (SIMD assembly): **46%** - NEON SIMD inner loop (subtract 42, 16-byte chunks)
- `crc32.ieeeUpdate`: **28%** - Hardware-accelerated CRC32 calculation
- `decodeGeneric` (Go loop): **15%** - State machine for `\r\n` / escape handling
- `memmove`: **9%** - Buffer transfers via `io.Copy`

**Key Optimizations:**
- SIMD processing of 16-byte chunks per iteration (ARM64 NEON / AMD64 SSE2)
- Hardware-accelerated CRC32 calculation
- Efficient state machine for special character handling
- Zero-copy operations where possible

Run benchmarks yourself:
```bash
# Benchmark current implementation (pure Go + SIMD)
go test -bench=BenchmarkDecoder -benchmem

# Compare with CGo baseline
git checkout cgo-baseline
go test -bench=BenchmarkDecoder -benchmem
```

## Building from Source

Building is straightforward with no C compiler required:

```bash
go build
```

The package includes hand-optimized assembly for:
- ARM64: `decode_arm64.s`, `encode_arm64.s` (NEON SIMD)
- AMD64: `decode_amd64.s`, `encode_amd64.s` (SSE2 SIMD)

## Contributing

Pull requests and issues are welcome! Please open an issue for bug reports, questions, or feature requests.
