package mpt1327

import (
	"errors"
	"fmt"
)

// Resolver maps an MPT 1327 channel number (the lower 13 bits of a
// GTC codeword's Function field) to a frequency in Hz. MPT 1327
// systems use vendor- and country-specific channel plans; both
// linear and table-backed strategies are exposed, mirroring the
// other protocol packages.
type Resolver interface {
	Frequency(channel uint16) (uint32, error)
}

// LinearBandPlan applies freq = BaseHz + (channel + Offset) * SpacingHz.
type LinearBandPlan struct {
	BaseHz    uint32
	SpacingHz uint32
	Offset    int
}

func (b LinearBandPlan) Frequency(ch uint16) (uint32, error) {
	if b.SpacingHz == 0 {
		return 0, errors.New("mpt1327/bandplan: SpacingHz must be > 0")
	}
	idx := int(ch) + b.Offset
	if idx < 0 {
		return 0, fmt.Errorf("mpt1327/bandplan: channel %d + offset %d went negative", ch, b.Offset)
	}
	return b.BaseHz + uint32(idx)*b.SpacingHz, nil
}

// TableBandPlan looks up explicit (channel → Hz) entries.
type TableBandPlan map[uint16]uint32

func (t TableBandPlan) Frequency(ch uint16) (uint32, error) {
	hz, ok := t[ch]
	if !ok {
		return 0, fmt.Errorf("mpt1327/bandplan: channel %d not in table", ch)
	}
	return hz, nil
}
