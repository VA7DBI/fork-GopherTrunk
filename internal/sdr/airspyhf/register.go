package airspyhf

import "github.com/MattCheramie/GopherTrunk/internal/sdr"

// Register the Airspy HF+ driver under its canonical name so a blank
// import of this package from cmd/gophertrunk makes the device
// discoverable through sdr.Pool.Open.
func init() { sdr.Register(New(nil)) }
