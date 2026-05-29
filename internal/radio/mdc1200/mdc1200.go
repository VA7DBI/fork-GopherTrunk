// Package mdc1200 decodes Motorola MDC1200 signaling bursts.
//
// MDC1200 ("Motorola Data Communications") is the analog in-band
// data burst Motorola two-way radios key at the start and/or end of
// a transmission. It carries the radio's unit ID (ANI — automatic
// number identification) plus emergency, status, call-alert,
// radio-check and selective-call signaling on otherwise-analog
// conventional VHF / UHF voice channels. Scanner operators use it to
// see *which* radio is transmitting and to surface emergency / status
// events on systems that are otherwise just FM voice.
//
// On the air it is a 1200-baud FFSK burst (CCIR tones: mark = 1200 Hz,
// space = 1800 Hz) carried inside the narrowband-FM voice channel —
// the same modulation class GopherTrunk already demodulates for
// MPT 1327 and APRS, so the DSP frontend (internal/radio/mdc1200/afsk)
// reuses internal/dsp/demod.FFSK. Unlike APRS the line code is plain
// NRZ, not NRZI.
//
// This package owns the protocol layer only: a stream of demodulated
// NRZ bits is framed by internal/radio/mdc1200/receiver, which hands
// each captured 112-bit data block to DecodeFrame here and gets back
// a typed Message (op / arg / unit ID, CRC-checked).
//
// Frame layout (after the 40-bit sync word, most-significant bit
// first): 112 payload bits, column-interleaved over a 16×7 grid. The
// de-interleaved, LSB-first-packed bytes are
//
//	data[0] = op, data[1] = arg, data[2:4] = unit ID (big-endian),
//	data[4:6] = CRC-16 of data[0:4] (little-endian on the wire),
//	data[6:14] = redundancy (used by the over-the-air FEC; not yet
//	             exploited here).
//
// The CRC is CRC-16/CCITT with reflected in/out, polynomial 0x1021,
// initial value 0x0000 and final XOR 0xFFFF.
//
// This is a clean-room implementation written from the public MDC1200
// protocol description (sync word, interleave geometry, CRC parameters
// and opcode semantics are protocol facts); no third-party decoder
// source is incorporated.
package mdc1200

import (
	"encoding/hex"
	"fmt"
)

const (
	// SyncWord is the 40-bit MDC1200 frame synchronization word,
	// most-significant bit first.
	SyncWord uint64 = 0x07092A446F

	// SyncBits is the length of SyncWord in bits.
	SyncBits = 40

	// FrameBits is the number of payload bits captured after the
	// sync word — one interleaved data block.
	FrameBits = 112
)

// Message is one decoded MDC1200 data block.
type Message struct {
	Op        uint8  // operation code (data[0])
	Arg       uint8  // operation argument (data[1])
	UnitID    uint16 // transmitting radio's unit ID (data[2:4])
	Operation string // human label from the op/arg table; "" when unrecognised
	Body      string // one-line summary for logs / panel
	RawHex    string // hex of the six decoded header bytes (op, arg, id, crc)
	CRCOK     bool   // the CRC over data[0:4] matched the received value

	// DoublePacket is true when Op selects an extended (two-block)
	// message. Extra carries the raw decoded header bytes of the
	// second block when present; interpreting its vendor-specific
	// payload is left to a follow-up.
	DoublePacket bool
	Extra        []byte
}

// DecodeFrame de-interleaves and decodes one captured 112-bit data
// block (one byte per bit, only bit 0 of each is read) into a Message.
// The bool reports whether the CRC validated; a CRC failure still
// returns the best-effort decode so the caller can surface marginal
// bursts if it wants to.
func DecodeFrame(bits []byte) (Message, bool) {
	data, ok := deinterleave(bits)
	if !ok {
		return Message{Operation: "incomplete"}, false
	}
	return decodeData(data), validCRC(data)
}

// decodeData builds a Message from the 14 de-interleaved bytes. It
// reads the header (op, arg, unit ID), checks the CRC and resolves
// the operation label.
func decodeData(data []byte) Message {
	m := Message{
		Op:     data[0],
		Arg:    data[1],
		UnitID: uint16(data[2])<<8 | uint16(data[3]),
		RawHex: hex.EncodeToString(data[:6]),
		CRCOK:  validCRC(data),
	}
	m.Operation = opLabel(m.Op, m.Arg)
	m.DoublePacket = isDoublePacket(m.Op)
	m.Body = m.summary()
	return m
}

// isDoublePacket reports whether an op selects a two-block message.
func isDoublePacket(op uint8) bool {
	return op == 0x35 || op == 0x55
}

// validCRC checks the received CRC against a freshly-computed one
// over the four header bytes.
func validCRC(data []byte) bool {
	if len(data) < 6 {
		return false
	}
	rcrc := uint16(data[5])<<8 | uint16(data[4])
	return crc16(data[:4]) == rcrc
}

// deinterleave reverses the 16×7 column interleave and packs the
// result LSB-first into 14 bytes. Returns false if fewer than
// FrameBits bits are supplied.
func deinterleave(bits []byte) ([]byte, bool) {
	if len(bits) < FrameBits {
		return nil, false
	}
	var lbits [FrameBits]byte
	idx := 0
	for i := 0; i < 16; i++ {
		for j := 0; j < 7; j++ {
			lbits[idx] = bits[j*16+i] & 1
			idx++
		}
	}
	data := make([]byte, 14)
	for i := range data {
		var b byte
		for j := 0; j < 8; j++ {
			if lbits[i*8+j] != 0 {
				b |= 1 << uint(j)
			}
		}
		data[i] = b
	}
	return data, true
}

// crc16 computes the MDC1200 CRC: CRC-16/CCITT with reflected input
// and output, polynomial 0x1021 (reflected 0x8408), initial value
// 0x0000 and final XOR 0xFFFF.
func crc16(data []byte) uint16 {
	crc := uint16(0x0000)
	for _, b := range data {
		crc ^= uint16(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0x8408
			} else {
				crc >>= 1
			}
		}
	}
	return ^crc
}

// opLabel maps an (op, arg) pair to a human-readable operation name.
//
// The mappings below follow the widely-published MDC1200 opcode
// conventions used by Motorola CPS and common third-party tooling.
// They are best-effort and intentionally non-exhaustive — many
// vendor-specific and extended opcodes exist. Unrecognised pairs
// return "" so the caller surfaces the raw op/arg instead.
func opLabel(op, arg uint8) string {
	switch op {
	case 0x01:
		// PTT ID / ANI — keyed at the head (0x80) or tail (0x00).
		if arg == 0x00 {
			return "PTT ID (end)"
		}
		return "PTT ID"
	case 0x00:
		if arg == 0x90 {
			return "Emergency"
		}
		return "Emergency"
	case 0x06:
		return "Status request"
	case 0x12:
		return fmt.Sprintf("Status %d", arg)
	case 0x2B:
		return "Radio inhibit (stun)"
	case 0x2C:
		return "Radio enable (revive)"
	case 0x22:
		return "Message"
	case 0x35:
		return "Voice selective call"
	case 0x46:
		return "Remote monitor"
	case 0x63:
		return "Radio check"
	case 0x0A:
		return "Call alert / page"
	}
	return ""
}

// summary renders the one-line Body string used by logs and the panel.
func (m Message) summary() string {
	op := m.Operation
	if op == "" {
		op = fmt.Sprintf("op=0x%02X arg=0x%02X", m.Op, m.Arg)
	}
	s := fmt.Sprintf("Unit %04X: %s", m.UnitID, op)
	if !m.CRCOK {
		s += " (CRC?)"
	}
	return s
}
