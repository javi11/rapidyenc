//go:build !cgo && goexperiment.simd

package rapidyenc

import (
	"math/bits"
	"simd/archsimd"
)

var (
	compactLUT [32768][16]byte
	decode     func(dest, src []byte, state *State) (nSrc int, decoded []byte, end End, err error)
)

func init() {
	const tableSize = 16
	for i := range compactLUT {
		k := i
		p := 0
		for j := range tableSize {
			if (k & 1) == 0 {
				compactLUT[i][p] = byte(j)
				p++
			}
			k >>= 1
		}
		for ; p < tableSize; p++ {
			compactLUT[i][p] = 0x80
		}
	}

	if archsimd.X86.AVX2() {
		decode = decodeAVX2
	} else {
		decode = decodeGeneric
	}
}

func decodeAVX2(dest, src []byte, state *State) (nSrc int, decoded []byte, end End, err error) {
	return decodeSIMD(64, dest, src, state, decodeSIMDAVX2)
}

func decodeSIMDAVX2(dest, src []byte, srcLength int, escFirst *uint8, nextMask *uint16) (consumed, produced int) {
	if len(dest) < srcLength {
		panic("slice y is shorter than slice x")
	}

	// TODO: need this?
	isRaw := true
	searchEnd := true

	neg42 := archsimd.BroadcastInt8x32(-42)

	var yencOffset archsimd.Int8x32
	if *escFirst > 0 {
		yencOffset = archsimd.LoadInt8x32(&[32]int8{
			-42 - 64, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42,
			-42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42,
		})
	} else {
		yencOffset = neg42
	}
	var minMask archsimd.Int8x32
	if nextMask != nil && isRaw {
		if *nextMask == 1 {
			minMask = archsimd.LoadInt8x32(&[32]int8{
				0, '.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.',
				'.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.',
			})
		} else if *nextMask == 2 {
			minMask = archsimd.LoadInt8x32(&[32]int8{
				'.', 0, '.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.',
				'.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.',
			})
		} else {
			minMask = archsimd.BroadcastInt8x32('.')
		}
	} else {
		minMask = archsimd.BroadcastInt8x32('.')
	}

	// set this before the loop because we can't check src after it's been overwritten
	decoderSetNextMask(isRaw, src, consumed, nextMask)

	// search for special chars
	lut := archsimd.LoadInt8x32(&[32]int8{
		// lower 128‑bit lane (elements 0..15)
		'.', -1, -1, -1, -1, -1, -1, -1, -1, -1, '\n', -1, -1, '\r', '=', -1,
		// upper 128‑bit lane (elements 16..31), same pattern
		'.', -1, -1, -1, -1, -1, -1, -1, -1, -1, '\n', -1, -1, '\r', '=', -1,
	})
	low4 := archsimd.BroadcastUint8x32(0x0f)

	for ; consumed < srcLength; consumed += 32 * 2 {
		oDataA := archsimd.LoadUint8x32Slice(src[consumed:]).AsInt8x32()
		oDataB := archsimd.LoadUint8x32Slice(src[consumed+32:]).AsInt8x32()

		// A
		idxA := oDataA.AsUint8x32().
			Min(minMask.AsUint8x32()).
			And(low4).
			AsInt8x32()
		cmpA := oDataA.Equal(lut.PermuteOrZeroGrouped(idxA))

		// B
		idxB := oDataB.AsUint8x32().
			Min(archsimd.BroadcastUint8x32('.')).
			And(low4).
			AsInt8x32()
		cmpB := oDataB.Equal(lut.PermuteOrZeroGrouped(idxB))

		// Build 64-bit mask
		mask := uint64(cmpB.ToBits())<<32 + uint64(cmpA.ToBits())

		var dataA, dataB archsimd.Int8x32
		if mask != 0 {
			cmpEqA := oDataA.Equal(archsimd.BroadcastInt8x32('='))
			cmpEqB := oDataB.Equal(archsimd.BroadcastInt8x32('='))
			maskEq := uint64(cmpEqB.ToBits())<<32 | uint64(cmpEqA.ToBits())

			var match2NlDotA archsimd.Mask8x32
			var match2NlDotB archsimd.Mask8x32
			var match2EqA archsimd.Mask8x32
			var match2EqB archsimd.Mask8x32
			var match2CrXDtA archsimd.Mask8x32
			var match2CrXDtB archsimd.Mask8x32
			var partialKillDotFound uint32

			// handle \r\n. sequences
			// RFC3977 requires the first dot on a line to be stripped, due to dot-stuffing
			if (isRaw || searchEnd) && mask != maskEq {
				tmpData2A := archsimd.LoadUint8x32Slice(src[consumed+2:]).AsInt8x32()
				tmpData2B := archsimd.LoadUint8x32SlicePart(src[consumed+2+32:]).AsInt8x32()

				if searchEnd {
					match2EqA = archsimd.BroadcastInt8x32('=').Equal(tmpData2A)
					match2EqB = archsimd.BroadcastInt8x32('=').Equal(tmpData2B)
				}
				if isRaw {
					// find patterns of \r_.
					match2CrXDtA = oDataA.Equal(archsimd.BroadcastInt8x32('\r')).And(tmpData2A.Equal(archsimd.BroadcastInt8x32('.')))
					match2CrXDtB = oDataB.Equal(archsimd.BroadcastInt8x32('\r')).And(tmpData2B.Equal(archsimd.BroadcastInt8x32('.')))
					partialKillDotFound = match2CrXDtA.Or(match2CrXDtB).ToBits()
				}

				var match1NlA archsimd.Mask8x32
				var match1NlB archsimd.Mask8x32

				if isRaw && partialKillDotFound > 0 {
					// merge matches for \r\n.
					match1LfA := archsimd.BroadcastInt8x32('\n').Equal(archsimd.LoadUint8x32Slice(src[consumed+1:]).AsInt8x32())
					match1LfB := archsimd.BroadcastInt8x32('\n').Equal(archsimd.LoadUint8x32Slice(src[consumed+1+32:]).AsInt8x32())
					// force re-computing these to avoid register spills elsewhere
					match1NlA = match1LfA.And(archsimd.BroadcastInt8x32('\r').Equal(archsimd.LoadUint8x32Slice(src[consumed:]).AsInt8x32()))
					match1NlB = match1LfB.And(archsimd.BroadcastInt8x32('\r').Equal(archsimd.LoadUint8x32Slice(src[consumed+32:]).AsInt8x32()))
					match2NlDotA = match2CrXDtA.And(match1NlA)
					match2NlDotB = match2CrXDtB.And(match1NlB)

					if searchEnd {
						tmpData4A := archsimd.LoadUint8x32Slice(src[consumed+4:]).AsInt8x32()
						tmpData4B := archsimd.LoadUint8x32Slice(src[consumed+4+32:]).AsInt8x32()
						// match instances of \r\n.\r\n and \r\n.=y
						match3CrA := archsimd.BroadcastInt8x32('\r').Equal(archsimd.LoadUint8x32Slice(src[consumed+3:]).AsInt8x32())
						match3CrB := archsimd.BroadcastInt8x32('\r').Equal(archsimd.LoadUint8x32Slice(src[consumed+3+32:]).AsInt8x32())
						match4LfA := tmpData4A.Equal(archsimd.BroadcastInt8x32('\n'))
						match4LfB := tmpData4B.Equal(archsimd.BroadcastInt8x32('\n'))
						match4EqYA := tmpData4A.Equal(archsimd.BroadcastInt16x16(0x793d).AsInt8x32()) // =y
						match4EqYB := tmpData4B.Equal(archsimd.BroadcastInt16x16(0x793d).AsInt8x32()) // =y

						var matchEnd uint32
						{
							match3EqYA := match2EqA.And(archsimd.BroadcastInt8x32('y').Equal(archsimd.LoadUint8x32Slice(src[consumed+3:]).AsInt8x32()))
							match3EqYB := match2EqB.And(archsimd.BroadcastInt8x32('y').Equal(archsimd.LoadUint8x32Slice(src[consumed+3+32:]).AsInt8x32()))
							match4EqYA = match4EqYA.ToInt8x32().AsInt16x16().ShiftAllLeft(8).AsInt8x32().ToMask()
							match4EqYB = match4EqYB.ToInt8x32().AsInt16x16().ShiftAllLeft(8).AsInt8x32().ToMask()
							// merge \r\n and =y matches for tmpData4
							match4EndA := match3CrA.And(match4LfA).Or(match4EqYA.Or(match3EqYA.ToInt8x32().AsInt16x16().ShiftAllLeft(8).AsInt8x32().ToMask()))
							match4EndB := match3CrB.And(match4LfB).Or(match4EqYB.Or(match3EqYB.ToInt8x32().AsInt16x16().ShiftAllLeft(8).AsInt8x32().ToMask()))
							// merge with \r\n.
							match4EndA = match4EndA.And(match2NlDotA)
							match4EndB = match4EndB.And(match2NlDotB)
							// match \r\n=y
							match3EndA := match3EqYA.And(match1NlA)
							match3EndB := match3EqYB.And(match1NlB)
							// combine match sequences
							matchEnd = match4EndA.Or(match3EndA).Or(match4EndB.Or(match3EndB)).ToBits()
						}

						if matchEnd > 0 {
							// terminator found
							// there's probably faster ways to do this, but reverting to scalar code should be good enough
							//consumed += consumed
							*nextMask = decoderSetNextMask2(isRaw, src, consumed, uint16(mask))
							break
						}
					}
					{
						mask |= uint64(match2NlDotA.ToBits()) << 2
						mask |= uint64(match2NlDotB.ToBits()) << 34
						match2NlDotB := match2NlDotB.ToInt8x32().GetHi().AsInt32x4().ShiftAllLeft(14).AsInt8x16().ExtendToInt16().AsInt8x32()
						minMask = archsimd.BroadcastInt8x32('.').SubSaturated(match2NlDotB)
					}
				} else if searchEnd {
					partialEndFound := false
					var match3EqYA, match3EqYB archsimd.Mask8x32
					{
						match3YA := archsimd.BroadcastInt8x32('y').Equal(archsimd.LoadUint8x32Slice(src[consumed+3:]).AsInt8x32())
						match3YB := archsimd.BroadcastInt8x32('y').Equal(archsimd.LoadUint8x32SlicePart(src[consumed+3+32:]).AsInt8x32())
						match3EqYA = match2EqA.And(match3YA)
						match3EqYB = match2EqB.And(match3YB)
						partialEndFound = match3EqYA.Or(match3EqYB).ToBits() > 0
					}
					if partialEndFound {
						endFound := false
						{
							match1LfA := archsimd.BroadcastInt8x32('\n').Equal(archsimd.LoadUint8x32Slice(src[consumed+1:]).AsInt8x32())
							match1LfB := archsimd.BroadcastInt8x32('\n').Equal(archsimd.LoadUint8x32SlicePart(src[consumed+1+32:]).AsInt8x32())
							a := match3EqYA.And(match1LfA.And(archsimd.LoadUint8x32Slice(src[consumed:]).AsInt8x32().Equal(archsimd.BroadcastInt8x32('\r'))))
							b := match3EqYB.And(match1LfB.And(archsimd.LoadUint8x32SlicePart(src[consumed+32:]).AsInt8x32().Equal(archsimd.BroadcastInt8x32('\r'))))
							endFound = a.Or(b).ToBits() > 0
						}
						if endFound {
							*nextMask = decoderSetNextMask2(isRaw, src, consumed, uint16(mask))
							break
						}
					}
					if isRaw {
						minMask = archsimd.BroadcastInt8x32('.')
					}
				} else if isRaw {
					minMask = archsimd.BroadcastInt8x32('.')
				}
			}

			maskEqShift1 := (maskEq << 1) + uint64(*escFirst)
			if mask&maskEqShift1 != 0 {
				maskEq = fixEqMask(maskEq, maskEqShift1)
				mask &= ^uint64(*escFirst)
				*escFirst = uint8(maskEq >> 63)
				// next, eliminate anything following a `=` from the special char mask; this eliminates cases of `=\r` so that they aren't removed
				maskEq <<= 1
				mask &= ^maskEq

				// unescape chars following `=`
				{
					// convert maskEq into vector form (i.e. reverse pmovmskb)
					// Wants SSE for _mm256_broadcastq_epi64(_mm_cvtsi64_si128(maskEq))
					vMaskEq := archsimd.BroadcastUint64x4(maskEq)
					vMaskEqBytes := vMaskEq.AsUint8x32()
					bitMask := archsimd.BroadcastUint64x4(0x8040201008040201).AsUint8x32()
					vMaskEqA := vMaskEqBytes.PermuteOrZeroGrouped(archsimd.LoadInt8x32(&[32]int8{
						0, 0, 0, 0, 1, 1, 1, 1,
						2, 2, 2, 2, 3, 3, 3, 3,
						0, 0, 0, 0, 1, 1, 1, 1,
						2, 2, 2, 2, 3, 3, 3, 3,
					})).And(bitMask).Equal(bitMask).ToInt8x32()
					vMaskEqB := vMaskEqBytes.PermuteOrZeroGrouped(archsimd.LoadInt8x32(&[32]int8{
						4, 4, 4, 4, 5, 5, 5, 5,
						6, 6, 6, 6, 7, 7, 7, 7,
						4, 4, 4, 4, 5, 5, 5, 5,
						6, 6, 6, 6, 7, 7, 7, 7,
					})).And(bitMask).Equal(bitMask).ToInt8x32()
					neg106 := archsimd.BroadcastInt8x32(-42 - 64)
					dataA = oDataA.Add(neg106.Merge(yencOffset, vMaskEqA.ToMask()))
					dataB = oDataB.Add(neg106.Merge(neg42, vMaskEqB.ToMask()))
				}
			} else {
				*escFirst = uint8(maskEq >> 63)

				{
					vecA := archsimd.BroadcastInt8x32(-42-64).Merge(
						yencOffset,
						cmpEqA.ToInt8x32().AsUint8x32().ConcatShiftBytesRightGrouped(
							15,
							archsimd.BroadcastInt8x32('=').SetHi(cmpEqA.ToInt8x32().GetLo()).AsUint8x32(),
						).Equal(archsimd.BroadcastUint8x32(0xff)),
					)
					vecB := archsimd.BroadcastInt8x32(-42-64).Merge(
						neg42,
						archsimd.BroadcastInt8x32('=').Equal(archsimd.LoadUint8x32Slice(src[consumed-1+32:]).AsInt8x32()),
					)
					dataA = oDataA.Add(vecA)
					dataB = oDataB.Add(vecB)
				}
			}

			if *escFirst > 0 {
				yencOffset = archsimd.LoadInt8x32(&[32]int8{
					-42 - 64, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42,
					-42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42,
				})
			} else {
				yencOffset = neg42
			}

			{
				// lookup compress masks and shuffle
				dataA = dataA.PermuteOrZeroGrouped(new(archsimd.Uint8x32).
					SetLo(archsimd.LoadUint8x16(&compactLUT[mask&0x7fff])).
					SetHi(archsimd.LoadUint8x16(&compactLUT[((mask>>12)&0x7fff0)/16])).
					AsInt8x32())
				// Store lower 128 bits
				dataA.GetLo().AsUint8x16().StoreSlice(dest[produced:])
				produced += 16 - bits.OnesCount64(mask&0xffff)
				// Store upper 128 bits
				dataA.GetHi().AsUint8x16().StoreSlice(dest[produced:])
				produced += 16 - bits.OnesCount64(mask&0xffff0000)

				mask >>= 28
				dataB = dataB.PermuteOrZeroGrouped(new(archsimd.Uint8x32).
					SetLo(archsimd.LoadUint8x16(&compactLUT[(mask&0x7fff0)/16])).
					SetHi(archsimd.LoadUint8x16(&compactLUT[((mask>>16)&0x7fff0)/16])).
					AsInt8x32())
				// Store lower 128 bits
				dataB.GetLo().AsUint8x16().StoreSlice(dest[produced:])
				produced += 16 - bits.OnesCount64(mask&0xffff0)
				// Store upper 128 bits
				dataB.GetHi().AsUint8x16().StoreSlice(dest[produced:])
				produced += 16 - bits.OnesCount64(mask>>20)
			}
		} else {
			dataA = oDataA.Add(yencOffset)
			dataB = oDataB.Add(neg42)
			dataA.AsUint8x32().StoreSlice(dest[produced:])
			dataB.AsUint8x32().StoreSlice(dest[produced+32:])
			produced += 2 * 32
			*escFirst = 0
			yencOffset = neg42
		}
	}
	return consumed, produced
}

func decoderSetNextMask(isRaw bool, src []byte, position int, nextMask *uint16) {
	if isRaw {
		if position > 0 { // have to gone through at least one loop cycle
			if position >= 2 && src[position-2] == '\r' && src[position-1] == '\n' && src[position] == '.' {
				*nextMask = 1
			} else if src[position-1] == '\r' && src[position] == '\n' && src[position+1] == '.' {
				*nextMask = 2
			} else {
				*nextMask = 0
			}
		}
	} else {
		*nextMask = 0
	}
}

// without backtracking
func decoderSetNextMask2(isRaw bool, src []byte, position int, mask uint16) uint16 {
	if isRaw {
		if src[position] == '.' {
			return mask & 1
		}
		if src[position+1] == '.' {
			return mask & 2
		}
	}
	return 0
}

// resolve invalid sequences of = to deal with cases like '===='
// bit hack inspired from simdjson: https://youtu.be/wlvKAT7SZIQ?t=33m38s
func fixEqMask(mask, maskShift1 uint64) uint64 {
	// isolate the start of each consecutive bit group (e.g. 01011101 -> 01000101)
	start := mask & ^maskShift1

	// this strategy works by firstly separating groups that start on even/odd bits
	// generally, it doesn't matter which one (even/odd) we pick, but clearing even groups specifically allows the escFirst bit in maskShift1 to work
	// (this is because the start of the escFirst group is at index -1, an odd bit, but we can't clear it due to being < 0, so we just retain all odd groups instead)

	even := uint64(0x5555555555555555) // every even bit (01010101...)

	// obtain groups which start on an odd bit (clear groups that start on an even bit, but this leaves an unwanted trailing bit)
	oddGroups := mask + (start & even)

	// clear even bits in odd groups, whilst conversely preserving even bits in even groups
	// the `& mask` also conveniently gets rid of unwanted trailing bits
	return (oddGroups ^ even) & mask
}

func decodeIncremental(dst, src []byte, state *State) (int, []byte, End, error) {
	if state == nil {
		state = new(StateCRLF)
	}

	return decode(dst, src, state)
}
