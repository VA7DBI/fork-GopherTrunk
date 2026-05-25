import { describe, expect, it } from "vitest";
import {
  formatP25Algorithm,
  formatP25KeyID,
  p25AlgorithmName,
} from "./p25Algorithm";

describe("p25AlgorithmName", () => {
  it.each([
    [0x80, "CLEAR"],
    [0x81, "DES-OFB"],
    [0x84, "AES-256"],
    [0x85, "AES-128"],
    [0xaa, "ADP/RC4"],
    [0x00, "unknown"],
    [0xff, "unknown"],
  ])("0x%s -> %s", (id, want) => {
    expect(p25AlgorithmName(id as number)).toBe(want);
  });
});

describe("formatP25Algorithm", () => {
  it("renders ID + mnemonic", () => {
    expect(formatP25Algorithm(0x84)).toBe("0x84 (AES-256)");
    expect(formatP25Algorithm(0x42)).toBe("0x42 (unknown)");
  });
});

describe("formatP25KeyID", () => {
  it("renders 16-bit hex with zero padding", () => {
    expect(formatP25KeyID(0x1234)).toBe("0x1234");
    expect(formatP25KeyID(0x0001)).toBe("0x0001");
    expect(formatP25KeyID(0xffff)).toBe("0xFFFF");
  });
});
