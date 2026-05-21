package phase1

// P25 LDU2 Encryption Sync word.
//
// An LDU2 carries an Encryption Sync (ES) word in the same 6 × 40-bit
// LC/ES slots an LDU1 uses for Link Control — so it is protected by the
// identical 24-codeword shortened Hamming(10,6,3) inner FEC, decoded
// here by lcInnerDecode (link_control.go). The ES identifies the
// privacy parameters of an encrypted voice call:
//
//   - MessageIndicator: the 72-bit per-call cryptographic sync vector
//   - AlgorithmID: the encryption algorithm (0x80 = unencrypted/clear,
//     0x81 = DES-OFB, 0x84 = AES-256, … per the TIA algorithm registry)
//   - KeyID: which key in the radio's keyset the call uses
//
// Like SDRtrunk, GopherTrunk identifies encryption — it surfaces the
// algorithm and key — but does not decrypt.
//
// The 96-bit ES content layout is the project's working model (the
// 144-bit Hamming-recovered field's first 12 octets); the RS(24,12,13)
// outer verification is a documented follow-up, as for Link Control.
//
//	octets 0-8 : Message Indicator (72 bits)
//	octet 9    : Algorithm ID
//	octets 10-11: Key ID
type EncryptionSync struct {
	MessageIndicator [9]byte
	AlgorithmID      uint8
	KeyID            uint16
}

// esContentOctets is the ES content size in octets (96 bits).
const esContentOctets = 12

// AlgorithmClear is the Algorithm ID a clear (unencrypted) call
// advertises in its Encryption Sync.
const AlgorithmClear uint8 = 0x80

// Encrypted reports whether the ES describes an encrypted call (any
// Algorithm ID other than the clear-voice value).
func (e EncryptionSync) Encrypted() bool { return e.AlgorithmID != AlgorithmClear }

// ParseEncryptionSync decodes the 6 LC/ES blocks of an LDU2 into a
// structured EncryptionSync, returning the inner-FEC corrected-error
// count.
func ParseEncryptionSync(blocks [LDULCESBlockCount][]byte) (EncryptionSync, int, error) {
	data, errs, err := lcInnerDecode(blocks)
	if err != nil {
		return EncryptionSync{}, 0, err
	}
	oct := bitsToOctets(data[:esContentOctets*8])
	var es EncryptionSync
	copy(es.MessageIndicator[:], oct[0:9])
	es.AlgorithmID = oct[9]
	es.KeyID = uint16(oct[10])<<8 | uint16(oct[11])
	return es, errs, nil
}

// AssembleEncryptionSync is the inverse of ParseEncryptionSync; it
// builds the 6 on-wire ES blocks. The RS-parity half of the 144-bit
// data field is left zero (see the package note above).
func AssembleEncryptionSync(es EncryptionSync) [LDULCESBlockCount][]byte {
	oct := make([]byte, esContentOctets)
	copy(oct[0:9], es.MessageIndicator[:])
	oct[9] = es.AlgorithmID
	oct[10], oct[11] = byte(es.KeyID>>8), byte(es.KeyID)

	data := make([]byte, 144)
	copy(data, octetsToBits(oct))
	return lcInnerEncode(data)
}
