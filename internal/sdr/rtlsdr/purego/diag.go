package purego

// VIDPID is one row of the librtlsdr VID/PID whitelist exposed for
// out-of-tree consumers (today: cmd/gophertrunk's `sdr doctor`). Kept
// in a thin wrapper so the doctor command can iterate the table
// without importing the internal knownDevice type or duplicating the
// list — both of which would drift out of sync with librtlsdr.
type VIDPID struct {
	VID  uint16
	PID  uint16
	Name string
}

// KnownVIDPIDs returns a copy of the librtlsdr device whitelist. Safe
// to mutate; the underlying knownDevices slice stays unchanged.
func KnownVIDPIDs() []VIDPID {
	out := make([]VIDPID, len(knownDevices))
	for i, d := range knownDevices {
		out[i] = VIDPID{VID: d.VID, PID: d.PID, Name: d.Name}
	}
	return out
}
