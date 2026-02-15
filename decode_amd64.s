//go:build !goexperiment.simd

#include "textflag.h"

// func decodeFast(dst, src []byte) (int, int)
//
// SSE2 yEnc decoder: subtracts 42 from each byte in 16-byte SIMD chunks.
// Handles = escape sequences inline (consuming 2 src bytes, writing 1 dst byte).
// Stops only at \r or \n (which require Go state machine for EndControl/dot-unstuffing).
//
// Returns (nDst, nSrc) where nDst = bytes written to dst, nSrc = bytes consumed from src.
// nSrc >= nDst because = escapes consume 2 src bytes per 1 dst byte.
//
// Register allocation:
//   DI = dst write pointer (advances)
//   SI = src read pointer (advances)
//   BX = remaining src bytes (decrements)
//   R10 = dst bytes written accumulator
//   R11 = src bytes consumed accumulator
//   AX, CX, DX, R8 = temporaries
//   X0 = data, X1-X3 = comparisons
//   X4 = splat(\r), X5 = splat(\n), X6 = splat(=), X7 = splat(42)
TEXT ·decodeFast(SB), NOSPLIT, $0-64
	MOVQ dst_base+0(FP), DI
	MOVQ src_base+24(FP), SI
	MOVQ src_len+32(FP), BX
	XORQ R10, R10                // nDst = 0
	XORQ R11, R11                // nSrc = 0

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
	ADDQ $16, R11
	SUBQ $16, BX
	JMP  simd_loop

has_specials:
	// Special byte found in this chunk.
	// Fall through to byte_tail which handles = inline and stops at \r/\n.
	JMP  byte_tail

byte_tail:
	// Process bytes one at a time.
	// Handle = escapes inline, stop only at \r or \n.
	TESTQ BX, BX
	JZ   done
	MOVBLZX (SI), AX
	CMPL AX, $13
	JEQ  done
	CMPL AX, $10
	JEQ  done
	CMPL AX, $61
	JEQ  handle_eq

	// Normal byte: subtract 42 and store
	SUBL $42, AX
	MOVB AL, (DI)
	INCQ DI
	INCQ R10
	INCQ SI
	INCQ R11
	DECQ BX
	JMP  simd_loop               // try SIMD again after processing bytes

handle_eq:
	// = found: consume = and decode the next byte with -42-64
	CMPQ BX, $2                  // need at least 2 bytes (= + escaped)
	JLT  done                    // not enough data, return to Go
	MOVBLZX 1(SI), AX           // load byte after =
	// If escaped byte is \r or \n, bail to Go
	CMPL AX, $13
	JEQ  done
	CMPL AX, $10
	JEQ  done
	SUBL $106, AX                // -42-64 = -106
	MOVB AL, (DI)
	INCQ DI
	INCQ R10                     // 1 dst byte written
	ADDQ $2, SI                  // 2 src bytes consumed (= + escaped)
	ADDQ $2, R11
	SUBQ $2, BX
	JMP  simd_loop               // try SIMD again

done:
	MOVQ R10, nDst+48(FP)
	MOVQ R11, nSrc+56(FP)
	RET
