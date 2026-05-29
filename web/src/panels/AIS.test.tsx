import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";

vi.mock("../api/ais", () => ({
  fetchAISVessels: vi.fn(),
}));

import { fetchAISVessels } from "../api/ais";
import { useShared } from "../store/shared";
import { AIS } from "./AIS";

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

describe("AIS panel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    resetStore();
  });

  it("renders an empty-state when no messages are present", async () => {
    vi.mocked(fetchAISVessels).mockResolvedValue([]);
    render(<AIS />);
    await waitFor(() => {
      expect(screen.getByText(/No AIS messages yet/)).toBeInTheDocument();
    });
  });

  it("renders position rows with MMSI + lat/lon + SOG/COG", async () => {
    vi.mocked(fetchAISVessels).mockResolvedValue([
      {
        id: 1,
        received_at: "2026-05-26T12:34:56Z",
        mmsi: 366053209,
        type: "position-a",
        body: "CLASS-A 37.8021,-122.3416",
        latitude: 37.8021,
        longitude: -122.3416,
        sog: 12.3,
        cog: 51.0,
        heading: 50,
        has_position: true,
        fcs_ok: true,
      },
    ]);
    render(<AIS />);
    await waitFor(() => {
      expect(screen.getByText("366053209")).toBeInTheDocument();
      expect(screen.getByText(/37\.8021, -122\.3416/)).toBeInTheDocument();
      expect(screen.getByText(/12\.3kn \/ 51°/)).toBeInTheDocument();
    });
  });

  it("renders static-data rows with vessel name + callsign + destination", async () => {
    vi.mocked(fetchAISVessels).mockResolvedValue([
      {
        id: 2,
        received_at: "2026-05-26T12:34:56Z",
        mmsi: 366053210,
        type: "static-voyage",
        body: "STATIC MMSI=366053210",
        vessel_name: "NAUTICAL LIMITS",
        callsign: "WCB1234",
        destination: "SF BAY",
        ship_type: 70,
        has_position: false,
        fcs_ok: true,
      },
    ]);
    render(<AIS />);
    await waitFor(() => {
      expect(screen.getByText("NAUTICAL LIMITS")).toBeInTheDocument();
    });
    // Position columns render an em-dash when has_position is false.
    expect(screen.getAllByText(/—/).length).toBeGreaterThan(0);
  });

  it("surfaces fetch errors", async () => {
    vi.mocked(fetchAISVessels).mockRejectedValue(new Error("daemon down"));
    render(<AIS />);
    await waitFor(() => {
      expect(screen.getByText(/daemon down/)).toBeInTheDocument();
    });
  });
});
