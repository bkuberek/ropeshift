package ledger

// ulidAlphabet is Crockford's base32, which sorts in the order of its values.
const ulidAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// ULIDEntropy is the number of random bytes in a ULID.
const ULIDEntropy = 10

// EncodeULID writes a 26-character ULID from a millisecond timestamp and 80 bits
// of entropy. The caller supplies both, because the domain reads neither a clock
// nor a random source.
func EncodeULID(ms int64, entropy [ULIDEntropy]byte) string {
	var raw [16]byte
	for i := range 6 {
		raw[i] = byte(uint64(ms) >> (40 - 8*i))
	}
	copy(raw[6:], entropy[:])

	var out [26]byte
	// 128 bits in 130 bits of output: the two top bits of the first character
	// are zero, so a shift by 125 bits gives its value.
	var acc uint
	bits := 2
	cursor := 0
	for _, b := range raw {
		acc = acc<<8 | uint(b)
		bits += 8
		for bits >= 5 {
			bits -= 5
			out[cursor] = ulidAlphabet[(acc>>bits)&31]
			cursor++
		}
	}
	return string(out[:])
}
