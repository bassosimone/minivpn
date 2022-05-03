package vpn

import (
	"errors"
	"math"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func Test_genRandomBytes(t *testing.T) {
	const smallBuffer = 128
	data, err := genRandomBytes(smallBuffer)
	if err != nil {
		t.Fatal("unexpected error", err)
	}
	if len(data) != smallBuffer {
		t.Fatal("unexpected returned buffer length")
	}
}

func Test_encodeOptionStringToBytes(t *testing.T) {
	type args struct {
		s string
	}
	tests := []struct {
		name    string
		args    args
		want    []byte
		wantErr error
	}{{
		name: "common case",
		args: args{
			s: "test",
		},
		want:    []byte{0, 5, 116, 101, 115, 116, 0},
		wantErr: nil,
	}, {
		name: "encoding empty string",
		args: args{
			s: "",
		},
		want:    []byte{0, 1, 0},
		wantErr: nil,
	}, {
		name: "encoding a very large string",
		args: args{
			s: string(make([]byte, 1<<16)),
		},
		want:    nil,
		wantErr: errEncodeOption,
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := encodeOptionStringToBytes(tt.args.s)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("encodeOptionStringToBytes() error = %v, wantErr %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatal(diff)
			}
		})
	}
}

func Test_decodeOptionStringFromBytes(t *testing.T) {
	type args struct {
		b []byte
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr error
	}{{
		name: "with zero-length input",
		args: args{
			b: nil,
		},
		want:    "",
		wantErr: errDecodeOption,
	}, {
		name: "with input length equal to one",
		args: args{
			b: []byte{0x00},
		},
		want:    "",
		wantErr: errDecodeOption,
	}, {
		name: "with input length equal to two",
		args: args{
			b: []byte{0x00, 0x00},
		},
		want:    "",
		wantErr: errDecodeOption,
	}, {
		name: "with length mismatch and length < actual length",
		args: args{
			b: []byte{
				0x00, 0x03, // length = 3
				0x61, 0x61, 0x61, 0x61, 0x61, // aaaaa
				0x00, // trailing zero
			},
		},
		want:    "",
		wantErr: errDecodeOption,
	}, {
		name: "with length mismatch and length > actual length",
		args: args{
			b: []byte{
				0x00, 0x44, // length = 68
				0x61, 0x61, 0x61, 0x61, 0x61, // aaaaa
				0x00, // trailing zero
			},
		},
		want:    "",
		wantErr: errDecodeOption,
	}, {
		name: "with missing trailing \\0",
		args: args{
			b: []byte{
				0x00, 0x05, // length = 5
				0x61, 0x61, 0x61, 0x61, 0x61, // aaaaa
			},
		},
		want:    "",
		wantErr: errDecodeOption,
	}, {
		name: "with valid input",
		args: args{
			b: []byte{
				0x00, 0x06, // length = 6
				0x61, 0x61, 0x61, 0x61, 0x61, // aaaaa
				0x00, // trailing zero
			},
		},
		want:    "aaaaa",
		wantErr: nil,
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeOptionStringFromBytes(tt.args.b)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("decodeOptionStringFromBytes() error = %v, wantErr %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatal(diff)
			}
		})
	}
}

func Test_bytesUnpadPKCS7(t *testing.T) {
	type args struct {
		b         []byte
		blockSize int
	}
	tests := []struct {
		name    string
		args    args
		want    []byte
		wantErr error
	}{{
		name: "with too-large blockSize",
		args: args{
			b:         []byte{0x00, 0x00, 0x00},
			blockSize: math.MaxUint8 + 1, // too large
		},
		want:    nil,
		wantErr: errUnpaddingPKCS7,
	}, {
		name: "with zero-length array",
		args: args{
			b:         nil,
			blockSize: 2,
		},
		want:    nil,
		wantErr: errUnpaddingPKCS7,
	}, {
		name: "with 0x00 used as padding",
		args: args{
			b: []byte{
				0x61, 0x61, // block ("aa")
				0x00, 0x00, // padding
			},
			blockSize: 2,
		},
		want:    nil,
		wantErr: errUnpaddingPKCS7,
	}, {
		name: "with padding larger than block size",
		args: args{
			b: []byte{
				0x61, 0x61, // block ("aa")
				0x03, 0x03, // padding
			},
			blockSize: 2,
		},
		want:    nil,
		wantErr: errUnpaddingPKCS7,
	}, {
		name: "with blocksize == 4 and len(data) == 0",
		args: args{
			b: []byte{
				0x04, 0x04, 0x04, 0x04, // padding
			},
			blockSize: 4,
		},
		want:    []byte{},
		wantErr: nil,
	}, {
		name: "with blocksize == 4 and len(data) == 1",
		args: args{
			b: []byte{
				0xde,             // data
				0x03, 0x03, 0x03, // padding
			},
			blockSize: 4,
		},
		want:    []byte{0xde},
		wantErr: nil,
	}, {
		name: "with blocksize == 4 and len(data) == 2",
		args: args{
			b: []byte{
				0xde, 0xad, // data
				0x02, 0x02, // padding
			},
			blockSize: 4,
		},
		want:    []byte{0xde, 0xad},
		wantErr: nil,
	}, {
		name: "with blocksize == 4 and len(data) == 3",
		args: args{
			b: []byte{
				0xde, 0xad, 0xbe, // data
				0x01, // padding
			},
			blockSize: 4,
		},
		want:    []byte{0xde, 0xad, 0xbe},
		wantErr: nil,
	}, {
		name: "with blocksize == 4 and len(data) == 4",
		args: args{
			b: []byte{
				0xde, 0xad, 0xbe, 0xff, // data
				0x04, 0x04, 0x04, 0x04, // padding
			},
			blockSize: 4,
		},
		want:    []byte{0xde, 0xad, 0xbe, 0xff},
		wantErr: nil,
	}, {
		name: "with blocksize == 4 and len(data) == 5",
		args: args{
			b: []byte{
				0xde, 0xad, 0xbe, 0xff, 0xab, // data
				0x03, 0x03, 0x03, // padding
			},
			blockSize: 4,
		},
		want:    []byte{0xde, 0xad, 0xbe, 0xff, 0xab},
		wantErr: nil,
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := bytesUnpadPKCS7(tt.args.b, tt.args.blockSize)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("bytesUnpadPKCS7() error = %v, wantErr %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatal(diff)
			}
		})
	}
}

func Test_bytesPadPKCS7(t *testing.T) {
	type args struct {
		b         []byte
		blockSize int
	}
	tests := []struct {
		name    string
		args    args
		want    []byte
		wantErr error
	}{{
		name: "with too-large block size",
		args: args{
			b:         []byte{0x00, 0x00, 0x00},
			blockSize: math.MaxUint8 + 1,
		},
		want:    nil,
		wantErr: errPaddingPKCS7,
	}, {
		name: "with blockSize == 4 and len(data) == 0",
		args: args{
			b:         nil,
			blockSize: 4,
		},
		want: []byte{
			0x04, 0x04, 0x04, 0x04, // only padding
		},
		wantErr: nil,
	}, {
		name: "with blockSize == 4 and len(data) == 1",
		args: args{
			b: []byte{
				0xde, // len(data) == 1
			},
			blockSize: 4,
		},
		want: []byte{
			0xde,             // data
			0x03, 0x03, 0x03, // padding
		},
		wantErr: nil,
	}, {
		name: "with blockSize == 4 and len(data) == 2",
		args: args{
			b: []byte{
				0xde, 0xad, // len(data) == 2
			},
			blockSize: 4,
		},
		want: []byte{
			0xde, 0xad, // data
			0x02, 0x02, // padding
		},
		wantErr: nil,
	}, {
		name: "with blockSize == 4 and len(data) == 3",
		args: args{
			b: []byte{
				0xde, 0xad, 0xbe, // len(data) == 3
			},
			blockSize: 4,
		},
		want: []byte{
			0xde, 0xad, 0xbe, //data
			0x01, // padding
		},
		wantErr: nil,
	}, {
		name: "with blockSize == 4 and len(data) == 4",
		args: args{
			b: []byte{
				0xde, 0xad, 0xbe, 0xef, // len(data) == 4
			},
			blockSize: 4,
		},
		want: []byte{
			0xde, 0xad, 0xbe, 0xef, // data
			0x04, 0x04, 0x04, 0x04, // padding
		},
		wantErr: nil,
	}, {
		name: "with blocksize == 4 and len(data) == 5",
		args: args{
			b: []byte{
				0xde, 0xad, 0xbe, 0xef, 0xab, // len(data) == 5
			},
			blockSize: 4,
		},
		want: []byte{
			0xde, 0xad, 0xbe, 0xef, 0xab, // data
			0x03, 0x03, 0x03, // padding
		},
		wantErr: nil,
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := bytesPadPKCS7(tt.args.b, tt.args.blockSize)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("bytesPadPKCS7() error = %v, wantErr %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatal(diff)
			}
		})
	}
}
