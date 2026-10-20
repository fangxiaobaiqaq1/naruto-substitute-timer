//go:build amd64 && !purego

#include "textflag.h"

// NCC span kernels (see nccKernelFunc). Per span: x, y, n are int32 at
// offsets 0, 4, 8 of a 16-byte nccSpan; the template pointer advances by n.
//
// Exactness: dot and sq are accumulated in 32-bit lanes and widened to
// 64-bit lanes before any lane can overflow; sum uses PSADBW (64-bit lanes).
// A lane receives at most 2*255*255 per 16-byte block (SSE2: two PMADDWL per
// block; AVX2: one), and the pending lanes are widened once more than
// FLUSH_BLOCKS blocks would be pending, so a lane never exceeds
// (FLUSH_BLOCKS + ceil(maxDotRun/16)) * 260100 < 2^31.
//
// Tails: the last 1..15 bytes of a span are read as one 16-byte block masked
// to the span (keep16) when that load stays below img+avail; otherwise they
// are summed byte by byte. Template loads may run up to 15 bytes past a span;
// PreparedNCC.pix is padded and those bytes multiply masked (zero) pixels.

#define FLUSH_BLOCKS 4096

// keep16<>+16-r: r bytes of 0xff followed by zeros.
DATA keep16<>+0x00(SB)/8, $0xffffffffffffffff
DATA keep16<>+0x08(SB)/8, $0xffffffffffffffff
DATA keep16<>+0x10(SB)/8, $0
DATA keep16<>+0x18(SB)/8, $0
GLOBL keep16<>(SB), RODATA|NOPTR, $32

// SSE2 block: X0 = 16 image bytes, X1 = 16 template bytes. Accumulates sum
// into X8 (2 x u64), dot into X9 and sq into X10 (4 x pending u32).
#define SSE2_BLOCK \
	MOVO      X0, X2; \
	PSADBW    X15, X2; \
	PADDQ     X2, X8; \
	MOVO      X0, X3; \
	PUNPCKLBW X15, X0; \
	PUNPCKHBW X15, X3; \
	MOVO      X1, X4; \
	PUNPCKLBW X15, X1; \
	PUNPCKHBW X15, X4; \
	PMADDWL   X0, X1; \
	PMADDWL   X3, X4; \
	PADDL     X1, X9; \
	PADDL     X4, X9; \
	PMADDWL   X0, X0; \
	PMADDWL   X3, X3; \
	PADDL     X0, X10; \
	PADDL     X3, X10

// Widen pending u32 lanes X9 (dot) / X10 (sq) into X11 / X12 (2 x u64).
#define SSE2_FLUSH \
	MOVO      X9, X0; \
	PUNPCKLLQ X15, X9; \
	PUNPCKHLQ X15, X0; \
	PADDQ     X9, X11; \
	PADDQ     X0, X11; \
	MOVO      X10, X0; \
	PUNPCKLLQ X15, X10; \
	PUNPCKHLQ X15, X0; \
	PADDQ     X10, X12; \
	PADDQ     X0, X12; \
	PXOR      X9, X9; \
	PXOR      X10, X10

// func nccSpansSSE2(img *byte, stride, avail int, spans *nccSpan, nspans int, tpl *byte) (dot, sum, sq uint64)
TEXT ·nccSpansSSE2(SB), NOSPLIT, $0-72
	MOVQ img+0(FP), R8
	MOVQ stride+8(FP), R9
	MOVQ avail+16(FP), R10
	LEAQ -16(R8)(R10*1), R10 // last address a 16-byte load may start at
	MOVQ spans+24(FP), R11
	MOVQ nspans+32(FP), R12
	MOVQ tpl+40(FP), DI
	PXOR X15, X15
	PXOR X8, X8
	PXOR X9, X9
	PXOR X10, X10
	PXOR X11, X11
	PXOR X12, X12
	XORQ BX, BX              // pending blocks in X9/X10
	TESTQ R12, R12
	JEQ sse2done

sse2span:
	MOVLQSX 0(R11), AX       // x
	MOVLQSX 4(R11), SI       // y
	MOVLQSX 8(R11), DX       // n
	IMULQ R9, SI
	ADDQ AX, SI
	ADDQ R8, SI              // SI = img + y*stride + x
	LEAQ 15(DX), AX
	SHRQ $4, AX              // blocks in this span
	ADDQ AX, BX
	CMPQ BX, $FLUSH_BLOCKS
	JLS sse2blocks
	SSE2_FLUSH
	MOVQ AX, BX

sse2blocks:
	CMPQ DX, $16
	JCS sse2tail

sse2block:
	MOVOU (SI), X0
	MOVOU (DI), X1
	SSE2_BLOCK
	ADDQ $16, SI
	ADDQ $16, DI
	SUBQ $16, DX
	CMPQ DX, $16
	JCC sse2block

sse2tail:
	TESTQ DX, DX
	JEQ sse2next
	CMPQ SI, R10
	JHI sse2scalar
	MOVOU (SI), X0
	LEAQ keep16<>+16(SB), AX
	SUBQ DX, AX
	MOVOU (AX), X2
	PAND X2, X0
	MOVOU (DI), X1
	ADDQ DX, DI
	SSE2_BLOCK
	JMP sse2next

sse2scalar:
	MOVBQZX (SI), AX
	MOVBQZX (DI), CX
	MOVQ AX, X0
	PADDQ X0, X8
	IMULQ AX, CX
	MOVQ CX, X0
	PADDQ X0, X11
	IMULQ AX, AX
	MOVQ AX, X0
	PADDQ X0, X12
	INCQ SI
	INCQ DI
	DECQ DX
	JNE sse2scalar

sse2next:
	ADDQ $16, R11
	DECQ R12
	JNE sse2span

sse2done:
	SSE2_FLUSH
	PSHUFD $0x4e, X11, X0
	PADDQ X0, X11
	MOVQ X11, dot+48(FP)
	PSHUFD $0x4e, X8, X0
	PADDQ X0, X8
	MOVQ X8, sum+56(FP)
	PSHUFD $0x4e, X12, X0
	PADDQ X0, X12
	MOVQ X12, sq+64(FP)
	RET

// AVX2 block: X0 = 16 image bytes; template words loaded from tplmem.
// Accumulates sum into X8 (2 x u64), dot into Y9 and sq into Y10 (8 x
// pending u32). Clobbers Y0, Y2.
#define AVX2_BLOCK(tplmem) \
	VPSADBW   X15, X0, X2; \
	VPADDQ    X2, X8, X8; \
	VPMOVZXBW X0, Y0; \
	VPMOVZXBW tplmem, Y2; \
	VPMADDWD  Y2, Y0, Y2; \
	VPADDD    Y2, Y9, Y9; \
	VPMADDWD  Y0, Y0, Y0; \
	VPADDD    Y0, Y10, Y10

// Widen pending u32 lanes Y9 (dot) / Y10 (sq) into Y11 / Y12 (4 x u64).
#define AVX2_FLUSH \
	VPUNPCKLDQ Y15, Y9, Y0; \
	VPUNPCKHDQ Y15, Y9, Y2; \
	VPADDQ     Y0, Y11, Y11; \
	VPADDQ     Y2, Y11, Y11; \
	VPUNPCKLDQ Y15, Y10, Y0; \
	VPUNPCKHDQ Y15, Y10, Y2; \
	VPADDQ     Y0, Y12, Y12; \
	VPADDQ     Y2, Y12, Y12; \
	VPXOR      Y9, Y9, Y9; \
	VPXOR      Y10, Y10, Y10

// func nccSpansAVX2(img *byte, stride, avail int, spans *nccSpan, nspans int, tpl *byte) (dot, sum, sq uint64)
TEXT ·nccSpansAVX2(SB), NOSPLIT, $0-72
	MOVQ img+0(FP), R8
	MOVQ stride+8(FP), R9
	MOVQ avail+16(FP), R10
	LEAQ -16(R8)(R10*1), R10 // last address a 16-byte load may start at
	MOVQ spans+24(FP), R11
	MOVQ nspans+32(FP), R12
	MOVQ tpl+40(FP), DI
	VPXOR Y15, Y15, Y15
	VPXOR Y8, Y8, Y8
	VPXOR Y9, Y9, Y9
	VPXOR Y10, Y10, Y10
	VPXOR Y11, Y11, Y11
	VPXOR Y12, Y12, Y12
	XORQ BX, BX              // pending blocks in Y9/Y10
	TESTQ R12, R12
	JEQ avx2done

avx2span:
	MOVLQSX 0(R11), AX       // x
	MOVLQSX 4(R11), SI       // y
	MOVLQSX 8(R11), DX       // n
	IMULQ R9, SI
	ADDQ AX, SI
	ADDQ R8, SI              // SI = img + y*stride + x
	LEAQ 15(DX), AX
	SHRQ $4, AX              // blocks in this span
	ADDQ AX, BX
	CMPQ BX, $FLUSH_BLOCKS
	JLS avx2blocks
	AVX2_FLUSH
	MOVQ AX, BX

avx2blocks:
	CMPQ DX, $16
	JCS avx2tail

avx2block:
	VMOVDQU (SI), X0
	AVX2_BLOCK((DI))
	ADDQ $16, SI
	ADDQ $16, DI
	SUBQ $16, DX
	CMPQ DX, $16
	JCC avx2block

avx2tail:
	TESTQ DX, DX
	JEQ avx2next
	CMPQ SI, R10
	JHI avx2scalar
	LEAQ keep16<>+16(SB), AX
	SUBQ DX, AX
	VMOVDQU (SI), X0
	VPAND (AX), X0, X0
	AVX2_BLOCK((DI))
	ADDQ DX, DI
	JMP avx2next

avx2scalar:
	MOVBQZX (SI), AX
	MOVBQZX (DI), CX
	VMOVQ AX, X0
	VPADDQ X0, X8, X8
	IMULQ AX, CX
	VMOVQ CX, X0
	VPADDQ Y0, Y11, Y11
	IMULQ AX, AX
	VMOVQ AX, X0
	VPADDQ Y0, Y12, Y12
	INCQ SI
	INCQ DI
	DECQ DX
	JNE avx2scalar

avx2next:
	ADDQ $16, R11
	DECQ R12
	JNE avx2span

avx2done:
	AVX2_FLUSH
	VEXTRACTI128 $1, Y11, X0
	VPADDQ X0, X11, X11
	VPSHUFD $0x4e, X11, X0
	VPADDQ X0, X11, X11
	VMOVQ X11, dot+48(FP)
	VPSHUFD $0x4e, X8, X0
	VPADDQ X0, X8, X8
	VMOVQ X8, sum+56(FP)
	VEXTRACTI128 $1, Y12, X0
	VPADDQ X0, X12, X12
	VPSHUFD $0x4e, X12, X0
	VPADDQ X0, X12, X12
	VMOVQ X12, sq+64(FP)
	VZEROUPPER
	RET

// Luma constants for grayRowsSSE2: words (299, 587, 114, 0) per pixel, the
// +500 rounding term, the exact reciprocal M = ceil(2^32/1000) = 4294968
// (floor(n*M / 2^32) == n/1000 for every n <= 255500) and a high-dword mask.
DATA lumaw<>+0x00(SB)/8, $0x00000072024b012b
DATA lumaw<>+0x08(SB)/8, $0x00000072024b012b
GLOBL lumaw<>(SB), RODATA|NOPTR, $16
DATA luma500<>+0x00(SB)/8, $0x000001f4000001f4
DATA luma500<>+0x08(SB)/8, $0x000001f4000001f4
GLOBL luma500<>(SB), RODATA|NOPTR, $16
DATA lumam<>+0x00(SB)/8, $0x0000000000418938
DATA lumam<>+0x08(SB)/8, $0x0000000000418938
GLOBL lumam<>(SB), RODATA|NOPTR, $16
DATA lumahi<>+0x00(SB)/8, $0xffffffff00000000
DATA lumahi<>+0x08(SB)/8, $0xffffffff00000000
GLOBL lumahi<>(SB), RODATA|NOPTR, $16

// func grayRowsSSE2(dst *byte, dstStride int, src *byte, srcStride int, w4, h int)
// Writes (299R + 587G + 114B + 500) / 1000 for w4 (a positive multiple of 4)
// RGBA pixels per row, h rows: exactly ToGray's integer luma, 4 pixels per
// step. The caller checks both rectangles against their buffers.
TEXT ·grayRowsSSE2(SB), NOSPLIT, $0-48
	MOVQ dst+0(FP), DI
	MOVQ dstStride+8(FP), R8
	MOVQ src+16(FP), SI
	MOVQ srcStride+24(FP), R9
	MOVQ w4+32(FP), R10
	MOVQ h+40(FP), R11
	PXOR X15, X15
	MOVOU lumaw<>(SB), X14
	MOVOU luma500<>(SB), X13
	MOVOU lumam<>(SB), X12
	MOVOU lumahi<>(SB), X11
	TESTQ R11, R11
	JEQ graydone
	TESTQ R10, R10
	JEQ graydone

grayrow:
	MOVQ SI, AX
	MOVQ DI, BX
	MOVQ R10, CX

graypx:
	MOVOU (AX), X0           // 4 RGBA pixels
	MOVO X0, X1
	PUNPCKLBW X15, X0        // pixels 0, 1 as words
	PUNPCKHBW X15, X1        // pixels 2, 3
	PMADDWL X14, X0          // [299R0+587G0, 114B0, 299R1+587G1, 114B1]
	PMADDWL X14, X1
	MOVO X0, X2
	SHUFPS $0x88, X1, X0     // [a0 a1 a2 a3]
	SHUFPS $0xdd, X1, X2     // [b0 b1 b2 b3]
	PADDL X2, X0
	PADDL X13, X0            // n = 299R + 587G + 114B + 500
	MOVO X0, X3
	PSRLQ $32, X3            // n1, n3 in the even lanes
	PMULULQ X12, X0          // n0*M, n2*M
	PMULULQ X12, X3          // n1*M, n3*M
	PSRLQ $32, X0            // [q0 0 q2 0]
	PAND X11, X3             // [0 q1 0 q3]
	POR X3, X0
	PACKSSLW X0, X0
	PACKUSWB X0, X0
	MOVQ X0, DX
	MOVL DX, (BX)
	ADDQ $16, AX
	ADDQ $4, BX
	SUBQ $4, CX
	JNE graypx

	ADDQ R9, SI
	ADDQ R8, DI
	DECQ R11
	JNE grayrow

graydone:
	RET
