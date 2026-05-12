//go:build linux

package player

import (
	"testing"
	"unsafe"
)

func TestParseIoctlDevice(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		wantCard   uint32
		wantDevice uint32
		wantErr    bool
	}{
		{"empty defaults to 0,0", "", 0, 0, false},
		{"\"default\" defaults to 0,0", "default", 0, 0, false},
		{"hw:0,0 explicit", "hw:0,0", 0, 0, false},
		{"hw:1,0 second card", "hw:1,0", 1, 0, false},
		{"hw:0,2 second device on card 0", "hw:0,2", 0, 2, false},
		{"hw:2,3 card 2 device 3", "hw:2,3", 2, 3, false},
		{"missing hw prefix fails", "0,0", 0, 0, true},
		{"missing comma fails", "hw:0", 0, 0, true},
		{"non-numeric card fails", "hw:a,0", 0, 0, true},
		{"non-numeric device fails", "hw:0,a", 0, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			card, device, err := parseIoctlDevice(tc.in)
			if (err != nil) != tc.wantErr {
				t.Errorf("err = %v, want err? %v", err, tc.wantErr)
			}
			if !tc.wantErr {
				if card != tc.wantCard || device != tc.wantDevice {
					t.Errorf("got card=%d device=%d, want %d,%d",
						card, device, tc.wantCard, tc.wantDevice)
				}
			}
		})
	}
}

// TestIoctlRequestEncoding pins the ioctl-number encoding against
// the well-known kernel-published values for SNDRV_PCM_IOCTL_*.
// The reference numbers come from the v6.x include/uapi/sound/asound.h
// header; they're stable since libasound 1.0 and any drift here
// would silently break audio on every Linux box.
func TestIoctlRequestEncoding(t *testing.T) {
	// _IO('A', 0x40) for PREPARE — no payload, so size = 0.
	gotPrepare := ioctlRequest(_IOC_NONE, 'A', 0x40, 0)
	const wantPrepare uintptr = 0x4140
	if gotPrepare != wantPrepare {
		t.Errorf("ioctlPrepare = 0x%x, want 0x%x", gotPrepare, wantPrepare)
	}
	// _IO('A', 0x42) for START.
	gotStart := ioctlRequest(_IOC_NONE, 'A', 0x42, 0)
	const wantStart uintptr = 0x4142
	if gotStart != wantStart {
		t.Errorf("ioctlStart = 0x%x, want 0x%x", gotStart, wantStart)
	}
	// _IO('A', 0x44) for DRAIN.
	gotDrain := ioctlRequest(_IOC_NONE, 'A', 0x44, 0)
	const wantDrain uintptr = 0x4144
	if gotDrain != wantDrain {
		t.Errorf("ioctlDrain = 0x%x, want 0x%x", gotDrain, wantDrain)
	}
}

// TestSndPCMHwParamsSize pins the struct size against the kernel
// UAPI layout. On 64-bit Linux the struct is 608 bytes — the
// trailing fifo_size is unsigned long (8 bytes) requiring 8-byte
// alignment, which inserts 4 bytes of padding after the prior
// uint32 fields. On 32-bit Linux it would be 604 bytes (no
// padding); we'd need a separate constant + build tag for that.
//
// The ioctl number encoding embeds this size — if our Go struct
// drifts vs. the kernel's, HW_PARAMS calls return -ENOTTY
// because the kernel checks the embedded size.
func TestSndPCMHwParamsSize(t *testing.T) {
	const wantSize64 = 608
	got := unsafe.Sizeof(sndPCMHwParams{})
	if got != wantSize64 {
		t.Errorf("sizeof(sndPCMHwParams) = %d, want %d (64-bit Linux UAPI)", got, wantSize64)
	}
}

// TestSndMaskSize: 256-bit mask packed into 8 uint32s = 32 bytes.
func TestSndMaskSize(t *testing.T) {
	if got, want := unsafe.Sizeof(sndMask{}), uintptr(32); got != want {
		t.Errorf("sizeof(sndMask) = %d, want %d", got, want)
	}
}

// TestSndIntervalSize: 3 uint32 fields = 12 bytes.
func TestSndIntervalSize(t *testing.T) {
	if got, want := unsafe.Sizeof(sndInterval{}), uintptr(12); got != want {
		t.Errorf("sizeof(sndInterval) = %d, want %d", got, want)
	}
}

// TestNewIoctlALSABackend_NoDevice confirms the constructor returns
// an error (rather than panicking) when /dev/snd/pcmC*D*p is
// missing. On a CI runner without a sound card the kernel sound
// subsystem isn't loaded and the path doesn't exist; the daemon's
// Player.New wraps this in a degraded "null player" fallback.
func TestNewIoctlALSABackend_NoDevice(t *testing.T) {
	// Pick a card / device combination that's basically guaranteed
	// not to exist on a CI runner. 99 ought to do it.
	_, err := newIoctlALSABackend(Config{SampleRate: 8000}, "hw:99,99")
	if err == nil {
		t.Skip("hw:99,99 unexpectedly exists; can't test the no-device path here")
	}
}
