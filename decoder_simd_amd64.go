//go:build !cgo && goexperiment.simd && amd64

package rapidyenc

import (
	"encoding/hex"
	"fmt"
	"math/bits"
	"simd/archsimd"
)

func decodeAVX2(dest, src []byte, state *State) (nSrc int, decoded []byte, end End, err error) {
	return decodeSIMD(64, dest, src, state, decodeSIMDAVX2)
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

	// set this before the loop because we can't check src after it's been overwritten
	decoder_set_nextMask(isRaw, src, consumed, nextMask)

	// search for special chars
	lut := archsimd.LoadInt8x32(&[32]int8{
		// lower 128‑bit lane (elements 0..15)
		'.', -1, -1, -1, -1, -1, -1, -1, -1, -1, '\n', -1, -1, '\r', '=', -1,
		// upper 128‑bit lane (elements 16..31), same pattern
		'.', -1, -1, -1, -1, -1, -1, -1, -1, -1, '\n', -1, -1, '\r', '=', -1,
	})
	low4 := archsimd.BroadcastUint8x32(0x0f)

	for ; consumed < len(src); consumed += 32 * 2 {
		println(fmt.Sprintf("%d/%d", consumed, len(src)))
		oDataA := archsimd.LoadUint8x32SlicePart(src[consumed:]).AsInt8x32()
		oDataB := archsimd.LoadUint8x32SlicePart(src[consumed+32:]).AsInt8x32()

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
		a := toBits(cmpA)
		b := toBits(cmpB)
		mask := uint64(b)<<32 + uint64(a)

		if mask > 0 {
			println("mask", consumed, mask, fmt.Sprintf("%064b", mask))
		}

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
					partialKillDotFound = toBits(match2CrXDtA.Or(match2CrXDtB))
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
							matchEnd = toBits(match4EndA.Or(match3EndA).Or(match4EndB.Or(match3EndB)))
						}

						if matchEnd > 0 {
							// terminator found
							// there's probably faster ways to do this, but reverting to scalar code should be good enough
							//consumed += consumed
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
						match3YA := archsimd.BroadcastInt8x32('y').Equal(archsimd.LoadUint8x32Slice(src[consumed+3:]).AsInt8x32())
						match3YB := archsimd.BroadcastInt8x32('y').Equal(archsimd.LoadUint8x32SlicePart(src[consumed+3+32:]).AsInt8x32())
						match3EqYA = match2EqA.And(match3YA)
						match3EqYB = match2EqB.And(match3YB)
						partialEndFound = toBits(match3EqYA.Or(match3EqYB)) > 0
					}
					if partialEndFound {
						endFound := false
						{
							match1LfA := archsimd.BroadcastInt8x32('\n').Equal(archsimd.LoadUint8x32Slice(src[consumed+1:]).AsInt8x32())
							match1LfB := archsimd.BroadcastInt8x32('\n').Equal(archsimd.LoadUint8x32SlicePart(src[consumed+1+32:]).AsInt8x32())
							a := match3EqYA.And(match1LfA.And(archsimd.LoadUint8x32Slice(src[consumed:]).AsInt8x32().Equal(archsimd.BroadcastInt8x32('\r'))))
							b := match3EqYB.And(match1LfB.And(archsimd.LoadUint8x32SlicePart(src[consumed+32:]).AsInt8x32().Equal(archsimd.BroadcastInt8x32('\r'))))
							endFound = toBits(a.Or(b)) > 0
						}
						if endFound {
							//consumed += consumed
							*nextMask = decoder_set_nextMask2(isRaw, src, consumed, uint16(mask))
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
					vecA := archsimd.BroadcastInt8x32(-42-64).Merge(
						yencOffset,
						cmpEqA.ToInt8x32().AsUint8x32().ConcatShiftBytesRightGrouped(
							15,
							archsimd.BroadcastInt8x32('=').SetHi(cmpEqA.ToInt8x32().GetLo()).AsUint8x32(),
						).Equal(archsimd.BroadcastUint8x32(0xff)),
					)
					vecB := archsimd.BroadcastInt8x32(-42-64).Merge(
						archsimd.BroadcastInt8x32(-42),
						archsimd.BroadcastInt8x32('=').Equal(archsimd.LoadUint8x32Slice(src[consumed-1+32:]).AsInt8x32()),
					)
					dataA = oDataA.Add(vecA)
					dataB = oDataB.Add(vecB)
				}
			}

			//{
			yencOffset = makeYencOffset(*escFirst)
			//}

			//XMM_SIZE := 16
			{
				// lookup compress masks and shuffle
				lo := archsimd.LoadUint8x16(&compactLUT[mask&0x7fff])
				hi := archsimd.LoadUint8x16(&compactLUT[(mask>>12)&0x7fff])
				var shuf archsimd.Uint8x32
				shuf = shuf.SetLo(lo).SetHi(hi)
				dataA = dataA.PermuteOrZeroGrouped(shuf.AsInt8x32())
				//dataA.AsUint8x32().StoreSlice(dest[produced:])
				// Store lower 128 bits
				dataA.GetLo().AsUint8x16().StoreSlice(dest[produced:])
				nAlo := 16 - bits.OnesCount32(uint32(mask&0xffff))
				println(fmt.Sprintf("%02x", produced), nAlo, hex.EncodeToString(dest[produced:produced+16]))
				produced += nAlo
				//// Store upper 128 bits
				dataA.GetHi().AsUint8x16().StoreSlice(dest[produced:])
				nAhi := 16 - bits.OnesCount32(uint32(mask&0xffff0000))
				println(fmt.Sprintf("%02x", produced), nAhi, hex.EncodeToString(dest[produced:produced+16]))
				produced += nAhi

				mask >>= 28
				lo = archsimd.LoadUint8x16(&compactLUT[mask&0x7fff])
				hi = archsimd.LoadUint8x16(&compactLUT[(mask>>16)&0x7fff])
				shuf = shuf.SetLo(lo).SetHi(hi)
				dataB = dataB.PermuteOrZeroGrouped(shuf.AsInt8x32())
				//dataB.AsUint8x32().StoreSlice(dest[produced:])
				//produced += nB
				// Store lower 128 bits
				dataB.GetLo().AsUint8x16().StoreSlice(dest[produced:])
				nBlo := 16 - bits.OnesCount32(uint32(mask&0xffff0))
				println(fmt.Sprintf("%02x", produced), nBlo, hex.EncodeToString(dest[produced:produced+16]))
				produced += nBlo
				// Store upper 128 bits
				dataB.GetHi().AsUint8x16().StoreSlice(dest[produced:])
				nBhi := 16 - bits.OnesCount32(uint32(mask>>20))
				println(fmt.Sprintf("%02x", produced), nBhi, hex.EncodeToString(dest[produced:produced+16]))
				produced += nBhi
			}
			println("long")
		} else {
			println("short", mask, fmt.Sprintf("%02x", produced))
			// if(use_isa < ISA_LEVEL_AVX3)
			dataA = oDataA.Add(yencOffset)
			dataB = oDataB.Add(archsimd.BroadcastInt8x32(-42))
			dataA.AsUint8x32().StoreSlice(dest[produced:])
			dataB.AsUint8x32().StoreSlice(dest[produced+32:])
			produced += 2 * 32
			*escFirst = 0
			yencOffset = archsimd.BroadcastInt8x32(-42)
		}
	}
	return consumed, produced
}

func printHex32(label string, shuf archsimd.Uint8x32) {
	print(label + " ")
	var tmp [8]uint32
	shuf.AsUint32x8().Store(&tmp)
	for i := 0; i < 8; i++ {
		print(fmt.Sprintf("\\x%08x", tmp[i]))
	}
	println()
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

func makeYencOffset(escFirst uint8) archsimd.Int8x32 {
	base := archsimd.BroadcastInt8x32(-42)

	if escFirst == 0 {
		return base
	}

	// Build a vector with 0x40 in byte 0, zero elsewhere
	var tmp [32]uint8
	tmp[0] = 0x40

	return base.Xor(archsimd.LoadUint8x32(&tmp).AsInt8x32())
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
	//return decodeGeneric(dst, src, state)
	return decodeAVX2(dst, src, state)
}
