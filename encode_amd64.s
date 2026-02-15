#include "textflag.h"

// func encodeFast(dst, src []byte) int
//
// Adds 42 to each byte in src and stores to dst, stopping at the first byte
// whose encoded form needs escaping (NUL=0, CR=13, LF=10, '='=61).
// Returns the number of bytes processed.
TEXT ·encodeFast(SB), NOSPLIT, $0-56
	MOVQ dst_base+0(FP), DI      // dst pointer
	MOVQ src_base+24(FP), SI     // src pointer
	MOVQ src_len+32(FP), CX      // src length
	XORQ AX, AX                  // bytes processed = 0

	// Set up constant XMM registers
	// XMM4 = all 42 (add value)
	MOVQ $0x2A2A2A2A2A2A2A2A, DX
	MOVQ DX, X4
	PUNPCKLQDQ X4, X4
	// XMM5 = all 0 (NUL check)
	PXOR X5, X5
	// XMM6 = all 13 (CR)
	MOVQ $0x0D0D0D0D0D0D0D0D, DX
	MOVQ DX, X6
	PUNPCKLQDQ X6, X6
	// XMM7 = all 10 (LF)
	MOVQ $0x0A0A0A0A0A0A0A0A, DX
	MOVQ DX, X7
	PUNPCKLQDQ X7, X7
	// XMM8 = all 61 (=)
	MOVQ $0x3D3D3D3D3D3D3D3D, DX
	MOVQ DX, X8
	PUNPCKLQDQ X8, X8

simd_loop:
	CMPQ CX, $16
	JLT  byte_tail

	// Load 16 src bytes
	MOVOU (SI), X0
	// Add 42 (encoded = src + 42)
	MOVOU X0, X1
	PADDB X4, X1

	// Check for specials in encoded output
	MOVOU X1, X2
	PCMPEQB X5, X2              // X2 = (encoded == NUL)
	MOVOU X1, X3
	PCMPEQB X6, X3              // X3 = (encoded == CR)
	POR  X3, X2
	MOVOU X1, X3
	PCMPEQB X7, X3              // X3 = (encoded == LF)
	POR  X3, X2
	MOVOU X1, X3
	PCMPEQB X8, X3              // X3 = (encoded == =)
	POR  X3, X2

	// Check if any byte is non-zero
	PMOVMSKB X2, DX
	TESTL DX, DX
	JNZ  byte_tail

	// Fast path: no specials, store encoded bytes
	MOVOU X1, (DI)
	ADDQ $16, DI
	ADDQ $16, SI
	ADDQ $16, AX
	SUBQ $16, CX
	JMP  simd_loop

byte_tail:
	TESTQ CX, CX
	JZ   done
	MOVBLZX (SI), DX
	ADDL $42, DX
	ANDL $0xFF, DX               // truncate to byte
	TESTL DX, DX                 // NUL?
	JZ   done
	CMPL DX, $13                 // CR?
	JEQ  done
	CMPL DX, $10                 // LF?
	JEQ  done
	CMPL DX, $61                 // =?
	JEQ  done

	MOVB DL, (DI)
	INCQ DI
	INCQ SI
	INCQ AX
	DECQ CX
	JMP  byte_tail

done:
	MOVQ AX, ret+48(FP)
	RET
