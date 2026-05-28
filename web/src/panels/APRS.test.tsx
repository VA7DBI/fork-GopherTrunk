import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";

vi.mock("../api/aprs", () => ({
  fetchAPRSPackets: vi.fn(),
}));

import { fetchAPRSPackets } from "../api/aprs";
import { useShared } from "../store/shared";
import { APRS } from "./APRS";

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

describe("APRS panel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    resetStore();
  });

  it("renders an empty-state when no packets are present", async () => {
    vi.mocked(fetchAPRSPackets).mockResolvedValue([]);
    render(<APRS />);
    await waitFor(() => {
      expect(screen.getByText(/No APRS packets yet/)).toBeInTheDocument();
    });
  });

  it("renders rows with src, type, body, and coordinates", async () => {
    vi.mocked(fetchAPRSPackets).mockResolvedValue([
      {
        id: 1,
        received_at: "2026-05-26T12:34:56Z",
        src: "W1AW-9",
        dst: "APRS",
        path: "WIDE1-1",
        type: "position",
        body: "49.06,-72.03 Test",
        latitude: 49.0583,
        longitude: -72.0292,
        fcs_ok: true,
      },
    ]);
    render(<APRS />);
    await waitFor(() => {
      expect(screen.getByText("W1AW-9")).toBeInTheDocument();
      expect(screen.getByText(/49\.06,-72\.03 Test/)).toBeInTheDocument();
      expect(screen.getByText(/WIDE1-1/)).toBeInTheDocument();
      // The coordinate column renders the lat/lon at 4-decimal precision.
      expect(screen.getByText(/49\.0583, -72\.0292/)).toBeInTheDocument();
    });
  });

  it("surfaces fetch errors", async () => {
    vi.mocked(fetchAPRSPackets).mockRejectedValue(new Error("daemon down"));
    render(<APRS />);
    await waitFor(() => {
      expect(screen.getByText(/daemon down/)).toBeInTheDocument();
    });
  });
});
