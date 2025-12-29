# rapidyenc

**rapidyenc** is a high-performance, CGO-powered Go library for decoding [yEnc](https://en.wikipedia.org/wiki/YEnc). It provides fast, memory-efficient decoding with robust error handling, supporting multiple platforms and architectures.

The module exposes the highly efficient encoding and decoding implementations provided by the C compatible library [animetosho/rapidyenc](https://github.com/animetosho/rapidyenc) taking advantage CPU features.

## Features

- **Fast yEnc encoding/decoding** using native C implementation via CGO.
- **Streaming interface** for efficient handling of large files.
- **Cross-platform:** Supports Linux, Windows, macOS on `amd64` and `arm64`
- **Header parsing:** Extracts yEnc `Meta` (filename, size, CRC32, etc).
- **Error detection:** CRC mismatch, data corruption, and missing headers.

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
meta, err := dec.Next(output) // Writes decoded data to output

// if err == nil then meta contains yEnc headers
```

## Building from Source

It may not be desirable to use the included binary blobs, I could not find a way of avoiding it as there didn't appear to be a way to pass per-file CFLAGS when using CGO. If things have changed or there is a better way please let me know.

See [Makefile](Makefile) and [build.yml](.github/workflows/build.yml) for how the blobs are compiled.

Adding support for other platforms involves creating a `toolchain-*.cmake` file, adjust [Makefile](Makefile), compile and update [cgo.go](cgo.go)

### CGO

Unfortunate for portability reasons, for now unavoidable but there are some [interesting things happening with SIMD](https://ithub.com/golang/go/issues/73787) that give hope for the possibility of a pure Go version in the future.

## Contributing

Pull requests and issues are welcome! Please open an issue for bug reports, questions, or feature requests.
