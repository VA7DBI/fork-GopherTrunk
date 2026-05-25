//go:build windows && (amd64 || arm64)

package usb

import (
	"strings"
	"testing"
)

func TestClassifyWindows_WinUSB(t *testing.T) {
	got := classifyWindows(Descriptor{}, "WinUSB", "Universal Serial Bus device")
	if !got.OK {
		t.Fatalf("WinUSB should be OK, got %+v", got)
	}
	if got.Hint != "" {
		t.Errorf("OK row should have no hint, got %q", got.Hint)
	}
}

func TestClassifyWindows_DVBDriver(t *testing.T) {
	for _, svc := range []string{"RTL2832UUSB", "rtl28xxbda", "RTL28xxBDA"} {
		got := classifyWindows(Descriptor{}, svc, "Realtek 2832U Reference Design")
		if got.OK {
			t.Errorf("%s should not be OK", svc)
		}
		if !strings.Contains(got.Hint, "Zadig") {
			t.Errorf("hint for %s should mention Zadig, got %q", svc, got.Hint)
		}
	}
}

func TestClassifyWindows_LibusbK(t *testing.T) {
	got := classifyWindows(Descriptor{}, "libusbK", "libusbK USB device")
	if got.OK {
		t.Fatalf("libusbK should not be OK")
	}
	if !strings.Contains(got.Hint, "WinUSB") {
		t.Errorf("hint should steer to WinUSB, got %q", got.Hint)
	}
}

func TestClassifyWindows_Composite(t *testing.T) {
	got := classifyWindows(Descriptor{}, "usbccgp", "USB Composite Device")
	if got.OK {
		t.Fatalf("usbccgp should not be OK")
	}
	if !strings.Contains(got.Hint, "Interface 0") {
		t.Errorf("hint should mention Interface 0, got %q", got.Hint)
	}
}

func TestClassifyWindows_Empty(t *testing.T) {
	got := classifyWindows(Descriptor{}, "", "")
	if got.OK {
		t.Fatalf("empty driver should not be OK on Windows")
	}
	if !strings.Contains(got.Hint, "Device Manager") {
		t.Errorf("hint should mention Device Manager, got %q", got.Hint)
	}
}

func TestClassifyWindows_Unknown(t *testing.T) {
	got := classifyWindows(Descriptor{}, "SomeDriver", "Some Driver Description")
	if got.OK {
		t.Fatalf("unknown driver should not be OK")
	}
	if !strings.Contains(got.Hint, "SomeDriver") {
		t.Errorf("hint should name the bound driver, got %q", got.Hint)
	}
}

func TestFallbackString(t *testing.T) {
	if got := fallbackString("", "x"); got != "x" {
		t.Errorf("fallbackString empty = %q, want x", got)
	}
	if got := fallbackString("y", "x"); got != "y" {
		t.Errorf("fallbackString non-empty = %q, want y", got)
	}
}

func TestParens(t *testing.T) {
	if got := parens(""); got != "" {
		t.Errorf("parens empty = %q, want empty", got)
	}
	if got := parens("desc"); got != " (desc)" {
		t.Errorf("parens(\"desc\") = %q, want \" (desc)\"", got)
	}
}
