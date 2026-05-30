// Package plutoplus provides a scaffold sdr.Driver for Pluto Plus SDR
// hardware. The wire protocol and sample path are intentionally stubbed
// so the driver can be linked, discovered, and tested while hardware
// support is developed.
package plutoplus

import (
	"context"
	"errors"
	"fmt"

	"github.com/MattCheramie/GopherTrunk/internal/sdr"
)

const driverName = "plutoplus"

var errStreamNotImplemented = errors.New("plutoplus: StreamIQ not implemented")

// Descriptor describes one discovered Pluto Plus radio.
type Descriptor struct {
	Serial       string
	Manufacturer string
	Product      string
}

// Enumerator abstracts hardware discovery for tests and future
// platform-specific enumerators.
type Enumerator interface {
	Enumerate() ([]Descriptor, error)
}

// Driver implements sdr.Driver for Pluto Plus.
type Driver struct {
	enum Enumerator
}

// New constructs a Pluto Plus driver. A nil enumerator intentionally
// yields an empty inventory until hardware probing is implemented.
func New(enum Enumerator) *Driver {
	return &Driver{enum: enum}
}

func (d *Driver) Name() string { return driverName }

func (d *Driver) Enumerate() ([]sdr.Info, error) {
	if d.enum == nil {
		return nil, nil
	}
	descs, err := d.enum.Enumerate()
	if err != nil {
		return nil, err
	}
	out := make([]sdr.Info, 0, len(descs))
	for i, desc := range descs {
		out = append(out, sdr.Info{
			Driver:       driverName,
			Index:        i,
			Serial:       desc.Serial,
			Manufacturer: desc.Manufacturer,
			Product:      desc.Product,
			TunerName:    "ADI Pluto Plus (stub)",
		})
	}
	return out, nil
}

func (d *Driver) Open(idx int) (sdr.Device, error) {
	infos, err := d.Enumerate()
	if err != nil {
		return nil, err
	}
	if idx < 0 || idx >= len(infos) {
		return nil, fmt.Errorf("plutoplus: index %d out of range", idx)
	}
	return &device{info: infos[idx]}, nil
}

type device struct {
	info sdr.Info
}

func (d *device) Info() sdr.Info                    { return d.info }
func (d *device) SetCenterFreq(uint32) error        { return nil }
func (d *device) SetSampleRate(uint32) error        { return nil }
func (d *device) SetGain(int) error                 { return nil }
func (d *device) SetPPM(int) error                  { return nil }
func (d *device) SetBiasTee(bool) error             { return nil }
func (d *device) StreamIQ(context.Context) (<-chan []complex64, error) {
	return nil, errStreamNotImplemented
}
func (d *device) Close() error { return nil }
