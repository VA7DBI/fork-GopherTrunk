package imbe

import (
	"math"

	"github.com/MattCheramie/GopherTrunk/internal/dsp/fft"
)

// IMBE 4400 unvoiced excitation — TIA-102.BABA §6.4.
//
// Voiced harmonics get the deterministic sinusoidal synthesis from
// step 4c (synth_voiced.go). Unvoiced harmonics get a noise-band
// excitation: white noise is FFT'd, bins under voiced harmonics
// (and bins outside the [1..L] model range) are zeroed, bins under
// unvoiced harmonics are multiplied by the per-harmonic amplitude
// Ml[l], and the result is IFFT'd back to the time domain.
//
// This file ships the spectrum-shaping kernel + the noise-driven
// pipeline. The caller passes pre-generated noise (rather than a
// noise source on SynthState) so unit tests stay deterministic;
// the high-level Decode() wiring that lands in step 4e + the
// post-merge Decode plumbing will pull noise from a seeded
// rand.Source attached to a per-call decoder.
//
// Algorithmic reference: TIA-102.BABA §6.4 + szechyjs/mbelib's
// unvoiced-synthesis loop (ISC-licensed; attribution preserved at
// the bottom of tables.go).

// UnvoicedFFTSize is the FFT length for the §6.4 noise spectrum.
// IMBE specifies 256 — long enough to give each harmonic band
// several bins for its noise excitation and short enough that the
// FFT is cheap. The 96-sample overlap (UnvoicedFFTSize − N) is
// used by the §6.4 overlap-add window in step 4e.
const UnvoicedFFTSize = 256

// ShapeUnvoicedSpectrum modifies spec in place: each FFT bin gets
// classified by the harmonic it falls under, then either zeroed
// (voiced harmonic, harmonic out of [1..L]) or scaled by Ml[l]
// (unvoiced harmonic). The mapping uses the bin's effective
// frequency (mirroring k > N/2 back through the conjugate
// symmetry) so the shape preserves real-valued output: scaling
// each (k, N−k) pair by the same real Ml[l] keeps the spectrum
// Hermitian-symmetric.
//
// The bin → harmonic mapping is l = round(2π·k_eff / (N · ω₀)) —
// IMBE's "closest harmonic centre" rule. Bins between l and l+1
// land in whichever harmonic's band they're closer to; the model
// has no per-bin partition between adjacent harmonics, just the
// nearest-centre snap.
//
// Silent + zero-L frames leave spec untouched (caller handles
// silence at a higher level).
func ShapeUnvoicedSpectrum(spec []complex128, p Params, M *[57]float64) {
	if p.Silent || p.L == 0 {
		return
	}
	if len(spec) != UnvoicedFFTSize {
		return
	}
	const twoPi = 2 * math.Pi
	N := UnvoicedFFTSize
	for k := 0; k < N; k++ {
		kEff := k
		if k > N/2 {
			kEff = N - k
		}
		f := twoPi * float64(kEff) / float64(N)
		l := int(math.Round(f / p.W0))
		if l < 1 || l > p.L || p.Vl[l] == 1 {
			spec[k] = 0
			continue
		}
		spec[k] *= complex(M[l], 0)
	}
}

// SynthUnvoicedFromNoise runs the full §6.4 unvoiced-excitation
// pipeline on a caller-supplied length-UnvoicedFFTSize noise
// buffer:
//
//  1. interpret noise[0..255] as a real time-domain signal,
//  2. forward-FFT to a 256-point complex spectrum,
//  3. ShapeUnvoicedSpectrum (zero voiced + out-of-range bins,
//     scale unvoiced bins by Ml[l]),
//  4. inverse-FFT back to a real time-domain signal,
//  5. accumulate the first SamplesPerFrame samples into dst.
//
// dst must be >= SamplesPerFrame; the function adds rather than
// overwrites so callers can sum it with the voiced-step output
// (step 4c, SynthVoiced) into the same buffer. Allocates one
// 256-complex spectrum + one fft.Plan per call.
//
// Noise input contract: the caller is responsible for noise
// statistics (Gaussian / unit-variance / seeded for tests). The
// IFFT result inherits whatever statistics the noise carried;
// callers wanting exact §6.4 RMS preservation should normalize
// upstream. This split keeps SynthUnvoicedFromNoise a pure
// function of its inputs (testable without a rand.Source).
//
// Silent + zero-L frames leave dst untouched. dst shorter than
// SamplesPerFrame, or noise of the wrong length, also leave
// dst untouched (caller short-circuits cleanly without a panic).
func SynthUnvoicedFromNoise(p Params, M *[57]float64, noise []float64, dst []float64) {
	if p.Silent || p.L == 0 {
		return
	}
	if len(noise) != UnvoicedFFTSize || len(dst) < SamplesPerFrame {
		return
	}
	plan := fft.New(UnvoicedFFTSize)
	spec := make([]complex128, UnvoicedFFTSize)
	for i, v := range noise {
		spec[i] = complex(v, 0)
	}
	spec = plan.Forward(spec, spec)
	ShapeUnvoicedSpectrum(spec, p, M)
	spec = plan.Inverse(spec, spec)
	for n := 0; n < SamplesPerFrame; n++ {
		dst[n] += real(spec[n])
	}
}
