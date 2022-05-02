package vpn

import (
	"reflect"
	"testing"
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

func Test_encodeBytes(t *testing.T) {
	type args struct {
		b []byte
	}
	// TODO(bassosimone,ainghazal): add here code that ensures that the function
	// we're testing fails when passed more than 1<<16 bytes.
	tests := []struct {
		name string
		args args
		want []byte
	}{
		{"goodEncode", args{[]byte("test")}, []byte{0, 5, 116, 101, 115, 116, 0}},
		{"null", args{[]byte("")}, []byte{0, 1, 0}},
		{"zero", args{[]byte{0}}, []byte{0, 2, 0, 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := encodeBytes(tt.args.b); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("encodeBytes() = %v, want %v", got, tt.want)
			}
		})
	}
}
