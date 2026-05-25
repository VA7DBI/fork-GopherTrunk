package usb

// DriverBinding is one row of the per-OS "what is currently bound to
// this dongle" report consumed by `gophertrunk sdr doctor`. It lets
// operators see why a dongle that physically appears in lsusb /
// Device Manager refuses to open: the function driver bound at plug
// time isn't the one the pure-Go RTL-SDR transport expects.
//
// On Linux the expected state is "no driver" (the dongle is opened
// raw via usbdevfs). The auto-detach landed in usb_linux.go means
// most operators will see OK=true here even if dvb_usb_rtl28xxu was
// initially bound — the inspector reflects post-detach state.
//
// On Windows the expected driver is "WinUSB". RTL2832UUSB / RTL28xxBDA
// are the in-box DVB-T drivers Windows binds by default; libusbK and
// libusb0 are Zadig-installable but not what the transport speaks;
// usbccgp is the composite-device parent (the operator picked the
// wrong node in Zadig).
type DriverBinding struct {
	Descriptor Descriptor
	DriverName string
	DriverDesc string
	Expected   string
	OK         bool
	Hint       string
	Err        error
}

// DriverInspector is the platform-abstract API the doctor subcommand
// calls. One implementation per OS, registered via
// platformDriverInspector. Returning ErrUnsupportedPlatform from
// Inspect is acceptable on exotic targets.
type DriverInspector interface {
	Inspect(vid, pid uint16) ([]DriverBinding, error)
}

// DefaultDriverInspector returns the platform's inspector. Mirrors the
// shape of DefaultEnumerator so the doctor command needs no build
// tags of its own.
func DefaultDriverInspector() DriverInspector { return platformDriverInspector() }
