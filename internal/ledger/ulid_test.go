package ledger

import (
	"slices"
	"strings"
	"testing"
)

func TestEncodeULID(t *testing.T) {
	tests := []struct {
		name    string
		ms      int64
		entropy [ULIDEntropy]byte
		want    string
	}{
		{name: "zero", want: strings.Repeat("0", 26)},
		// The timestamp prefix of the reference vector in the ULID specification.
		{name: "timestamp prefix", ms: 1469918176385, want: "01ARYZ6S41" + strings.Repeat("0", 16)},
		{
			name:    "all bits set",
			ms:      1<<48 - 1,
			entropy: [ULIDEntropy]byte{255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
			want:    "7ZZZZZZZZZZZZZZZZZZZZZZZZZ",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EncodeULID(tt.ms, tt.entropy); got != tt.want {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestULIDSortsByTime(t *testing.T) {
	var ids []string
	for ms := int64(1000); ms < 1400; ms += 7 {
		ids = append(ids, EncodeULID(ms, [ULIDEntropy]byte{9}))
	}
	if !slices.IsSorted(ids) {
		t.Error("ULIDs do not sort by time")
	}
}
