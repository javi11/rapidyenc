//go:build !cgo && goexperiment.simd && amd64

package rapidyenc

import (
	"math/bits"
	"simd/archsimd"
	"unsafe"
)

func decodeAVX2(dest, src []byte, state *State) (nSrc int, decoded []byte, end End, err error) {
	return decodeSIMD(256, dest, src, state, decodeSIMDAVX2)
}

func decodeSIMDAVX2(dest, src []byte, escFirst *uint8, nextMask *uint16) (consumed, produced int) {
	if len(dest) < len(src) {
		panic("slice y is shorter than slice x")
	}

	// TODO: need this?
	isRaw := true
	searchEnd := true

	var yencOffset archsimd.Int8x32
	if *escFirst > 0 {
		yencOffset = archsimd.LoadInt8x32(&[32]int8{
			-42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42,
			-42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42 - 64,
		})
	} else {
		yencOffset = archsimd.BroadcastInt8x32(-42)
	}
	var minMask archsimd.Int8x32
	if nextMask != nil && isRaw {
		if *nextMask == 1 {
			minMask = archsimd.LoadInt8x32(&[32]int8{
				'.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.',
				'.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.', 0,
			})
		} else if *nextMask == 2 {
			minMask = archsimd.LoadInt8x32(&[32]int8{
				'.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.',
				'.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.', '.', 0, '.',
			})
		} else {
			minMask = archsimd.BroadcastInt8x32('.')
		}
	} else {
		minMask = archsimd.BroadcastInt8x32('.')
	}

	//decoder_set_nextMask<isRaw>(src, len, _nextMask); // set this before the loop because we can't check src after it's been overwritten
	decoder_set_nextMask(isRaw, src, consumed, nextMask)

	for i := 0; i < len(src); i += 16 * 4 {
		oDataA := archsimd.LoadUint8x32Slice(src[i:]).AsInt8x32()
		oDataB := archsimd.LoadUint8x32Slice(src[i+32:]).AsInt8x32()

		// search for special chars
		lutBytes := [32]int8{
			-1, '=', '\r', -1, -1, '\n', -1, -1, -1, -1, -1, -1, -1, -1, -1, '.',
			-1, '=', '\r', -1, -1, '\n', -1, -1, -1, -1, -1, -1, -1, -1, -1, '.',
		}
		lut := archsimd.LoadInt8x32(&lutBytes)

		// Clamp data and shuffle
		indicesA := oDataA.Min(minMask)
		indicesB := oDataB.Min(archsimd.BroadcastInt8x32('.'))
		shufA := lut.PermuteOrZeroGrouped(indicesA)
		shufB := lut.PermuteOrZeroGrouped(indicesB)

		// Compare for equality
		cmpA := oDataA.Equal(shufA)
		cmpB := oDataB.Equal(shufB)

		// Build 64-bit mask
		maskA := toBits(cmpA)
		maskB := toBits(cmpB)
		mask := (uint64(maskB) << 32) | uint64(maskA)

		var dataA, dataB archsimd.Int8x32
		if mask != 0 {
			cmpEqA := oDataA.Equal(archsimd.BroadcastInt8x32('='))
			cmpEqB := oDataB.Equal(archsimd.BroadcastInt8x32('='))
			maskEq := uint64(toBits(cmpEqB))<<32 | uint64(toBits(cmpEqA))

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
				//# define SHIFT_DATA_A(offs) _mm256_loadu_si256((__m256i *)(src+i+offs))
				//# define SHIFT_DATA_B(offs) _mm256_loadu_si256((__m256i *)(src+i+offs) + 1)

				tmpData2A := archsimd.LoadUint8x32Slice(src[i+2*16:]).AsInt8x32()
				tmpData2B := archsimd.LoadUint8x32Slice(src[i+2*16+1:]).AsInt8x32()

				if searchEnd {
					match2EqA = archsimd.BroadcastInt8x32('=').Equal(tmpData2A)
					match2EqB = archsimd.BroadcastInt8x32('=').Equal(tmpData2B)
				}
				match2CrXDtA = oDataA.Equal(archsimd.BroadcastInt8x32('\r')).And(tmpData2A.Equal(archsimd.BroadcastInt8x32('.')))
				match2CrXDtB = oDataB.Equal(archsimd.BroadcastInt8x32('\r')).And(tmpData2B.Equal(archsimd.BroadcastInt8x32('.')))
				partialKillDotFound = toBits(match2CrXDtA.Or(match2CrXDtB))

				var match1NlA archsimd.Mask8x32
				var match1NlB archsimd.Mask8x32

				if isRaw && partialKillDotFound > 0 {
					// merge matches for \r\n.
					match1LfA := archsimd.BroadcastInt8x32('\n').Equal(archsimd.LoadUint8x32Slice(src[i+1*16:]).AsInt8x32())
					match1LfB := archsimd.BroadcastInt8x32('\n').Equal(archsimd.LoadUint8x32Slice(src[i+2*16:]).AsInt8x32())
					// force re-computing these to avoid register spills elsewhere
					match1NlA = match1LfA.And(archsimd.BroadcastInt8x32('\r').Equal(archsimd.LoadUint8x32Slice(src[i:]).AsInt8x32()))
					match1NlB = match1LfB.And(archsimd.BroadcastInt8x32('\r').Equal(archsimd.LoadUint8x32Slice(src[i+16:]).AsInt8x32()))
					match2NlDotA = match2CrXDtA.And(match1NlA)
					match2NlDotB = match2CrXDtB.And(match1NlB)

					if searchEnd {
						tmpData4A := archsimd.LoadUint8x32Slice(src[i+4*16:]).AsInt8x32()
						tmpData4B := archsimd.LoadUint8x32Slice(src[i+4*16+1:]).AsInt8x32()
						// match instances of \r\n.\r\n and \r\n.=y
						match3CrA := tmpData4A.Equal(archsimd.BroadcastInt8x32('\n'))
						match3CrB := tmpData4B.Equal(archsimd.BroadcastInt8x32('\n'))
						match4LfA := tmpData4A.Equal(archsimd.BroadcastInt8x32('\n'))
						match4LfB := tmpData4B.Equal(archsimd.BroadcastInt8x32('\n'))
						//match4EqYA := tmpData4A.Equal(archsimd.BroadcastInt16x16(0x793d).AsInt8x32()) // =y
						//match4EqYB := tmpData4B.Equal(archsimd.BroadcastInt16x16(0x793d).AsInt8x32()) // =y

						var matchEnd uint32
						{
							match3EqYA := match2EqA.And(archsimd.BroadcastInt8x32('y').Equal(archsimd.LoadUint8x32Slice(src[i+3*16:]).AsInt8x32()))
							match3EqYB := match2EqB.And(archsimd.BroadcastInt8x32('y').Equal(archsimd.LoadUint8x32Slice(src[i+3*16+1:]).AsInt8x32()))
							match4EqYA := match3EqYA.ToInt8x32().AsInt16x16().ShiftAllLeft(8).AsInt8x32().ToMask()
							match4EqYB := match3EqYB.ToInt8x32().AsInt16x16().ShiftAllLeft(8).AsInt8x32().ToMask()
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
							matchEnd = toBits(match4EndA.Or(match3EndA).Or(match4EndB.Or(match3EndB)))
						}

						if matchEnd > 0 {
							// terminator found
							// there's probably faster ways to do this, but reverting to scalar code should be good enough
							consumed += i
							*nextMask = decoder_set_nextMask2(isRaw, src, consumed, uint16(mask))
							break
						}
					}
					{
						mask |= uint64(toBits(match2NlDotA)) << 2
						mask |= uint64(toBits(match2NlDotB)) << 34
						match2NlDotB := match2NlDotB.ToInt8x32().GetHi().AsInt32x4().ShiftAllLeft(14).AsInt8x16().ExtendToInt16().AsInt8x32()
						minMask = archsimd.BroadcastInt8x32('.').SubSaturated(match2NlDotB)
					}
				} else if searchEnd {
					partialEndFound := false
					var match3EqYA, match3EqYB archsimd.Mask8x32
					{
						match3YA := archsimd.BroadcastInt8x32('y').Equal(archsimd.LoadUint8x32Slice(src[i+3*16:]).AsInt8x32())
						match3YB := archsimd.BroadcastInt8x32('y').Equal(archsimd.LoadUint8x32Slice(src[i+3*16+1:]).AsInt8x32())
						match3EqYA = match2EqA.And(match3YA)
						match3EqYB = match2EqB.And(match3YB)
						partialEndFound = toBits(match3EqYA.Or(match3EqYB)) > 0
					}
					if partialEndFound {
						endFound := false
						{
							match1LfA := archsimd.BroadcastInt8x32('\n').Equal(archsimd.LoadUint8x32Slice(src[i+1*16:]).AsInt8x32())
							match1LfB := archsimd.BroadcastInt8x32('\n').Equal(archsimd.LoadUint8x32Slice(src[i+1*16+1:]).AsInt8x32())
							a := match3EqYA.And(match1LfA.And(archsimd.LoadUint8x32Slice(src[i:]).AsInt8x32().Equal(archsimd.BroadcastInt8x32('\r'))))
							b := match3EqYB.And(match1LfB.And(archsimd.LoadUint8x32Slice(src[i+32:]).AsInt8x32().Equal(archsimd.BroadcastInt8x32('\r'))))
							endFound = toBits(a.Or(b)) > 0
						}
						if endFound {
							consumed += i
							*nextMask = decoder_set_nextMask2(isRaw, src, consumed, uint16(mask))
						}
					}
					if isRaw {
						minMask = archsimd.BroadcastInt8x32('.')
					}
				} else if isRaw {
					minMask = archsimd.BroadcastInt8x32('.')
				}
			}

			maskEqShift1 := maskEq<<1 + uint64(*escFirst)
			if mask&maskEqShift1 != 0 {
				maskEq = fix_eqMask(maskEq, maskEqShift1)
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
					neg42 := archsimd.BroadcastInt8x32(-42)
					neg106 := archsimd.BroadcastInt8x32(-42 - 64)
					dataA = oDataA.Add(neg106.Merge(yencOffset, vMaskEqA.ToMask()))
					dataB = oDataB.Add(neg106.Merge(neg42, vMaskEqB.ToMask()))
				}
			} else {
				*escFirst = uint8(maskEq >> 63)

				{
					// Does not seem to be an equivalent for _mm256_alignr_epi8
					// Broadcast the '=' byte
					eqVec := archsimd.BroadcastInt8x32('=')

					// Shift cmpEqA left by 1 byte, pulling in '=' at the low position
					cmpEqA = cmpEqA.ToInt8x32().AsInt16x16().ShiftAllLeftConcat(1, eqVec.AsInt16x16()).AsInt8x32().ToMask()

					// Load 32 bytes from src[i-1]
					loaded := archsimd.LoadInt8x32((*[32]int8)(unsafe.Pointer(&src[i-1])))

					// Compare loaded bytes with '='
					cmpEqB := loaded.Equal(eqVec)

					// Blend + add for dataA
					neg106 := archsimd.BroadcastInt8x32(-42 - 64)
					dataA = oDataA.Add(
						neg106.Merge(yencOffset, cmpEqA),
					)

					// Blend + add for dataB
					neg42 := archsimd.BroadcastInt8x32(-42)
					dataB = oDataB.Add(
						neg106.Merge(neg42, cmpEqB),
					)
				}
			}

			{
				// subtract 64 from first element if escFirst == 1
				// Step 1: lower 16-bit lane with escFirst
				// Step 2: shift each 16-bit element left by 6 bits
				tmp128 := archsimd.BroadcastInt16x8(int16(*escFirst)).ShiftAllLeft(6)

				// Step 3: zero-extend to 256-bit vector
				tmp256 := tmp128.ExtendLo2ToInt64x2().AsInt16x8().Broadcast256().AsInt8x32()

				// Step 4: broadcast -42
				neg42 := archsimd.BroadcastInt8x32(-42)

				// Step 5: XOR
				yencOffset = neg42.Xor(tmp256)
			}

			XMM_SIZE := 16
			{
				// lookup compress masks and shuffle
				lo := archsimd.LoadUint8x16(&compactLUT[mask&0x7fff])
				hi := archsimd.LoadUint8x16(&compactLUT[(mask>>12)&0x7fff0])
				var shuf archsimd.Uint8x32
				shuf.SetLo(lo).SetHi(hi)
				dataA = dataA.PermuteOrZeroGrouped(shuf.AsInt8x32())
				// Store lower 128 bits
				dataA.GetLo().AsUint8x16().StoreSlice(dest)
				consumed += bits.OnesCount32(uint32(mask & 0xffff))
				// Store upper 128 bits
				dataA.GetHi().AsUint8x16().StoreSlice(dest[XMM_SIZE:])
				consumed += bits.OnesCount32(uint32((mask & 0xffff0000) >> 16))

				lo = archsimd.LoadUint8x16(&compactLUT[mask&0x7fff0])
				hi = archsimd.LoadUint8x16(&compactLUT[(mask>>16)&0x7fff0])
				shuf.SetLo(lo).SetHi(hi)
				dataB = dataB.PermuteOrZeroGrouped(shuf.AsInt8x32())
				// Store lower 128 bits
				dataB.GetLo().AsUint8x16().StoreSlice(dest[2*XMM_SIZE:]) // wrong
				consumed += bits.OnesCount32(uint32(mask & 0xffff0))
				// Store upper 128 bits
				dataB.GetHi().AsUint8x16().StoreSlice(dest[3*XMM_SIZE:]) // wrong
				consumed += bits.OnesCount32(uint32(mask >> 20))
			}
			//consumed += XMM_SIZE*4
		} else {
			// if(use_isa < ISA_LEVEL_AVX3)
			dataA = oDataA.Add(yencOffset)
			dataB = oDataA.Add(archsimd.BroadcastInt8x32(-42))

			dataA.AsUint8x32().StoreSlice(dest)
			dataB.AsUint8x32().StoreSlice(dest[2*16:]) //wrong
			consumed += 4 * 16
			produced += 4 * 16
			dest = dest[4*16:]
			*escFirst = 0
			yencOffset = archsimd.BroadcastInt8x32(-42)
		}
	}
	return consumed, produced
}

// toBits extract MSB of each int8 and place in corresponding bit
func toBits(mask archsimd.Mask8x32) uint32 {
	var tmp [32]int8
	mask.ToInt8x32().Store(&tmp)
	var bits uint32 = 0
	for i := 0; i < 32; i++ {
		bits |= (uint32(tmp[i]) >> 7 & 1) << i
	}
	return bits
}

func decoder_set_nextMask(isRaw bool, src []byte, position int, nextMask *uint16) {
	if isRaw {
		if position > 0 { // have to gone through at least one loop cycle
			if position >= 2 && src[position-2] == '\r' && src[position-1] == '\n' && src[position] == '.' {
				*nextMask = 1
			} else if len(src) > position && src[position-1] == '\r' && src[position] == '\n' && src[position+1] == '.' {
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
func decoder_set_nextMask2(isRaw bool, src []byte, position int, mask uint16) uint16 {
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
func fix_eqMask(mask, maskShift1 uint64) uint64 {
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

// _do_decode_raw
func decodeIncremental(dst, src []byte, state *State) (int, []byte, End, error) {
	if state == nil {
		unusedState := StateCRLF
		state = &unusedState
	}

	maybeInitLUT()
	return decodeAVX2(dst, src, state)
}
