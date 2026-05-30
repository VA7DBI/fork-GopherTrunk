package plutoplus

import (
	"context"
	"errors"
	"testing"
)

type stubEnumerator struct {
	descs []Descriptor
	err   error
}

func (s stubEnumerator) Enumerate() ([]Descriptor, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.descs, nil
}

func TestName(t *testing.T) {
	if got := New(nil).Name(); got != driverName {
		t.Fatalf("Name() = %q, want %q", got, driverName)
	}
}

func TestEnumerateNilEnumeratorIsEmpty(t *testing.T) {
	infos, err := New(nil).Enumerate()
	if err != nil {
		t.Fatalf("Enumerate() err = %v", err)
	}
	if len(infos) != 0 {
		t.Fatalf("Enumerate() len = %d, want 0", len(infos))
	}
}

func TestEnumerateMapsDescriptors(t *testing.T) {
	infos, err := New(stubEnumerator{descs: []Descriptor{{
		Serial:       "PLUTO-001",
		Manufacturer: "Analog Devices",
		Product:      "Pluto Plus",
	}}}).Enumerate()
	if err != nil {
		t.Fatalf("Enumerate() err = %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("Enumerate() len = %d, want 1", len(infos))
	}
	if infos[0].Driver != driverName || infos[0].Index != 0 || infos[0].Serial != "PLUTO-001" {
		t.Fatalf("Enumerate() info = %+v", infos[0])
	}
	if infos[0].TunerName != "ADI Pluto Plus (stub)" {
		t.Fatalf("Enumerate() tuner = %q, want ADI Pluto Plus (stub)", infos[0].TunerName)
	}
}

func TestEnumeratePropagatesErrors(t *testing.T) {
	wantErr := errors.New("probe failed")
	_, err := New(stubEnumerator{err: wantErr}).Enumerate()
	if !errors.Is(err, wantErr) {
		t.Fatalf("Enumerate() err = %v, want %v", err, wantErr)
	}
}

func TestOpenIndexBounds(t *testing.T) {
	_, err := New(stubEnumerator{}).Open(0)
	if err == nil {
		t.Fatal("Open(0) expected error for empty inventory")
	}
}

func TestOpenReturnsStubDevice(t *testing.T) {
	drv := New(stubEnumerator{descs: []Descriptor{{
		Serial:       "PLUTO-001",
		Manufacturer: "Analog Devices",
		Product:      "Pluto Plus",
	}}})
	dev, err := drv.Open(0)
	if err != nil {
		t.Fatalf("Open(0) err = %v", err)
	}
	info := dev.Info()
	if info.Driver != driverName || info.Serial != "PLUTO-001" {
		t.Fatalf("Open(0) info = %+v", info)
	}
	if err := dev.SetCenterFreq(162550000); err != nil {
		t.Fatalf("SetCenterFreq() err = %v", err)
	}
	if err := dev.SetSampleRate(2400000); err != nil {
		t.Fatalf("SetSampleRate() err = %v", err)
	}
	if err := dev.SetGain(-1); err != nil {
		t.Fatalf("SetGain() err = %v", err)
	}
	if err := dev.SetPPM(0); err != nil {
		t.Fatalf("SetPPM() err = %v", err)
	}
	if err := dev.SetBiasTee(false); err != nil {
		t.Fatalf("SetBiasTee() err = %v", err)
	}
	if _, err := dev.StreamIQ(context.Background()); !errors.Is(err, errStreamNotImplemented) {
		t.Fatalf("StreamIQ() err = %v, want %v", err, errStreamNotImplemented)
	}
	if err := dev.Close(); err != nil {
		t.Fatalf("Close() err = %v", err)
	}
}
