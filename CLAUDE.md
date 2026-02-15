# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Git Workflow

Always push and create PRs against the fork: https://github.com/javi11/rapidyenc (remote `fork`), not the upstream repo.

## Build & Test Commands

```bash
go build ./...                              # Build
go test ./...                               # Run all tests
go test -run TestDecodeAll ./...            # Run a single test
go test -bench=. -benchmem ./...            # Run all benchmarks
go test -bench=BenchmarkDecodeAll -benchmem -count=5 ./...  # Single benchmark, 5 rounds
GOEXPERIMENT=simd go test ./...             # Test with AMD64 archsimd (Go 1.26+)
```

No C compiler needed. No Makefile. No linter configured.

## Architecture

Pure Go + hand-written SIMD assembly (ARM64 NEON / AMD64 SSE2) yEnc codec. Single flat package, no sub-packages.

### Decode path

```
DecodeAll(dst, src)           → bulk single-call API, fuses CRC32 via crc32.Update
  └─ DecodeIncremental()      → Go state machine (decode_generic.go)
       └─ decodeFast(dst,src) → SIMD asm: subtract 42, handle = escapes, stop at \r\n

NewDecoder(r) / Read(p)      → io.Reader streaming API, uses separate readBuf
  └─ same internal path
```

### Encode path

```
NewEncoder(w, meta) / Write(p) / Close()  → io.Writer streaming API, writes yEnc headers/footers
  └─ encodeGeneric(lineSize, src, dst, col) → Go encoder with SIMD fast path
       └─ encodeFast(dst,src)               → SIMD asm: add 42, stop at special bytes
```

### SIMD build variants (per arch)

| File pattern | When used |
|---|---|
| `*_arm64.go` + `*_arm64.s` | ARM64 (always NEON) |
| `*_amd64.go` + `*_amd64.s` | AMD64 default (SSE2) |
| `*_amd64_simd.go` | AMD64 with `GOEXPERIMENT=simd` (archsimd, Go 1.26+) |
| `*_noasm.go` | Fallback: non-arm64/non-amd64 platforms |

### Key files

- `types.go` — State/End/Format enums
- `meta.go` — yEnc header metadata (Meta, DecodedMeta with CRC32)
- `platform.go` — Version(), DecodeKernel(), EncodeKernel()
- `maxlength.go` — buffer size calculation for encoding
- `decode.go` — DecodeAll bulk API
- `decoder.go` — io.Reader-based streaming decoder
- `encoder.go` — io.Writer-based streaming encoder

## Critical Constraints

- **Go state machine MUST see `\r\n` sequences** — SIMD cannot strip CRLF because the Go layer needs them for EndControl (`\r\n=y`) and dot-unstuffing (`\r\n.`). This is a fundamental architectural constraint.
- **Partial SIMD stores are unsafe for in-place decoding** — VST1/MOVOU writes 16 bytes but only N are valid. Decoder uses a separate `readBuf` to avoid corrupting unread source data. DecodeAll does not have this issue (separate dst/src).
- **`decodeFast` returns `(nDst, nSrc)`** — handles `=X` escape sequences inline in assembly to avoid Go→asm call overhead per escape.
- **CRC32 is a separate memory pass** — fused via `crc32.Update(acc, ieeeTable, ...)` in DecodeAll (no Hash32 interface dispatch), but still a second pass over data.

## Assembly Conventions

- ARM64: NEON via VLD1/VST1/VCMEQ/VSUB/VADD, 16-byte chunks
- AMD64: SSE2 via MOVOU/PCMPEQB/PSUBB/PADDB, 16-byte chunks
- Both: return `(nDst, nSrc int)` to caller, fall through to `byte_tail` for remaining bytes
- UMAXV encoding (ARM64): `0x6E30A8{Rn[4:0]<<5|Rd[4:0]}` — double-check Rd vs Rn fields

## Test Data

- `testdata/logo_full.uu` — UU-encoded file (tests ErrUU detection)
- Programmatic: 1MB random data, 800KB spaces, small edge cases
- Roundtrip tests verify encode→decode consistency with CRC32 validation
