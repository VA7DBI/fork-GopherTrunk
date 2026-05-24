//go:build windows

package purego

import (
	"fmt"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestOpenUSBHintAccessDenied(t *testing.T) {
	wrapped := fmt.Errorf("winusb: CreateFile %q: %w", `\\?\usb#vid_0bda&pid_2838#00000006`, windows.ERROR_ACCESS_DENIED)
	hint := openUSBHint(wrapped)
	if hint == "" {
		t.Fatal("openUSBHint returned empty for ERROR_ACCESS_DENIED; expected remediation text")
	}
	if !strings.Contains(hint, "another process is holding the dongle") {
		t.Errorf("openUSBHint = %q; want substring about another process", hint)
	}
	if !strings.Contains(hint, "sdr.devices") {
		t.Errorf("openUSBHint = %q; want substring about sdr.devices duplicate-serial advice", hint)
	}
}

func TestOpenUSBHintUnrelatedError(t *testing.T) {
	if got := openUSBHint(fmt.Errorf("totally unrelated")); got != "" {
		t.Errorf("openUSBHint(unrelated) = %q; want empty string", got)
	}
}
