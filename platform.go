package rapidyenc

import "runtime"

// Version returns the version of the rapidyenc library.
func Version() string {
	return "2.0.0"
}

// DecodeKernel returns the name of the implementation being used for decode operations.
func DecodeKernel() string {
	if useSIMDDecode {
		return archKernel()
	}
	return "generic"
}

// EncodeKernel returns the name of the implementation being used for encode operations.
func EncodeKernel() string {
	if useSIMDEncode {
		return archKernel()
	}
	return "generic"
}

func archKernel() string {
	switch runtime.GOARCH {
	case "arm64":
		return "NEON"
	case "amd64":
		return "SSE2"
	default:
		return "generic"
	}
}
