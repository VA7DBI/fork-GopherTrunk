//go:build linux && (amd64 || arm64 || 386 || arm || riscv64 || loong64)

package usb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClassifyLinux_NoDriver(t *testing.T) {
	got := classifyLinux(Descriptor{VID: 0x0bda, PID: 0x2838}, "")
	if !got.OK {
		t.Fatalf("empty driver should be OK, got %+v", got)
	}
	if got.Hint != "" {
		t.Errorf("OK row should have no hint, got %q", got.Hint)
	}
}

func TestClassifyLinux_DVBBound(t *testing.T) {
	got := classifyLinux(Descriptor{VID: 0x0bda, PID: 0x2838}, "dvb_usb_rtl28xxu")
	if got.OK {
		t.Fatalf("dvb_usb_rtl28xxu should not be OK")
	}
	if !strings.Contains(got.Hint, "blacklist") {
		t.Errorf("hint should mention blacklist, got %q", got.Hint)
	}
}

func TestClassifyLinux_OtherDriver(t *testing.T) {
	got := classifyLinux(Descriptor{}, "some_other_driver")
	if got.OK {
		t.Fatalf("other driver should not be OK")
	}
	if !strings.Contains(got.Hint, "some_other_driver") {
		t.Errorf("hint should name the bound driver, got %q", got.Hint)
	}
}

// TestReadInterfaceDriver_FakeSysfs builds a minimal fake sysfs layout
// (matching the real /sys/bus/usb/devices/<bus>-<port>/<bus>-<port>:1.0/driver
// symlink shape) and confirms the reader returns the right basename.
func TestReadInterfaceDriver_FakeSysfs(t *testing.T) {
	root := t.TempDir()
	dev := filepath.Join(root, "1-2")
	iface := filepath.Join(dev, "1-2:1.0")
	if err := os.MkdirAll(iface, 0o755); err != nil {
		t.Fatal(err)
	}
	driverTarget := filepath.Join(root, "..", "drivers", "dvb_usb_rtl28xxu")
	if err := os.MkdirAll(driverTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(driverTarget, filepath.Join(iface, "driver")); err != nil {
		t.Fatal(err)
	}
	got := readInterfaceDriver(dev)
	if got != "dvb_usb_rtl28xxu" {
		t.Errorf("readInterfaceDriver = %q, want dvb_usb_rtl28xxu", got)
	}
}

func TestReadInterfaceDriver_NoDriverSymlink(t *testing.T) {
	root := t.TempDir()
	dev := filepath.Join(root, "1-2")
	iface := filepath.Join(dev, "1-2:1.0")
	if err := os.MkdirAll(iface, 0o755); err != nil {
		t.Fatal(err)
	}
	got := readInterfaceDriver(dev)
	if got != "" {
		t.Errorf("readInterfaceDriver = %q, want empty", got)
	}
}
