#include "textflag.h"

// func encodeFast(dst, src []byte) int
//
// Adds 42 to each byte in src and stores to dst, stopping at the first byte
// whose encoded form needs escaping (NUL=0, CR=13, LF=10, '='=61).
// Returns the number of bytes processed.
TEXT ·encodeFast(SB), NOSPLIT, $0-56
	MOVD dst_base+0(FP), R0      // dst pointer
	MOVD src_base+24(FP), R1     // src pointer
	MOVD src_len+32(FP), R2      // src length
	MOVD $0, R3                  // bytes processed

	// Set up constant vectors
	VMOVI $42, V25.B16           // add value
	VEOR  V29.B16, V29.B16, V29.B16 // NUL (0)
	VMOVI $13, V28.B16           // CR
	VMOVI $10, V27.B16           // LF
	VMOVI $61, V26.B16           // =

simd_loop:
	CMP  $16, R2
	BLT  byte_tail

	// Load 16 src bytes and add 42
	VLD1 (R1), [V0.B16]
	VADD V25.B16, V0.B16, V1.B16    // V1 = encoded (src + 42)

	// Check for specials in encoded output
	VCMEQ V29.B16, V1.B16, V2.B16   // == NUL
	VCMEQ V28.B16, V1.B16, V3.B16   // == CR
	VCMEQ V27.B16, V1.B16, V4.B16   // == LF
	VCMEQ V26.B16, V1.B16, V5.B16   // == =
	VORR  V2.B16, V3.B16, V6.B16
	VORR  V4.B16, V5.B16, V7.B16
	VORR  V6.B16, V7.B16, V6.B16

	// Check if any byte is non-zero
	VMOV V6.D[0], R4
	VMOV V6.D[1], R5
	ORR  R4, R5, R4
	CBNZ R4, byte_tail

	// Fast path: no specials, store encoded bytes
	VST1 [V1.B16], (R0)
	ADD  $16, R0
	ADD  $16, R1
	ADD  $16, R3
	SUB  $16, R2
	B    simd_loop

byte_tail:
	// Process bytes one at a time until an escape is needed
	CBZ  R2, done
	MOVBU (R1), R4
	ADD  $42, R4, R5             // encoded = src + 42 (truncated to byte)
	AND  $0xFF, R5, R5
	CBZ  R5, done                // NUL?
	CMP  $13, R5                 // CR?
	BEQ  done
	CMP  $10, R5                 // LF?
	BEQ  done
	CMP  $61, R5                 // =?
	BEQ  done

	MOVB R5, (R0)
	ADD  $1, R0
	ADD  $1, R1
	ADD  $1, R3
	SUB  $1, R2
	B    byte_tail

done:
	MOVD R3, ret+48(FP)
	RET
