package nxdn

import "testing"

func TestFrameLayoutSumsCorrectly(t *testing.T) {
	got := FSWDibits + LICHWireDibits + SACCHDibits + InfoFieldDibits
	if got != FrameDibits {
		t.Errorf("layout = %d, want %d", got, FrameDibits)
	}
	if FrameDibits*2 != FrameBits {
		t.Errorf("FrameBits inconsistent: %d != %d*2", FrameBits, FrameDibits)
	}
}

func TestFrameSlices(t *testing.T) {
	var f Frame
	for i := 0; i < FrameDibits; i++ {
		f.Dibits[i] = uint8(i % 4)
	}
	tests := []struct {
		name string
		got  []uint8
		size int
	}{
		{"FSW", f.FSW(), FSWDibits},
		{"LICH", f.LICH(), LICHWireDibits},
		{"SACCH", f.SACCH(), SACCHDibits},
		{"Info", f.Info(), InfoFieldDibits},
	}
	for _, tc := range tests {
		if len(tc.got) != tc.size {
			t.Errorf("%s: len = %d, want %d", tc.name, len(tc.got), tc.size)
		}
	}
}

func TestBaudRateString(t *testing.T) {
	if Rate4800.String() != "4800" || Rate9600.String() != "9600" {
		t.Errorf("BaudRate.String() mismatch: %s / %s", Rate4800.String(), Rate9600.String())
	}
}
