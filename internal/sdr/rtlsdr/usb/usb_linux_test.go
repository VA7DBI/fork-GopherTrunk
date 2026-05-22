//go:build linux && (amd64 || arm64 || 386 || arm || riscv64 || loong64)

package usb

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// fakeUSBDevice writes the sysfs files that linuxEnumerator.List reads.
type fakeUSBDevice struct {
	dir          string // sysfs entry name (e.g. "1-1.4")
	vid, pid     string // hex strings without 0x prefix, lowercase
	bus, dev     string // decimal
	serial       string
	manufacturer string
	product      string
	skipVID      bool // omit idVendor (simulates non-device sysfs entries)
}

func writeFakeSysfs(t *testing.T, devs []fakeUSBDevice) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range devs {
		entry := filepath.Join(root, d.dir)
		if err := os.MkdirAll(entry, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", entry, err)
		}
		write := func(name, val string) {
			if val == "" {
				return
			}
			if err := os.WriteFile(filepath.Join(entry, name), []byte(val+"\n"), 0o644); err != nil {
				t.Fatalf("write %s/%s: %v", entry, name, err)
			}
		}
		if !d.skipVID {
			write("idVendor", d.vid)
			write("idProduct", d.pid)
		}
		write("busnum", d.bus)
		write("devnum", d.dev)
		write("serial", d.serial)
		write("manufacturer", d.manufacturer)
		write("product", d.product)
	}
	return root
}

func TestLinuxEnumerator_ListParsesSysfs(t *testing.T) {
	root := writeFakeSysfs(t, []fakeUSBDevice{
		{dir: "1-1.4", vid: "0bda", pid: "2838", bus: "1", dev: "7",
			serial: "00000001", manufacturer: "Realtek", product: "RTL2838UHIDIR"},
		{dir: "1-2", vid: "0bda", pid: "2832", bus: "1", dev: "8",
			serial: "00000002", manufacturer: "Realtek", product: "RTL2832U"},
		{dir: "1-3", vid: "1d50", pid: "6089", bus: "1", dev: "9",
			serial: "hackrf-one"},
		{dir: "usb1", skipVID: true}, // root hub-style entry; must be skipped
	})

	e := &linuxEnumerator{sysfsRoot: root, devfsRoot: "/dev/bus/usb"}
	all, err := e.List(0, 0)
	if err != nil {
		t.Fatalf("List(0,0): %v", err)
	}
	if got, want := len(all), 3; got != want {
		t.Fatalf("List(0,0) len = %d, want %d (got %+v)", got, want, all)
	}

	rtl, err := e.List(0x0bda, 0)
	if err != nil {
		t.Fatalf("List vendor: %v", err)
	}
	if got, want := len(rtl), 2; got != want {
		t.Fatalf("Realtek len = %d, want %d", got, want)
	}

	exact, err := e.List(0x0bda, 0x2838)
	if err != nil {
		t.Fatalf("List exact: %v", err)
	}
	if len(exact) != 1 {
		t.Fatalf("exact match len = %d, want 1", len(exact))
	}
	d := exact[0]
	if d.Bus != 1 || d.Address != 7 || d.VID != 0x0bda || d.PID != 0x2838 {
		t.Errorf("descriptor = %+v, want {bus:1 addr:7 vid:0bda pid:2838}", d)
	}
	if d.Serial != "00000001" || d.Product != "RTL2838UHIDIR" || d.Manufacturer != "Realtek" {
		t.Errorf("strings = (%q,%q,%q), want (Realtek,RTL2838UHIDIR,00000001)", d.Manufacturer, d.Product, d.Serial)
	}
	if d.Path != "/dev/bus/usb/001/007" {
		t.Errorf("Path = %q, want /dev/bus/usb/001/007", d.Path)
	}
}

func TestLinuxEnumerator_ListSortedByBusThenAddress(t *testing.T) {
	root := writeFakeSysfs(t, []fakeUSBDevice{
		{dir: "2-1", vid: "0bda", pid: "2832", bus: "2", dev: "3"},
		{dir: "1-2", vid: "0bda", pid: "2832", bus: "1", dev: "9"},
		{dir: "1-1", vid: "0bda", pid: "2832", bus: "1", dev: "2"},
	})
	e := &linuxEnumerator{sysfsRoot: root}
	all, _ := e.List(0, 0)
	if len(all) != 3 {
		t.Fatalf("got %d descriptors", len(all))
	}
	if all[0].Bus != 1 || all[0].Address != 2 {
		t.Errorf("first = %+v, want bus=1 addr=2", all[0])
	}
	if all[1].Bus != 1 || all[1].Address != 9 {
		t.Errorf("second = %+v, want bus=1 addr=9", all[1])
	}
	if all[2].Bus != 2 || all[2].Address != 3 {
		t.Errorf("third = %+v, want bus=2 addr=3", all[2])
	}
}

func TestLinuxEnumerator_MissingRootReturnsNil(t *testing.T) {
	e := &linuxEnumerator{sysfsRoot: filepath.Join(t.TempDir(), "does-not-exist")}
	all, err := e.List(0, 0)
	if err != nil {
		t.Fatalf("List on missing root: %v", err)
	}
	if all != nil {
		t.Errorf("descriptors = %v, want nil", all)
	}
}

func TestLinuxEnumerator_BadHexSkipped(t *testing.T) {
	root := writeFakeSysfs(t, []fakeUSBDevice{
		{dir: "1-1", vid: "zzzz", pid: "2832", bus: "1", dev: "2"},
		{dir: "1-2", vid: "0bda", pid: "2832", bus: "1", dev: "3"},
	})
	e := &linuxEnumerator{sysfsRoot: root}
	all, _ := e.List(0, 0)
	if len(all) != 1 {
		t.Fatalf("got %d descriptors, want 1 (bad-hex entry should be skipped)", len(all))
	}
}

func TestIoctlEncodings(t *testing.T) {
	// USBDEVFS_DISCARDURB is _IO('U', 11) — direction NONE, size 0.
	// _IOC encoding: (0<<30) | ('U'<<8) | 11 = 0x550B.
	if got, want := usbdevfsDiscardURB, uintptr(0x550B); got != want {
		t.Errorf("usbdevfsDiscardURB = 0x%X, want 0x%X", got, want)
	}
	// USBDEVFS_RESET is _IO('U', 20) = 0x5514.
	if got, want := usbdevfsReset, uintptr(0x5514); got != want {
		t.Errorf("usbdevfsReset = 0x%X, want 0x%X", got, want)
	}
	// USBDEVFS_CLAIMINTERFACE is _IOR('U', 15, unsigned int) = 0x8004550F.
	if got, want := usbdevfsClaimInterface, uintptr(0x8004550F); got != want {
		t.Errorf("usbdevfsClaimInterface = 0x%X, want 0x%X", got, want)
	}
	// USBDEVFS_RELEASEINTERFACE is _IOR('U', 16, unsigned int) = 0x80045510.
	if got, want := usbdevfsReleaseInterface, uintptr(0x80045510); got != want {
		t.Errorf("usbdevfsReleaseInterface = 0x%X, want 0x%X", got, want)
	}
	// USBDEVFS_DISCONNECT is _IO('U', 22) — direction NONE, size 0.
	// _IOC encoding: (0<<30) | ('U'<<8) | 22 = 0x5516.
	if got, want := usbdevfsDisconnect, uintptr(0x5516); got != want {
		t.Errorf("usbdevfsDisconnect = 0x%X, want 0x%X", got, want)
	}
}

func TestIoctlEncodings_64Bit(t *testing.T) {
	// On 64-bit arches the struct sizes match the kernel's published
	// constants. On 32-bit they differ (pointers are 4 bytes), so the
	// ioctl number changes accordingly — we only assert the 64-bit
	// values here.
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("32-bit arch; skipping 64-bit ioctl constants")
	}
	// USBDEVFS_CONTROL is _IOWR('U', 0, 24) = 0xC0185500 on 64-bit.
	if got, want := usbdevfsControl, uintptr(0xC0185500); got != want {
		t.Errorf("usbdevfsControl = 0x%X, want 0x%X", got, want)
	}
	// USBDEVFS_SUBMITURB is _IOR('U', 10, 56) = 0x8038550A on 64-bit.
	if got, want := usbdevfsSubmitURB, uintptr(0x8038550A); got != want {
		t.Errorf("usbdevfsSubmitURB = 0x%X, want 0x%X", got, want)
	}
	// USBDEVFS_REAPURB is _IOW('U', 12, 8) = 0x4008550C on 64-bit.
	if got, want := usbdevfsReapURB, uintptr(0x4008550C); got != want {
		t.Errorf("usbdevfsReapURB = 0x%X, want 0x%X", got, want)
	}
	// USBDEVFS_IOCTL is _IOWR('U', 18, sizeof(struct usbdevfs_ioctl)).
	// On 64-bit the struct is {int, int, void*} = 16 bytes, so the
	// encoding is _IOWR('U', 18, 16) = 0xC0105512.
	if got, want := usbdevfsIoctlCmd, uintptr(0xC0105512); got != want {
		t.Errorf("usbdevfsIoctlCmd = 0x%X, want 0x%X", got, want)
	}
}

// TestClaimWithAutoDetach covers the EBUSY → detach → re-claim policy
// the Linux transport applies so a DVB-kernel-driver-bound RTL-SDR
// dongle still opens (the "claim interface 0: device or resource busy"
// report). The policy is a pure function over two closures, so it is
// exercised here without a real usbdevfs device node.
func TestClaimWithAutoDetach(t *testing.T) {
	t.Run("claim succeeds first try, detach not called", func(t *testing.T) {
		detachCalls := 0
		err := claimWithAutoDetach(
			func() error { return nil },
			func() error { detachCalls++; return nil },
		)
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if detachCalls != 0 {
			t.Errorf("detach called %d times, want 0 (no EBUSY)", detachCalls)
		}
	})

	t.Run("EBUSY then detach then re-claim succeeds", func(t *testing.T) {
		claimCalls, detachCalls := 0, 0
		err := claimWithAutoDetach(
			func() error {
				claimCalls++
				if claimCalls == 1 {
					return unix.EBUSY
				}
				return nil
			},
			func() error { detachCalls++; return nil },
		)
		if err != nil {
			t.Fatalf("err = %v, want nil (re-claim after detach must succeed)", err)
		}
		if claimCalls != 2 {
			t.Errorf("claim called %d times, want 2 (initial + post-detach retry)", claimCalls)
		}
		if detachCalls != 1 {
			t.Errorf("detach called %d times, want 1", detachCalls)
		}
	})

	t.Run("EBUSY survives detach and re-claim", func(t *testing.T) {
		err := claimWithAutoDetach(
			func() error { return unix.EBUSY },
			func() error { return nil },
		)
		if !errors.Is(err, unix.EBUSY) {
			t.Errorf("err = %v, want EBUSY (a user-space process still holds the interface)", err)
		}
	})

	t.Run("detach failure wraps both errors", func(t *testing.T) {
		detachErr := errors.New("disconnect ioctl failed")
		err := claimWithAutoDetach(
			func() error { return unix.EBUSY },
			func() error { return detachErr },
		)
		if !errors.Is(err, detachErr) {
			t.Errorf("err = %v, want it to wrap the detach failure", err)
		}
		if !errors.Is(err, unix.EBUSY) {
			t.Errorf("err = %v, want it to also wrap the original EBUSY", err)
		}
	})

	t.Run("non-EBUSY error passes through, detach not called", func(t *testing.T) {
		detachCalls := 0
		sentinel := errors.New("some other claim failure")
		err := claimWithAutoDetach(
			func() error { return sentinel },
			func() error { detachCalls++; return nil },
		)
		if !errors.Is(err, sentinel) {
			t.Errorf("err = %v, want the original non-EBUSY error", err)
		}
		if detachCalls != 0 {
			t.Errorf("detach called %d times, want 0 (detach is EBUSY-only)", detachCalls)
		}
	})
}
