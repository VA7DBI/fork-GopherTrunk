// P25 TIA-102.AACE-A Algorithm ID registry lookup. Mirrors
// internal/radio/p25/algorithm.go so log lines, the TUI, and the SPA
// render encryption metadata identically.

export function p25AlgorithmName(id: number): string {
  switch (id) {
    case 0x80:
      return "CLEAR";
    case 0x81:
      return "DES-OFB";
    case 0x83:
      return "TDES-2";
    case 0x84:
      return "AES-256";
    case 0x85:
      return "AES-128";
    case 0x86:
      return "TDES";
    case 0x89:
      return "AES-256-OFB";
    case 0xaa:
      return "ADP/RC4";
    case 0x9f:
      return "DES-XL";
    default:
      return "unknown";
  }
}

// formatP25Algorithm renders id as "0x84 (AES-256)" — the same format
// the daemon uses in its log lines so operators can correlate UI rows
// with their decoded-message log.
export function formatP25Algorithm(id: number): string {
  return `0x${id.toString(16).toUpperCase().padStart(2, "0")} (${p25AlgorithmName(id)})`;
}

// formatP25KeyID renders the key ID as a 16-bit hex value.
export function formatP25KeyID(id: number): string {
  return `0x${id.toString(16).toUpperCase().padStart(4, "0")}`;
}
