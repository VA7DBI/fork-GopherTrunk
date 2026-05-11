package purego

import "github.com/MattCheramie/GopherTrunk/internal/sdr"

// init registers the pure-Go RTL-SDR driver with the global SDR
// registry under the canonical name "rtlsdr". PR-08 made this the
// default (dropped the rtlsdr_purego build tag introduced in PR-06).
//
// The existing CGO librtlsdr backend continues to compile during one
// release of safety-net coexistence — with -tags rtlsdr_cgo it
// registers under "rtlsdr-cgo" alongside the pure-Go entry, so
// operators who hit a regression can fall back to the C library
// without rolling back the binary. PR-09 deletes that path
// entirely along with every `librtlsdr` apt / MSYS2 / DLL-bundling
// step in the build system.
func init() { sdr.Register(&Driver{}) }
