package rapidyenc

// MaxLength returns the maximum possible length of yEnc encoded output,
// given an input of length bytes with the specified lineLength.
func MaxLength(length, lineLength int) int {
	ret := length*2 + // all characters escaped
		2 + // allocation for offset and that a newline may occur early
		64 // allocation for potential SIMD overflowing

	// add newlines, considering the possibility of all chars escaped
	if lineLength == 128 { // optimize common case
		return ret + 2*(length>>6)
	}
	return ret + 2*((length*2)/lineLength)
}
