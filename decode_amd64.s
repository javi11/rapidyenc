#include "textflag.h"

// func decodeFast(dst, src []byte) int
//
// SSE2 yEnc decoder: subtracts 42 from each byte in 16-byte SIMD chunks.
// Stops at any special byte (\r, \n, =) and returns the count of
// clean bytes decoded. Uses partial-chunk optimization: when a special
// is found mid-chunk, stores the clean prefix before it.
//
// Register allocation:
//   DI = dst write pointer (advances)
//   SI = src read pointer (advances)
//   BX = remaining src bytes (decrements)
//   R10 = bytes processed accumulator
//   AX, CX, DX, R8 = temporaries
//   X0 = data, X1-X3 = comparisons
//   X4 = splat(\r), X5 = splat(\n), X6 = splat(=), X7 = splat(42)
TEXT ·decodeFast(SB), NOSPLIT, $0-56
	MOVQ dst_base+0(FP), DI
	MOVQ src_base+24(FP), SI
	MOVQ src_len+32(FP), BX
	XORQ R10, R10                // n = 0

	// Splat constant vectors
	MOVQ $0x0D0D0D0D0D0D0D0D, DX
	MOVQ DX, X4
	PUNPCKLQDQ X4, X4
	MOVQ $0x0A0A0A0A0A0A0A0A, DX
	MOVQ DX, X5
	PUNPCKLQDQ X5, X5
	MOVQ $0x3D3D3D3D3D3D3D3D, DX
	MOVQ DX, X6
	PUNPCKLQDQ X6, X6
	MOVQ $0x2A2A2A2A2A2A2A2A, DX
	MOVQ DX, X7
	PUNPCKLQDQ X7, X7

simd_loop:
	CMPQ BX, $16
	JLT  byte_tail

	MOVOU (SI), X0               // load 16 src bytes

	// Compare against special characters
	MOVOU X0, X1
	PCMPEQB X4, X1              // X1 = (byte == \r)
	MOVOU X0, X2
	PCMPEQB X5, X2              // X2 = (byte == \n)
	MOVOU X0, X3
	PCMPEQB X6, X3              // X3 = (byte == =)

	// Combine all specials: \r | \n | =
	POR  X2, X1
	POR  X3, X1
	PMOVMSKB X1, AX
	TESTL AX, AX
	JNZ  has_specials

	// === Fast path: no specials ===
	PSUBB X7, X0
	MOVOU X0, (DI)
	ADDQ $16, DI
	ADDQ $16, SI
	ADDQ $16, R10
	SUBQ $16, BX
	JMP  simd_loop

has_specials:
	// Special byte found in this chunk.
	// Fall through to byte_tail to process bytes one at a time.
	// We do NOT store a partial 16-byte SIMD result because when
	// dst and src share the same buffer (in-place decoding), the
	// extra bytes past the clean prefix would corrupt unread source data.
	JMP  byte_tail

byte_tail:
	TESTQ BX, BX
	JZ   done
	MOVBLZX (SI), AX
	CMPL AX, $13
	JEQ  done
	CMPL AX, $10
	JEQ  done
	CMPL AX, $61
	JEQ  done

	SUBL $42, AX
	MOVB AL, (DI)
	INCQ DI
	INCQ R10
	INCQ SI
	DECQ BX
	JMP  byte_tail

done:
	MOVQ R10, ret+48(FP)
	RET
