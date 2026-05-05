package framing

// Trellis4State1Half is a 4-state, 1/2-rate (1 input bit → 1 output dibit)
// convolutional encoder/Viterbi decoder. The state transitions are supplied
// as tables so that protocol packages can plug in their specific code
// (e.g. P25 TIA-102.BAAA Annex A defines particular next-state/output
// tables; an equivalent representation is the generator polynomial pair
// (g1, g2) which Trellis4StatePoly converts into tables).
type Trellis4State1Half struct {
	// Next[state][input] = nextState
	Next [4][2]int
	// Out[state][input] = output dibit (0..3)
	Out [4][2]uint8
}

// Trellis4StatePoly returns a trellis configured from generator polynomials
// (g1, g2), each a 3-bit value where bit 2 is the input tap. (7,5) octal
// (= 0b111, 0b101) is the canonical 4-state K=3 1/2-rate code.
func Trellis4StatePoly(g1, g2 int) Trellis4State1Half {
	var t Trellis4State1Half
	for state := 0; state < 4; state++ {
		for in := 0; in < 2; in++ {
			// Shift register: [in, state_bit_1, state_bit_0]
			s := state | (in << 2)
			b1 := xorTaps(s, g1)
			b2 := xorTaps(s, g2)
			t.Out[state][in] = uint8((b1 << 1) | b2)
			t.Next[state][in] = (in << 1) | (state >> 1)
		}
	}
	return t
}

func xorTaps(reg, poly int) int {
	v := reg & poly
	parity := 0
	for v != 0 {
		parity ^= v & 1
		v >>= 1
	}
	return parity
}

// Encode produces output dibits for an input bit slice (each entry 0/1).
// The encoder starts in state 0 and ends in whatever state the input drives
// it to; callers that need a known final state should append flush bits.
func (t Trellis4State1Half) Encode(in []byte) []uint8 {
	out := make([]uint8, len(in))
	state := 0
	for i, b := range in {
		b &= 1
		out[i] = t.Out[state][b]
		state = t.Next[state][b]
	}
	return out
}

// DecodeHard runs hard-decision Viterbi over received dibits and returns the
// most-likely input bit sequence. The decoder starts in state 0; if final
// state is unknown, ending-state metric is taken across all four states.
func (t Trellis4State1Half) DecodeHard(rx []uint8) []byte {
	const inf = 1 << 30
	N := len(rx)
	// pathMetric[state] = accumulated Hamming-distance metric.
	pm := [4]int{0, inf, inf, inf}
	// trace[stage][state] = predecessor (state, input_bit) packed
	trace := make([][4]uint8, N)

	for i := 0; i < N; i++ {
		var npm [4]int
		for s := 0; s < 4; s++ {
			npm[s] = inf
		}
		for s := 0; s < 4; s++ {
			if pm[s] >= inf {
				continue
			}
			for b := 0; b < 2; b++ {
				ns := t.Next[s][b]
				cost := pm[s] + dibitDistance(t.Out[s][b], rx[i])
				if cost < npm[ns] {
					npm[ns] = cost
					trace[i][ns] = uint8((s << 1) | b) // s in bits 1..2, b in bit 0
				}
			}
		}
		pm = npm
	}

	// Find best terminal state.
	best := 0
	for s := 1; s < 4; s++ {
		if pm[s] < pm[best] {
			best = s
		}
	}

	// Trace back.
	out := make([]byte, N)
	state := best
	for i := N - 1; i >= 0; i-- {
		entry := trace[i][state]
		out[i] = entry & 1
		state = int(entry >> 1)
	}
	return out
}

func dibitDistance(a, b uint8) int {
	d := (a ^ b) & 0x3
	switch d {
	case 0:
		return 0
	case 1, 2:
		return 1
	default:
		return 2
	}
}
