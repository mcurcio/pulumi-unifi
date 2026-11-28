package uos

import (
	"reflect"
	"testing"
)

func TestDecodeCode(t *testing.T) {
	code := []byte{
		0x03,       // 0 iconst_0
		0x10, 0xfe, // 1 bipush -2
		0x11, 0x01, 0x00, // 3 sipush 256
		0xaa, 0, // 6 tableswitch; operands 4-byte aligned at 8
		0, 0, 0, 20, // default
		0, 0, 0, 1, // low
		0, 0, 0, 2, // high
		0, 0, 0, 10, 0, 0, 0, 12, // 2 offsets
		0xab,    // 28 lookupswitch; operands aligned at 32
		0, 0, 0, // pad
		0, 0, 0, 5, // default
		0, 0, 0, 1, // npairs
		0, 0, 0, 7, 0, 0, 0, 9,
		0xc4, 0x15, 0x01, 0x00, // 48 wide iload 256
		0xc4, 0x84, 0x00, 0x01, 0x00, 0x02, // 52 wide iinc
		0xb8, 0x00, 0x2a, // 58 invokestatic #42
		0xb9, 0x00, 0x07, 0x01, 0x00, // 61 invokeinterface #7
		0xb0, // 66 areturn
	}
	ins, err := decodeCode(code)
	if err != nil {
		t.Fatal(err)
	}
	var offs []int
	for _, in := range ins {
		offs = append(offs, in.Off)
	}
	if want := []int{0, 1, 3, 6, 28, 48, 52, 58, 61, 66}; !reflect.DeepEqual(offs, want) {
		t.Fatalf("offsets = %v, want %v", offs, want)
	}
	if ins[1].Arg != -2 || ins[2].Arg != 256 || ins[7].Arg != 42 || ins[8].Arg != 7 {
		t.Errorf("args = %+v", ins)
	}
	if _, err := decodeCode([]byte{0xb8, 0x00}); err == nil {
		t.Error("truncated instruction accepted")
	}
}
