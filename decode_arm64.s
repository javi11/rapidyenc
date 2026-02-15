#include "textflag.h"

// func decodeFast(dst, src []byte) (int, int)
//
// NEON yEnc decoder: subtracts 42 from each byte in 16-byte SIMD chunks.
// Handles = escape sequences inline (consuming 2 src bytes, writing 1 dst byte).
// Stops only at \r or \n (which require Go state machine for EndControl/dot-unstuffing).
//
// Returns (nDst, nSrc) where nDst = bytes written to dst, nSrc = bytes consumed from src.
// nSrc >= nDst because = escapes consume 2 src bytes per 1 dst byte.
//
// Register allocation:
//   R0 = dst write pointer (advances)
//   R1 = src read pointer (advances)
//   R2 = remaining src bytes (decrements)
//   R4 = dst bytes written accumulator
//   R5 = src bytes consumed accumulator
//   R6-R9 = temporaries
//   V0 = data, V1-V3 = comparisons, V4 = combined mask
//   V20 = splat(\r), V21 = splat(\n), V22 = splat(=), V23 = splat(42)
TEXT ·decodeFast(SB), NOSPLIT, $0-64
	MOVD dst_base+0(FP), R0
	MOVD src_base+24(FP), R1
	MOVD src_len+32(FP), R2
	MOVD $0, R4                  // nDst = 0
	MOVD $0, R5                  // nSrc = 0

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
	ADD  $16, R5
	SUB  $16, R2
	B    simd_loop

has_specials:
	// Special byte found in this chunk.
	// Fall through to byte_tail which handles = inline and stops at \r/\n.
	B    byte_tail

byte_tail:
	// Process bytes one at a time.
	// Handle = escapes inline, stop only at \r or \n.
	CBZ  R2, done
	MOVBU (R1), R6
	CMP  $13, R6                 // \r?
	BEQ  done
	CMP  $10, R6                 // \n?
	BEQ  done
	CMP  $61, R6                 // =?
	BEQ  handle_eq

	// Normal byte: subtract 42 and store
	SUB  $42, R6, R6
	MOVB R6, (R0)
	ADD  $1, R0
	ADD  $1, R4
	ADD  $1, R1
	ADD  $1, R5
	SUB  $1, R2
	B    simd_loop               // try SIMD again after processing bytes

handle_eq:
	// = found: consume = and decode the next byte with -42-64
	CMP  $2, R2                  // need at least 2 bytes (= + escaped)
	BLT  done                    // not enough data, return to Go
	MOVBU 1(R1), R6              // load byte after =
	// If escaped byte is \r or \n, bail to Go — these are extremely rare
	// but the state machine needs to see them for proper CRLF handling.
	CMP  $13, R6
	BEQ  done
	CMP  $10, R6
	BEQ  done
	SUB  $106, R6, R6            // -42-64 = -106
	MOVB R6, (R0)
	ADD  $1, R0
	ADD  $1, R4                  // 1 dst byte written
	ADD  $2, R1                  // 2 src bytes consumed (= + escaped)
	ADD  $2, R5
	SUB  $2, R2
	B    simd_loop               // try SIMD again

done:
	MOVD R4, nDst+48(FP)
	MOVD R5, nSrc+56(FP)
	RET
