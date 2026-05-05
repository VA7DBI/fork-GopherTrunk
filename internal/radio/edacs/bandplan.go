package edacs

import (
	"errors"
	"fmt"
)

// BandPlan resolves an EDACS Logical Channel Number (LCN) to a
// frequency in Hz. EDACS deployments commonly use a linear plan over
// 800 / 900 MHz NPSPAC bands; some VHF / UHF systems use irregular
// per-LCN tables. Mirror of internal/radio/motorola/bandplan.go.
type Resolver interface {
	Frequency(lcn uint8) (uint32, error)
}

// LinearBandPlan applies freq = BaseHz + (lcn + Offset) * SpacingHz.
type LinearBandPlan struct {
	BaseHz    uint32
	SpacingHz uint32
	Offset    int // signed; supports negative offsets
}

func (b LinearBandPlan) Frequency(lcn uint8) (uint32, error) {
	if b.SpacingHz == 0 {
		return 0, errors.New("edacs/bandplan: SpacingHz must be > 0")
	}
	idx := int(lcn) + b.Offset
	if idx < 0 {
		return 0, fmt.Errorf("edacs/bandplan: LCN %d + offset %d went negative", lcn, b.Offset)
	}
	return b.BaseHz + uint32(idx)*b.SpacingHz, nil
}

// TableBandPlan looks up explicit (lcn → Hz) entries.
type TableBandPlan map[uint8]uint32

func (t TableBandPlan) Frequency(lcn uint8) (uint32, error) {
	hz, ok := t[lcn]
	if !ok {
		return 0, fmt.Errorf("edacs/bandplan: LCN %d not in table", lcn)
	}
	return hz, nil
}
