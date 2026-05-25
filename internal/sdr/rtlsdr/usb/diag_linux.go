//go:build linux && (amd64 || arm64 || 386 || arm || riscv64 || loong64)

package usb

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func platformDriverInspector() DriverInspector { return &linuxDriverInspector{} }

// linuxDriverInspector reports which kernel driver currently owns
// interface 0 of each matching dongle. Sources its data from sysfs
// (the same root linuxEnumerator walks), so a non-root operator can
// run `gophertrunk sdr doctor` without claiming any device.
type linuxDriverInspector struct {
	sysfsRoot string
}

func (l *linuxDriverInspector) sysfs() string {
	if l.sysfsRoot != "" {
		return l.sysfsRoot
	}
	return "/sys/bus/usb/devices"
}

func (l *linuxDriverInspector) Inspect(vid, pid uint16) ([]DriverBinding, error) {
	enum := &linuxEnumerator{sysfsRoot: l.sysfsRoot}
	descs, err := enum.List(vid, pid)
	if err != nil {
		return nil, err
	}
	root := l.sysfs()
	out := make([]DriverBinding, 0, len(descs))
	for _, d := range descs {
		sysName, ok := sysfsNameFor(root, d.Bus, d.Address)
		if !ok {
			out = append(out, DriverBinding{
				Descriptor: d,
				Expected:   "(none/usbfs)",
				Hint:       "Could not resolve sysfs entry for this device — re-plug the dongle and retry.",
			})
			continue
		}
		driver := readInterfaceDriver(filepath.Join(root, sysName))
		out = append(out, classifyLinux(d, driver))
	}
	return out, nil
}

// classifyLinux maps an interface-0 driver name (or "" for none) to
// the doctor row. Extracted as a pure function so the unit test can
// table-drive the policy without touching sysfs.
func classifyLinux(d Descriptor, driver string) DriverBinding {
	b := DriverBinding{
		Descriptor: d,
		DriverName: driver,
		Expected:   "(none/usbfs)",
	}
	switch driver {
	case "":
		b.OK = true
	case "dvb_usb_rtl28xxu":
		b.Hint = "Kernel bound the DVB-T driver. The transport auto-detaches it at open time, but to stop the kernel from binding it again, blacklist the module: echo 'blacklist dvb_usb_rtl28xxu' | sudo tee /etc/modprobe.d/blacklist-rtl.conf && sudo modprobe -r dvb_usb_rtl28xxu"
	default:
		b.Hint = fmt.Sprintf("Unexpected kernel driver %q bound. Detach with: sudo sh -c 'echo -n %s > /sys/bus/usb/drivers/%s/unbind'", driver, "<sysfs-name>", driver)
	}
	return b
}

// sysfsNameFor finds the /sys/bus/usb/devices entry whose busnum +
// devnum match (bus, addr). Sysfs entries are named like "1-2" or
// "1-2.3" — the bus/port topology, not the dynamic device number —
// so we have to scan rather than build the path directly.
func sysfsNameFor(root string, bus, addr uint8) (string, bool) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "usb") || strings.Contains(name, ":") {
			continue
		}
		path := filepath.Join(root, name)
		b, bOK := readUint8(filepath.Join(path, "busnum"))
		a, aOK := readUint8(filepath.Join(path, "devnum"))
		if !bOK || !aOK {
			continue
		}
		if b == bus && a == addr {
			return name, true
		}
	}
	return "", false
}

// readInterfaceDriver returns the basename of the driver symlink
// bound to interface 0, or "" when no driver is bound. RTL-SDR is a
// single-interface device, so interface 0 is the whole story.
func readInterfaceDriver(devicePath string) string {
	entries, err := os.ReadDir(devicePath)
	if err != nil {
		return ""
	}
	devName := filepath.Base(devicePath)
	ifaceSuffix := ":1.0"
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, devName+":") {
			continue
		}
		if !strings.HasSuffix(name, ifaceSuffix) {
			continue
		}
		link, err := os.Readlink(filepath.Join(devicePath, name, "driver"))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return ""
			}
			return ""
		}
		return filepath.Base(link)
	}
	return ""
}
