package tuner

import (
	"math"

	"github.com/MattCheramie/GopherTrunk/internal/dsp"
)

// nco is a recursive complex-exponential generator. Each Step call
// returns the current phasor and advances by step = e^{-j 2π f / Fs}.
// Phasor magnitude drifts away from unity over time due to repeated
// float32 multiplies; renorm() folds it back to 1 every renormalize
// samples. The fix-up is cheap (one sqrt) and the drift is bounded
// well below the bank's overall numeric noise floor in between.
type nco struct {
	phasor       complex64
	step         complex64
	sinceRenorm  int
	renormEveryN int
}

func newNCO(offsetHz, sampleRateHz float64) *nco {
	n := &nco{phasor: complex(1, 0), renormEveryN: 4096}
	n.setOffset(offsetHz, sampleRateHz)
	return n
}

func (n *nco) setOffset(offsetHz, sampleRateHz float64) {
	theta := -2 * math.Pi * offsetHz / sampleRateHz
	n.step = complex(float32(math.Cos(theta)), float32(math.Sin(theta)))
}

func (n *nco) reset() {
	n.phasor = complex(1, 0)
	n.sinceRenorm = 0
}

func (n *nco) renorm() {
	r := real(n.phasor)
	i := imag(n.phasor)
	mag := float32(math.Sqrt(float64(r)*float64(r) + float64(i)*float64(i)))
	if mag > 0 {
		n.phasor = complex(r/mag, i/mag)
	} else {
		n.phasor = complex(1, 0)
	}
	n.sinceRenorm = 0
}

// DDCBank is a Bank built from one independent NCO mixer + rational
// polyphase resampler per tap. CPU cost is O(N_taps · N_samples).
type DDCBank struct {
	inRateHz  float64
	outRateHz float64
	guardFrac float64
	taps      []*ddcTap
	mixBuf    []complex64
}

type ddcTap struct {
	offsetHz  float64
	nco       *nco
	resampler *dsp.Resampler
	outBuf    []complex64
	sink      SinkFunc
}

// NewDDCBank constructs a DDCBank that takes wide-band IQ at inRateHz and
// produces narrow-band IQ at outRateHz per tap. The guardFrac (0..0.5)
// reserves a fraction of the IQ band at each edge as a guard against
// alias roll-off; AddTap rejects offsets outside the usable band. A typical
// value is 0.05 (5%).
func NewDDCBank(inRateHz, outRateHz, guardFrac float64) *DDCBank {
	return &DDCBank{
		inRateHz:  inRateHz,
		outRateHz: outRateHz,
		guardFrac: guardFrac,
	}
}

// AddTap registers a new tap at the given offset.
func (b *DDCBank) AddTap(offsetHz float64, sink SinkFunc) error {
	if !b.offsetInBand(offsetHz) {
		return ErrOffsetOutOfBand
	}
	l, m := rationalRatio(b.outRateHz, b.inRateHz)
	tapsPerBranch := (ddcStopbandTaps*m + l - 1) / l
	if tapsPerBranch < ddcMinTapsPerBranch {
		tapsPerBranch = ddcMinTapsPerBranch
	}
	tap := &ddcTap{
		offsetHz:  offsetHz,
		nco:       newNCO(offsetHz, b.inRateHz),
		resampler: dsp.NewResampler(l, m, tapsPerBranch, ddcKaiserBeta),
		sink:      sink,
	}
	b.taps = append(b.taps, tap)
	return nil
}

func (b *DDCBank) offsetInBand(offsetHz float64) bool {
	half := b.inRateHz * (0.5 - b.guardFrac)
	return offsetHz >= -half && offsetHz <= half
}

// Process mixes each tap down to baseband and decimates to OutputRateHz,
// invoking each tap's sink with the resulting chunk.
func (b *DDCBank) Process(src []complex64) {
	if len(src) == 0 {
		for _, t := range b.taps {
			t.sink(nil)
		}
		return
	}
	if cap(b.mixBuf) < len(src) {
		b.mixBuf = make([]complex64, len(src))
	} else {
		b.mixBuf = b.mixBuf[:len(src)]
	}
	for _, t := range b.taps {
		mixInto(b.mixBuf, src, t.nco)
		t.outBuf = t.resampler.Process(t.outBuf, b.mixBuf)
		t.sink(t.outBuf)
	}
}

// mixInto multiplies src by the NCO phasor and writes to dst (len(dst) ==
// len(src) is the caller's contract). The phasor is advanced and
// periodically renormalised to stay on the unit circle.
func mixInto(dst, src []complex64, n *nco) {
	for i, x := range src {
		dst[i] = x * n.phasor
		n.phasor *= n.step
		n.sinceRenorm++
		if n.sinceRenorm >= n.renormEveryN {
			n.renorm()
		}
	}
}

// InputRateHz returns the wide-band sample rate.
func (b *DDCBank) InputRateHz() float64 { return b.inRateHz }

// OutputRateHz returns the per-tap narrow-band sample rate.
func (b *DDCBank) OutputRateHz() float64 { return b.outRateHz }

// Reset clears every tap's NCO and resampler state.
func (b *DDCBank) Reset() {
	for _, t := range b.taps {
		t.nco.reset()
		t.resampler.Reset()
	}
}

// Constants mirror internal/scanner/ccdecoder/ddc.go so the wide-band
// taps yield the same anti-alias characteristics as the existing
// single-channel down-converter (>60 dB stopband, ~70 dB peak sidelobe).
const (
	ddcStopbandTaps     = 12
	ddcMinTapsPerBranch = 8
	ddcKaiserBeta       = 7.0
)

// rationalRatio reduces out/in to lowest L/M terms. Pathologically large
// ratios (e.g. odd SDR rates) fall back to a pure integer decimator so
// the resampler doesn't allocate thousands of branches.
func rationalRatio(out, in float64) (l, m int) {
	outI := int(math.Round(out))
	inI := int(math.Round(in))
	if outI <= 0 || inI <= 0 {
		return 1, 1
	}
	g := gcdInt(outI, inI)
	l, m = outI/g, inI/g
	if l > 64 || m > 8192 {
		l = 1
		m = int(math.Round(float64(inI) / float64(outI)))
		if m < 1 {
			m = 1
		}
	}
	return l, m
}

func gcdInt(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	if a < 0 {
		a = -a
	}
	return a
}
