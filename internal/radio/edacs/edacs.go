// Package edacs decodes Enhanced Digital Access Communications System
// trunked control channels (also marketed as GE-Marc / Ericsson EDACS).
// EDACS uses a continuous 9600-baud control channel over which 40-bit
// Control Channel Words (CCWs) flow, prefaced by a sync sequence and
// protected by an interleaved Reed-Solomon-derived FEC.
//
// Files:
//
//   sync.go       Standard EDACS control-channel sync word + a
//                 sliding correlator over a bit stream.
//   ccw.go        CCW = {Command, Status, Address, LCN, Aux} packed
//                 into 40 bits. Round-trip assemble / parse helpers.
//   opcodes.go    Command enum + per-command payload accessors
//                 (Group Voice Grant, Data Grant, System ID,
//                 Adjacent Site, Idle).
//   bandplan.go   LCN → Hz resolver. Linear and table strategies,
//                 same shape as the Motorola package's resolver.
//   control.go    Control-channel state machine ingesting CCWs and
//                 emitting cc.locked / grant events on the bus.
//
// What's NOT yet wired (honest deferrals so a contributor can pick
// these up):
//
//   - The 9600-baud GMSK demodulator that turns IQ into the bits this
//     package consumes. Same shape gap as the Motorola MSK demod.
//   - The interleaved-Reed-Solomon FEC over each CCW. The CCW parser
//     here assumes the upstream caller has already corrected errors.
//   - EDACS ProVoice voice frames (the digital-voice variant uses a
//     proprietary AMBE-derived vocoder; see docs/vocoders.md).
//   - Vendor-specific command extensions. The Command list focuses on
//     the standard EDACS subset that carries voice grants and system
//     identification.
//
// All of the above slot into the existing engine + recorder + composer
// pipeline through events.KindGrant once they ship.
package edacs
