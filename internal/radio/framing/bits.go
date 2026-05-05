// Package framing provides the bit-level primitives shared across P25, DMR,
// and NXDN: bit packing, CRC, Hamming, Golay, and convolutional/Viterbi
// decoders. Each protocol package consumes these primitives so the FEC
// implementations live in one place.
package framing

// PackBitsMSB packs a slice of 0/1 values into bytes, MSB-first.
func PackBitsMSB(bits []byte) []byte {
	out := make([]byte, (len(bits)+7)/8)
	for i, b := range bits {
		if b&1 != 0 {
			out[i>>3] |= 1 << uint(7-(i&7))
		}
	}
	return out
}

// UnpackBitsMSB unpacks bytes into 0/1 values, MSB-first. Length n must not
// exceed len(src)*8.
func UnpackBitsMSB(src []byte, n int) []byte {
	if n > len(src)*8 {
		n = len(src) * 8
	}
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		if src[i>>3]&(1<<uint(7-(i&7))) != 0 {
			out[i] = 1
		}
	}
	return out
}

// DibitsToBits expands MSB-first dibits (each value 0..3) into pairs of bits.
// P25 mapping: dibit 0=00, 1=01, 2=10, 3=11.
func DibitsToBits(dibits []uint8) []byte {
	out := make([]byte, len(dibits)*2)
	for i, d := range dibits {
		out[2*i] = (d >> 1) & 1
		out[2*i+1] = d & 1
	}
	return out
}

// BitsToDibits packs a bit slice (MSB-first per dibit) back into dibits.
// Trailing odd bit, if any, is dropped.
func BitsToDibits(bits []byte) []uint8 {
	n := len(bits) / 2
	out := make([]uint8, n)
	for i := 0; i < n; i++ {
		out[i] = (bits[2*i]&1)<<1 | (bits[2*i+1] & 1)
	}
	return out
}

// PopCount64 returns the number of 1 bits in v.
func PopCount64(v uint64) int {
	v -= (v >> 1) & 0x5555555555555555
	v = (v & 0x3333333333333333) + ((v >> 2) & 0x3333333333333333)
	v = (v + (v >> 4)) & 0x0F0F0F0F0F0F0F0F
	return int((v * 0x0101010101010101) >> 56)
}

// HammingDistanceBits returns the count of positions where a and b differ.
func HammingDistanceBits(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	d := 0
	for i := 0; i < n; i++ {
		if a[i]&1 != b[i]&1 {
			d++
		}
	}
	return d
}
