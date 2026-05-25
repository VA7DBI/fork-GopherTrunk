import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";

vi.mock("../api/spectrum", () => ({
  fetchSpectrumDevices: vi.fn(),
  openSpectrumStream: vi.fn(),
}));

import { fetchSpectrumDevices, openSpectrumStream } from "../api/spectrum";
import { useShared } from "../store/shared";
import { Spectrum } from "./Spectrum";

function resetStore() {
  useShared.setState({
    serverURL: "http://localhost:8080",
    token: null,
    connected: true,
    wsStatus: "idle",
    mutations: null,
    lastError: null,
    events: [],
    activeCalls: [],
    devices: [],
    systems: [],
    talkgroups: [],
    health: null,
    audio: null,
    scanner: null,
  });
}

describe("Spectrum panel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    resetStore();
  });

  it("renders an empty-state when no SDRs are available", async () => {
    vi.mocked(fetchSpectrumDevices).mockResolvedValue([]);
    render(<Spectrum />);
    await waitFor(() => {
      expect(screen.getByText("No SDRs available")).toBeInTheDocument();
    });
    // No stream should be opened when there's no device.
    expect(openSpectrumStream).not.toHaveBeenCalled();
  });

  it("opens a stream against the first device returned by the daemon", async () => {
    vi.mocked(fetchSpectrumDevices).mockResolvedValue([
      {
        serial: "rtl-1",
        driver: "rtlsdr",
        product: "NESDR",
        role: "control",
        center_hz: 851_012_500,
        sample_rate_hz: 2_048_000,
      },
    ]);
    vi.mocked(openSpectrumStream).mockReturnValue({ close: vi.fn() });

    render(<Spectrum />);

    await waitFor(() => {
      expect(openSpectrumStream).toHaveBeenCalledTimes(1);
    });
    const callArgs = vi.mocked(openSpectrumStream).mock.calls[0]?.[1];
    expect(callArgs?.serial).toBe("rtl-1");
  });

  it("surfaces an error message when device discovery fails", async () => {
    vi.mocked(fetchSpectrumDevices).mockRejectedValue(new Error("kaboom"));
    render(<Spectrum />);
    await waitFor(() => {
      expect(screen.getByText(/kaboom/)).toBeInTheDocument();
    });
  });

  it("shows live status pill when the WebSocket reports open", async () => {
    vi.mocked(fetchSpectrumDevices).mockResolvedValue([
      {
        serial: "rtl-1",
        driver: "rtlsdr",
        role: "control",
        center_hz: 100,
        sample_rate_hz: 2_048_000,
      },
    ]);
    vi.mocked(openSpectrumStream).mockImplementation((_cfg, opts) => {
      opts.onStatus?.("open");
      return { close: vi.fn() };
    });

    render(<Spectrum />);
    await waitFor(() => {
      expect(screen.getByText("live")).toBeInTheDocument();
    });
  });
});
