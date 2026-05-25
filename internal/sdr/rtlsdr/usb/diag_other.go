//go:build !(linux && (amd64 || arm64 || 386 || arm || riscv64 || loong64)) && !(windows && (amd64 || arm64))

package usb

func platformDriverInspector() DriverInspector { return unsupportedInspector{} }

type unsupportedInspector struct{}

func (unsupportedInspector) Inspect(vid, pid uint16) ([]DriverBinding, error) {
	return nil, ErrUnsupportedPlatform
}
