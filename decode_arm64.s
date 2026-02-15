#include "textflag.h"

// func decodeFast(dst, src []byte) int
//
// NEON yEnc decoder: subtracts 42 from each byte in 16-byte SIMD chunks.
// Stops at any special byte (\r, \n, =) and returns the count of
// clean bytes decoded. Uses partial-chunk optimization: when a special
// is found mid-chunk, stores the clean prefix before it.
//
// Register allocation:
//   R0 = dst write pointer (advances)
//   R1 = src read pointer (advances)
//   R2 = remaining src bytes (decrements)
//   R4 = bytes processed accumulator
//   R6-R8 = temporaries
//   V0 = data, V1-V4 = comparisons
//   V20 = splat(\r), V21 = splat(\n), V22 = splat(=), V23 = splat(42)
TEXT ·decodeFast(SB), NOSPLIT, $0-56
	MOVD dst_base+0(FP), R0
	MOVD src_base+24(FP), R1
	MOVD src_len+32(FP), R2
	MOVD $0, R4                  // n = 0

	// Set up constant vectors
	VMOVI $13, V20.B16           // \r
	VMOVI $10, V21.B16           // \n
	VMOVI $61, V22.B16           // =
	VMOVI $42, V23.B16           // subtract value

simd_loop:
	CMP  $16, R2
	BLT  byte_tail

	VLD1 (R1), [V0.B16]         // load 16 src bytes

	// Compare against special characters
	VCMEQ V20.B16, V0.B16, V1.B16   // V1 = (byte == \r)
	VCMEQ V21.B16, V0.B16, V2.B16   // V2 = (byte == \n)
	VCMEQ V22.B16, V0.B16, V3.B16   // V3 = (byte == =)

	// Combine all specials: \r | \n | =
	VORR V1.B16, V2.B16, V4.B16
	VORR V4.B16, V3.B16, V4.B16

	// Quick check: any specials at all?
	VMOV V4.D[0], R6
	VMOV V4.D[1], R7
	ORR  R6, R7, R8
	CBNZ R8, has_specials

	// === Fast path: no specials, subtract 42 and store all 16 ===
	VSUB V23.B16, V0.B16, V0.B16
	VST1 [V0.B16], (R0)
	ADD  $16, R0
	ADD  $16, R1
	ADD  $16, R4
	SUB  $16, R2
	B    simd_loop

has_specials:
	// Special byte found in this chunk.
	// Fall through to byte_tail to process bytes one at a time.
	// We do NOT store a partial 16-byte SIMD result because when
	// dst and src share the same buffer (in-place decoding), the
	// extra bytes past the clean prefix would corrupt unread source data.
	B    byte_tail

byte_tail:
	// Process remaining bytes one at a time, stop at any special
	CBZ  R2, done
	MOVBU (R1), R6
	CMP  $13, R6                 // \r?
	BEQ  done
	CMP  $10, R6                 // \n?
	BEQ  done
	CMP  $61, R6                 // =?
	BEQ  done

	SUB  $42, R6, R6
	MOVB R6, (R0)
	ADD  $1, R0
	ADD  $1, R4
	ADD  $1, R1
	SUB  $1, R2
	B    byte_tail

done:
	MOVD R4, ret+48(FP)
	RET
