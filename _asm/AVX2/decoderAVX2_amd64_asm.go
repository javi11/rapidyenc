package main

import (
	. "github.com/mmcloughlin/avo/build"
	. "github.com/mmcloughlin/avo/operand"
	. "github.com/mmcloughlin/avo/reg"
)

//go:generate go run . -out ../../decoderAVX2_amd64.s -pkg decoder

func main() {
	Package("github.com/mnightingale/rapidyenc")
	decodeSIMDAVX2()
	Generate()
}

func _mm256_set_epi8(values [32]int8) {
	//v := YMM()

	for _, value := range values {
		tmp := GP8()
		MOVB(I8(value), tmp)
	}

}

func decodeSIMDAVX2() {
	TEXT("decodeSIMDAVX2", NOSPLIT, "func (src []byte, dest *[]byte, escFirst *byte, nextMask *uint16)")

	Load(Param("src").Base(), RAX)
	Load(Param("src").Len(), RCX)

	//escFirst := Load(Param("escFirst"), GP64())

	//escFirst := YMM()

	_mm256_set_epi8([32]int8{
		-42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42,
		-42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42 - 64,
	})

	//VecBroadcast(Imm(-42), YMM())
	//VPBROADCASTB()

	RET()
}
