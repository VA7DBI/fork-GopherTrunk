package plutoplus

import "github.com/MattCheramie/GopherTrunk/internal/sdr"

func init() {
	sdr.Register(New(nil))
}
